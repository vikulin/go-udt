package udt

import (
	"container/heap"
	"log"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

func (s *udtSocket) goReceiveEvent() {
	recvEvent := s.recvEvent
	closed := s.closed
	for {
		select {
		case _, ok := <-closed:
			return
		case evt, ok := <-recvEvent:
			if !ok {
				return
			}
			s.expCount = 1
			switch sp := evt.pkt.(type) {
			case *packet.Ack2Packet:
				s.ingestAck2(sp, evt.now)
			case *packet.MsgDropReqPacket:
				s.ingestMsgDropReq(sp, evt.now)
			case *packet.DataPacket:
				s.ingestData(sp)
			}
		}
	}
}

// owned by: multiplexer SYN loop
// called by the multiplexer on every SYN
func (s *udtSocket) onReceiveTick(m *multiplexer, t time.Time) {
	/* TODO: Query the system time to check if ACK, NAK, or EXP timer has
	expired. If there is any, process the event (as described below
	in this section) and reset the associated time variables. For
	ACK, also check the ACK packet interval. */
}

/*
ACK is used to trigger an acknowledgement (ACK). Its period is set by
   the congestion control module. However, UDT will send an ACK no
   longer than every 0.01 second, even though the congestion control
   does not need timer-based ACK. Here, 0.01 second is defined as the
   SYN time, or synchronization time, and it affects many of the other
   timers used in UDT.

   NAK is used to trigger a negative acknowledgement (NAK). Its period
   is dynamically updated to 4 * RTT_+ RTTVar + SYN, where RTTVar is the
   variance of RTT samples.

   EXP is used to trigger data packets retransmission and maintain
   connection status. Its period is dynamically updated to N * (4 * RTT
   + RTTVar + SYN), where N is the number of continuous timeouts. To
   avoid unnecessary timeout, a minimum threshold (e.g., 0.5 second)
   should be used in the implementation.
*/

// owned by: readPacket
// readData is called when a data packet has been received for this socket
func (s *udtSocket) readData(p *packet.DataPacket, now time.Time) {

	/* If the sequence number of the current data packet is 16n + 1,
	where n is an integer, record the time interval between this
	packet and the last data packet in the Packet Pair Window. */
	if (p.Seq-1)&0xf == 0 && s.recvPktHistory != nil {
		lastTime := s.recvPktHistory[len(s.recvPktHistory)-1]
		pairDist := now.Sub(lastTime)
		if s.recvPktPairHistory == nil {
			s.recvPktPairHistory = []time.Duration{pairDist}
		} else {
			s.recvPktPairHistory = append(s.recvPktPairHistory, pairDist)
			if len(s.recvPktPairHistory) > 16 {
				s.recvPktPairHistory = s.recvPktPairHistory[len(s.recvPktPairHistory)-16:]
			}
		}
	}

	// Record the packet arrival time in PKT History Window.
	if s.recvPktHistory == nil {
		s.recvPktHistory = []time.Time{now}
	} else {
		s.recvPktHistory = append(s.recvPktHistory, now)
		if len(s.recvPktHistory) > 16 {
			s.recvPktHistory = s.recvPktHistory[len(s.recvPktHistory)-16:]
		}
	}
}

func absdiff(a uint32, b uint32) uint32 {
	if a < b {
		return b - a
	}
	return a - b
}

// owned by: goReceiveEvent
// ingestAck2 is called to process an ACK2 packet
func (s *udtSocket) ingestAck2(p *packet.Ack2Packet, now time.Time) {
	ackSeq := p.AckSeqNo
	if s.ackHistory == nil {
		return // no ACKs to search
	}

	ackHistEntry, ackIdx := s.ackHistory.Find(ackSeq)
	if ackHistEntry == nil {
		return // this ACK not found
	}
	heap.Remove(&s.ackHistory, ackIdx)

	// Update the largest ACK number ever been acknowledged.
	if s.largestACK < ackSeq {
		s.largestACK = ackSeq
	}

	thisRTT := uint32(now.Sub(ackHistEntry.sendTime) / 1000)
	s.rtt = (s.rtt*7 + thisRTT) / 8
	s.rttVar = (s.rttVar*3 + absdiff(s.rtt, thisRTT)) / 4

	// Update both ACK and NAK period to 4 * RTT + RTTVar + SYN.*/
	s.resetAckNakPeriods()
	//s.rto = 4 * s.rtt + s.rttVar
}

// owned by: goReceiveEvent
// ingestMsgDropReq is called to process an message drop request packet
func (s *udtSocket) ingestMsgDropReq(p *packet.MsgDropReqPacket, now time.Time) {
	for pktID := p.FirstSeq; pktID <= p.LastSeq; pktID++ {
		// remove all these packets from the loss list
		if s.recvLossList != nil {
			if lossEntry, idx := s.recvLossList.Find(pktID); lossEntry != nil {
				heap.Remove(&s.recvLossList, idx)
			}
		}

		// remove all pending packets with this message
		if s.recvPktPend != nil {
			if lossEntry, idx := s.recvPktPend.Find(pktID); lossEntry != nil {
				heap.Remove(&s.recvPktPend, idx)
			}
		}
	}

	if s.recvLossList != nil && len(s.recvLossList) == 0 {
		s.recvLossList = nil
	}
	if s.recvPktPend != nil && len(s.recvPktPend) == 0 {
		s.recvPktPend = nil
	}

	// try to push any pending packets out, now that we have dropped any blocking packets
	for s.recvPktPend != nil {
		nextPkt, _ := s.recvPktPend.UpperBound(p.LastSeq)
		if nextPkt == nil || !s.attemptProcessPacket(nextPkt, false) {
			break
		}
	}
}

// owned by: goReceiveEvent
// ingestData is called to process a data packet
func (s *udtSocket) ingestData(p *packet.DataPacket) {
	seq := p.Seq
	/* If the sequence number of the current data packet is greater
	than LRSN + 1, put all the sequence numbers between (but
	excluding) these two values into the receiver's loss list and
	send them to the sender in an NAK packet. */
	if seq > s.farNextPktSeq {
		newLoss := make(receiveLossHeap, 0, seq-s.farNextPktSeq)
		for idx := s.farNextPktSeq; idx < seq; idx++ {
			newLoss = append(newLoss, recvLossEntry{packetID: seq})
		}

		if s.recvLossList == nil {
			s.recvLossList = newLoss
			heap.Init(&s.recvLossList)
		} else {
			for idx := s.farNextPktSeq; idx < seq; idx++ {
				heap.Push(&s.recvLossList, recvLossEntry{packetID: seq})
			}
		}

		s.sendNAK(newLoss)
		s.farNextPktSeq = seq + 1

	} else if seq < s.farNextPktSeq {
		// If the sequence number is less than LRSN, remove it from the receiver's loss list.
		if !s.recvLossList.Remove(seq) {
			return // already previously received packet -- ignore
		}

		if len(s.recvLossList) == 0 {
			s.recvLossList = nil
		}
	}

	s.attemptProcessPacket(p, true)
}

func (s *udtSocket) attemptProcessPacket(p *packet.DataPacket, isNew bool) bool {
	seq := p.Seq

	// can we process this packet?
	boundary, mustOrder, msgID := p.GetMessageData()
	if s.recvLossList != nil && mustOrder {
		minLostSeq := s.recvLossList.Min()
		if minLostSeq < seq {
			// we're required to order these packets and we're missing prior packets, so push and return
			if isNew {
				if s.recvPktPend == nil {
					s.recvPktPend = dataPacketHeap{p}
					heap.Init(&s.recvPktPend)
				} else {
					heap.Push(&s.recvPktPend, p)
				}
			}
			return false
		}
	}

	// can we find the start of this message?
	pieces := make([]*packet.DataPacket, 0)
	cannotContinue := false
	switch boundary {
	case packet.MbLast, packet.MbMiddle:
		// we need prior packets, let's make sure we have them
		if s.recvPktPend != nil {
			pieceSeq := seq - 1
			for {
				prevPiece, _ := s.recvPktPend.Find(pieceSeq)
				if prevPiece == nil {
					// we don't have the previous piece, is it missing?
					if s.recvLossList != nil {
						if lossEntry, _ := s.recvLossList.Find(pieceSeq); lossEntry != nil {
							// it's missing, stop processing
							cannotContinue = true
						}
					}
					// in any case we can't continue with this
					log.Printf("Message with id %s appears to be a broken fragment", msgID)
					break
				}
				prevBoundary, _, prevMsg := prevPiece.GetMessageData()
				if prevMsg != msgID {
					// ...oops? previous piece isn't in the same message
					log.Printf("Message with id %s appears to be a broken fragment", msgID)
					break
				}
				pieces = append([]*packet.DataPacket{prevPiece}, pieces...)
				if prevBoundary == packet.MbFirst {
					break
				}
				pieceSeq--
			}
		}
	}
	if !cannotContinue {
		pieces = append(pieces, p)

		switch boundary {
		case packet.MbFirst, packet.MbMiddle:
			// we need following packets, let's make sure we have them
			if s.recvPktPend != nil {
				pieceSeq := seq + 1
				for {
					nextPiece, _ := s.recvPktPend.Find(pieceSeq)
					if nextPiece == nil {
						// we don't have the previous piece, is it missing?
						if pieceSeq >= s.farNextPktSeq {
							// hasn't been received yet
							cannotContinue = true
						} else if s.recvLossList != nil {
							if lossEntry, _ := s.recvLossList.Find(pieceSeq); lossEntry != nil {
								// it's missing, stop processing
								cannotContinue = true
							}
						} else {
							log.Printf("Message with id %s appears to be a broken fragment", msgID)
						}
						// in any case we can't continue with this
						break
					}
					nextBoundary, _, nextMsg := nextPiece.GetMessageData()
					if nextMsg != msgID {
						// ...oops? previous piece isn't in the same message
						log.Printf("Message with id %s appears to be a broken fragment", msgID)
						break
					}
					pieces = append(pieces, nextPiece)
					if nextBoundary == packet.MbLast {
						break
					}
				}
			}
		}
	}

	if cannotContinue {
		// we need to wait for more packets, store and return
		if isNew {
			if s.recvPktPend == nil {
				s.recvPktPend = dataPacketHeap{p}
				heap.Init(&s.recvPktPend)
			} else {
				heap.Push(&s.recvPktPend, p)
			}
		}
		return false
	}

	// we have a message, pull it from the pending heap (if necessary), assemble it into a message, and return it
	if s.recvPktPend != nil {
		for _, piece := range pieces {
			s.recvPktPend.Remove(piece.Seq)
		}
		if len(s.recvPktPend) == 0 {
			s.recvPktPend = nil
		}
	}

	msg := make([]byte, 0)
	for _, piece := range pieces {
		msg = append(msg, piece.Data...)
	}
	s.messageIn <- msg
	return true
}

func (s *udtSocket) sendNAK(rl receiveLossHeap) {
	lossInfo := make([]uint32, 0)
	firstPkt := rl[0].packetID
	lastPkt := rl[0].packetID
	len := len(rl)
	for idx := 1; idx < len; idx++ {
		thisID := rl[idx].packetID
		if thisID == lastPkt+1 {
			lastPkt = thisID
		} else {
			if firstPkt == lastPkt {
				lossInfo = append(lossInfo, firstPkt&0x7FFFFFFF)
			} else {
				lossInfo = append(lossInfo, firstPkt|0x7FFFFFFF, lastPkt&0x7FFFFFFF)
			}
			firstPkt = thisID
			lastPkt = thisID
		}
	}
	if firstPkt == lastPkt {
		lossInfo = append(lossInfo, firstPkt&0x7FFFFFFF)
	} else {
		lossInfo = append(lossInfo, firstPkt|0x7FFFFFFF, lastPkt&0x7FFFFFFF)
	}

	err := s.sendPacket(&packet.NakPacket{
		CmpLossInfo: lossInfo,
	})
	if err != nil {
		log.Printf("Cannot send NAK: %s", err.Error())
	}
}
