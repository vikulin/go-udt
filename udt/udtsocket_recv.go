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
	if (p.Seq.Seq-1)&0xf == 0 && s.recvPktHistory != nil {
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
	s.rttVar = (s.rttVar*3 + absdiff(s.rtt, thisRTT)) / 4
	s.rtt = (s.rtt*7 + thisRTT) / 8

	// Update both ACK and NAK period to 4 * RTT + RTTVar + SYN.*/
	s.resetAckNakPeriods()
	//s.rto = 4 * s.rtt + s.rttVar
}

// owned by: goReceiveEvent
// ingestMsgDropReq is called to process an message drop request packet
func (s *udtSocket) ingestMsgDropReq(p *packet.MsgDropReqPacket, now time.Time) {
	stopSeq := p.LastSeq.Add(1)
	for pktID := p.FirstSeq; pktID != stopSeq; pktID.Incr() {
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

	if p.FirstSeq == s.farRecdPktSeq.Add(1) {
		s.farRecdPktSeq = p.LastSeq
	}
	if s.recvLossList != nil && len(s.recvLossList) == 0 {
		s.farRecdPktSeq = s.farNextPktSeq.Add(-1)
		s.recvLossList = nil
	}
	if s.recvPktPend != nil && len(s.recvPktPend) == 0 {
		s.recvPktPend = nil
	}

	// try to push any pending packets out, now that we have dropped any blocking packets
	for s.recvPktPend != nil && stopSeq != s.farNextPktSeq {
		nextPkt, _ := s.recvPktPend.Min(stopSeq, s.farNextPktSeq)
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
	seqDiff := seq.BlindDiff(s.farNextPktSeq)
	if seqDiff > 0 {
		newLoss := make(receiveLossHeap, 0, seqDiff)
		for idx := s.farNextPktSeq; idx != seq; idx.Incr() {
			newLoss = append(newLoss, recvLossEntry{packetID: seq})
		}

		if s.recvLossList == nil {
			s.recvLossList = newLoss
			heap.Init(&s.recvLossList)
		} else {
			for idx := s.farNextPktSeq; idx != seq; idx.Incr() {
				heap.Push(&s.recvLossList, recvLossEntry{packetID: seq})
			}
			heap.Init(&newLoss)
		}

		s.sendNAK(newLoss)
		s.farNextPktSeq = seq.Add(1)

	} else if seqDiff < 0 {
		// If the sequence number is less than LRSN, remove it from the receiver's loss list.
		if !s.recvLossList.Remove(seq) {
			return // already previously received packet -- ignore
		}

		if len(s.recvLossList) == 0 {
			s.farRecdPktSeq = s.farNextPktSeq.Add(-1)
			s.recvLossList = nil
		} else {
			s.farRecdPktSeq, _ = s.recvLossList.Min(s.farRecdPktSeq, s.farNextPktSeq)
		}
	}

	s.attemptProcessPacket(p, true)
}

func (s *udtSocket) attemptProcessPacket(p *packet.DataPacket, isNew bool) bool {
	seq := p.Seq

	// can we process this packet?
	boundary, mustOrder, msgID := p.GetMessageData()
	if s.recvLossList != nil && mustOrder && s.farRecdPktSeq.Add(1) != seq {
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

	// can we find the start of this message?
	pieces := make([]*packet.DataPacket, 0)
	cannotContinue := false
	switch boundary {
	case packet.MbLast, packet.MbMiddle:
		// we need prior packets, let's make sure we have them
		if s.recvPktPend != nil {
			pieceSeq := seq.Add(-1)
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
				pieceSeq.Decr()
			}
		}
	}
	if !cannotContinue {
		pieces = append(pieces, p)

		switch boundary {
		case packet.MbFirst, packet.MbMiddle:
			// we need following packets, let's make sure we have them
			if s.recvPktPend != nil {
				pieceSeq := seq.Add(1)
				for {
					nextPiece, _ := s.recvPktPend.Find(pieceSeq)
					if nextPiece == nil {
						// we don't have the previous piece, is it missing?
						if pieceSeq == s.farNextPktSeq {
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

func (s *udtSocket) sendACK(lightAck bool) {
	/*
	   {
	   int32_t ack;

	   // If there is no loss, the ACK is the current largest sequence number plus 1;
	   // Otherwise it is the smallest sequence number in the receiver loss list.
	   if (0 == m_pRcvLossList->getLossLength())
	      ack = CSeqNo::incseq(m_iRcvCurrSeqNo);
	   else
	      ack = m_pRcvLossList->getFirstLostSeq();

	   if (ack == m_iRcvLastAckAck)
	      break;

	   // send out a lite ACK
	   // to save time on buffer processing and bandwidth/AS measurement, a lite ACK only feeds back an ACK number
	   if (4 == size)
	   {
	      ctrlpkt.pack(pkttype, NULL, &ack, size);
	      ctrlpkt.m_iID = m_PeerID;
	      m_pSndQueue->sendto(m_pPeerAddr, ctrlpkt);

	      break;
	   }

	   uint64_t currtime;
	   CTimer::rdtsc(currtime);

	   // There are new received packets to acknowledge, update related information.
	   if (CSeqNo::seqcmp(ack, m_iRcvLastAck) > 0)
	   {
	      int acksize = CSeqNo::seqoff(m_iRcvLastAck, ack);

	      m_iRcvLastAck = ack;

	      m_pRcvBuffer->ackData(acksize);

	      // signal a waiting "recv" call if there is any data available
	      #ifndef WIN32
	         pthread_mutex_lock(&m_RecvDataLock);
	         if (m_bSynRecving)
	            pthread_cond_signal(&m_RecvDataCond);
	         pthread_mutex_unlock(&m_RecvDataLock);
	      #else
	         if (m_bSynRecving)
	            SetEvent(m_RecvDataCond);
	      #endif

	      // acknowledge any waiting epolls to read
	      s_UDTUnited.m_EPoll.update_events(m_SocketID, m_sPollID, UDT_EPOLL_IN, true);
	   }
	   else if (ack == m_iRcvLastAck)
	   {
	      if ((currtime - m_ullLastAckTime) < ((m_iRTT + 4 * m_iRTTVar) * m_ullCPUFrequency))
	         break;
	   }
	   else
	      break;

	   // Send out the ACK only if has not been received by the sender before
	   if (CSeqNo::seqcmp(m_iRcvLastAck, m_iRcvLastAckAck) > 0)
	   {
	      int32_t data[6];

	      m_iAckSeqNo = CAckNo::incack(m_iAckSeqNo);
	      data[0] = m_iRcvLastAck;
	      data[1] = m_iRTT;
	      data[2] = m_iRTTVar;
	      data[3] = m_pRcvBuffer->getAvailBufSize();
	      // a minimum flow window of 2 is used, even if buffer is full, to break potential deadlock
	      if (data[3] < 2)
	         data[3] = 2;

	      if (currtime - m_ullLastAckTime > m_ullSYNInt)
	      {
	         data[4] = m_pRcvTimeWindow->getPktRcvSpeed();
	         data[5] = m_pRcvTimeWindow->getBandwidth();
	         ctrlpkt.pack(pkttype, &m_iAckSeqNo, data, 24);

	         CTimer::rdtsc(m_ullLastAckTime);
	      }
	      else
	      {
	         ctrlpkt.pack(pkttype, &m_iAckSeqNo, data, 16);
	      }

	      ctrlpkt.m_iID = m_PeerID;
	      m_pSndQueue->sendto(m_pPeerAddr, ctrlpkt);

	      m_pACKWindow->store(m_iAckSeqNo, m_iRcvLastAck);

	      ++ m_iSentACK;
	      ++ m_iSentACKTotal;
	   }

	   break;
	   }
	*/
}

func (s *udtSocket) sendNAK(rl receiveLossHeap) {
	lossInfo := make([]packet.PacketID, 0)
	var firstPkt packet.PacketID
	var lastPkt packet.PacketID

	isFirst := true
	rl.Sorted(func(e recvLossEntry) bool {
		if isFirst {
			firstPkt = e.packetID
			lastPkt = e.packetID
			isFirst = false
			return true
		}

		thisID := e.packetID
		if thisID == lastPkt+1 {
			lastPkt = thisID
		} else {
			if firstPkt == lastPkt {
				lossInfo = append(lossInfo, firstPkt&0x7FFFFFFF)
			} else {
				lossInfo = append(lossInfo, firstPkt|0x80000000, lastPkt&0x7FFFFFFF)
			}
			firstPkt = thisID
			lastPkt = thisID
		}
		return true
	})
	if firstPkt == lastPkt {
		lossInfo = append(lossInfo, firstPkt&0x7FFFFFFF)
	} else {
		lossInfo = append(lossInfo, firstPkt|0x80000000, lastPkt&0x7FFFFFFF)
	}

	err := s.sendPacket(&packet.NakPacket{
		CmpLossInfo: lossInfo,
	})
	if err != nil {
		log.Printf("Cannot send NAK: %s", err.Error())
	}
	/*
			      // update next NAK time, which should wait enough time for the retansmission, but not too long
		      m_ullNAKInt = (m_iRTT + 4 * m_iRTTVar) * m_ullCPUFrequency;
		      int rcv_speed = m_pRcvTimeWindow->getPktRcvSpeed();
		      if (rcv_speed > 0)
		         m_ullNAKInt += (m_pRcvLossList->getLossLength() * 1000000ULL / rcv_speed) * m_ullCPUFrequency;
		      if (m_ullNAKInt < m_ullMinNakInt)
		         m_ullNAKInt = m_ullMinNakInt;
	*/
}
