package udt

import (
	"container/heap"
	"log"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

func (s *udtSocket) goSendEvent() {
	sendEvent := s.sendEvent
	messageOut := s.messageOut
	closed := s.closed
	for {
		switch s.sendState {
		case sendStateIdle: // not waiting for anything, can send immediately
			if s.msgPartialSend != nil { // we have a partial message waiting, try to send more of it now
				s.processDataMsg(false, messageOut)
				continue
			}

			select {
			case _, ok := <-closed:
				return
			case msg, ok := <-messageOut:
				if !ok {
					return
				}
				s.msgPartialSend = msg
				s.processDataMsg(true, messageOut)
			case evt, ok := <-sendEvent:
				if !ok {
					return
				}
				s.expCount = 1
				s.expResetCount = evt.now
				switch sp := evt.pkt.(type) {
				case *packet.AckPacket:
					s.ingestAck(sp, evt.now)
				case *packet.NakPacket:
					s.ingestNak(sp, evt.now)
				}
			}
		case sendStateSending: // recently sent something, waiting for SND before sending more
			select {
			case _, ok := <-closed:
				return
			case evt, ok := <-sendEvent:
				if !ok {
					return
				}
				s.expCount = 1
				s.expResetCount = evt.now
				switch sp := evt.pkt.(type) {
				case *packet.AckPacket:
					s.ingestAck(sp, evt.now)
				case *packet.NakPacket:
					s.ingestNak(sp, evt.now)
				}
			case _ = <-s.sndEvent: // SND event
				s.sndEvent = nil
				if s.farFlowWinSize == 0 {
					s.sendState = sendStateWaiting
				} else {
					s.sendState = sendStateIdle
				}
				if !s.processSendLoss() || s.pktSeq%16 == 0 {
					s.processSendExpire()
				}
			}
		case sendStateProcessDrop: // immediately re-process any drop list requests
			if s.sndEvent != nil {
				s.sendState = sendStateSending
			} else if s.farFlowWinSize == 0 {
				s.sendState = sendStateWaiting
			} else {
				s.sendState = sendStateIdle
			}
			if !s.processSendLoss() || s.pktSeq%16 == 0 {
				s.processSendExpire()
			}
		case sendStateWaiting: // destination is full, waiting for them to process something and come back
			select {
			case _, ok := <-closed:
				return
			case evt, ok := <-sendEvent:
				if !ok {
					return
				}
				s.expCount = 1
				s.expResetCount = evt.now
				switch sp := evt.pkt.(type) {
				case *packet.AckPacket:
					s.ingestAck(sp, evt.now)
				case *packet.NakPacket:
					s.ingestNak(sp, evt.now)
				}
			}
		}
	}
}

// owned by: goSendEvent
// try to pack a new data packet and send it
func (s *udtSocket) processDataMsg(isFirst bool, inChan <-chan []byte) {
	for s.msgPartialSend != nil {
		state := packet.MbOnly
		if s.isDatagram {
			if isFirst {
				state = packet.MbFirst
			} else {
				state = packet.MbMiddle
			}
		}
		if isFirst || !s.isDatagram {
			s.msgSeq++
		}

		mtu := s.mtu
		msgLen := len(s.msgPartialSend)
		if msgLen >= mtu {
			// we are full -- send what we can and leave the rest
			var dp *packet.DataPacket
			if msgLen == mtu {
				dp = &packet.DataPacket{
					Seq:  s.pktSeq,
					Data: s.msgPartialSend,
				}
				s.msgPartialSend = nil
			} else {
				dp = &packet.DataPacket{
					Seq:  s.pktSeq,
					Data: s.msgPartialSend[0 : mtu-1],
				}
				s.msgPartialSend = s.msgPartialSend[mtu:]
			}
			s.pktSeq++
			dp.SetMessageData(state, !s.isDatagram, s.msgSeq)
			s.sendDataPacket(dp, false)
			return
		}

		// we are not full -- send only if this is a datagram or there's nothing obvious left
		if s.isDatagram {
			if isFirst {
				state = packet.MbOnly
			} else {
				state = packet.MbLast
			}
		} else {
			select {
			case morePartialSend := <-inChan:
				// we have more data, concat and try again
				s.msgPartialSend = append(s.msgPartialSend, morePartialSend...)
				continue
			default:
				// nothing immediately available, just send what we have
			}
		}

		dp := &packet.DataPacket{
			Seq:  s.pktSeq,
			Data: s.msgPartialSend,
		}
		s.msgPartialSend = nil
		s.pktSeq++
		dp.SetMessageData(state, !s.isDatagram, s.msgSeq)
		s.sendDataPacket(dp, false)
		return
	}
}

// owned by: goSendEvent
// If the sender's loss list is not empty, retransmit the first packet in the list and remove it from the list.
func (s *udtSocket) processSendLoss() bool {
	if s.sendLossList == nil || s.sendPktPend == nil {
		return false
	}

	minLoss, minLossIdx := s.sendLossList.Min()
	if minLossIdx < 0 {
		// empty loss list? shouldn't really happen as we don't keep empty lists, but check for it anyhow
		return false
	}

	heap.Remove(&s.sendLossList, minLossIdx)
	if len(s.sendLossList) == 0 {
		s.sendLossList = nil
	}

	dp, dpIdx := s.sendPktPend.Find(minLoss)
	if dp == nil {
		// can't find record of this packet, not much we can do really
		return false
	}

	s.sendDataPacket(dp, true)
	return true
}

// owned by: goSendEvent
// we have a packed packet and a green light to send, so lets send this and mark it
func (s *udtSocket) sendDataPacket(dp *packet.DataPacket, isResend bool) {
	if s.sendPktPend == nil {
		s.sendPktPend = dataPacketHeap{dp}
		heap.Init(&s.sendPktPend)
	} else {
		heap.Push(&s.sendPktPend, dp)
	}

	err := s.sendPacket(dp)
	if err != nil {
		log.Printf("Error sending data packet: %s", err.Error())
	}

	// have we exceeded our recipient's window size?
	if s.farFlowWinSize > 0 {
		s.farFlowWinSize--
	}
	if s.farFlowWinSize == 0 {
		s.sendState = sendStateWaiting
		return
	}

	if !isResend && dp.Seq%16 == 0 {
		s.processSendExpire()
		return
	}

	if s.sndPeriod > 0 {
		s.sndEvent = time.After(s.sndPeriod)
		s.sendState = sendStateSending
	}
}

// owned by: goSendEvent
// ingestAck is called to process an ACK packet
func (s *udtSocket) ingestAck(p *packet.AckPacket, now time.Time) {
	// Update the largest acknowledged sequence number.

	// Send back an ACK2 with the same ACK sequence number in this ACK.
	err := s.sendPacket(&packet.Ack2Packet{
		AckSeqNo: p.AckSeqNo,
	})
	if err != nil {
		log.Printf("Cannot send ACK2: %s", err.Error())
	}

	// Update RTT and RTTVar.

	// Update both ACK and NAK period to 4 * RTT + RTTVar + SYN.
	s.resetAckNakPeriods()

	// Update flow window size.

	if p.Rtt != 0 || p.RttVar != 0 || p.BuffAvail != 0 || p.PktRecvRate != 0 || p.EstLinkCap != 0 {
		// Update packet arrival rate: A = (A * 7 + a) / 8, where a is the value carried in the ACK.
		// Update estimated link capacity: B = (B * 7 + b) / 8, where b is the value carried in the ACK.
	}

	// Update sender's buffer (by releasing the buffer that has been acknowledged).
	pktSeqHi := p.PktSeqHi
	if s.sendPktPend != nil {
		for {
			minLoss, minLossIdx := s.sendPktPend.Min()
			if minLoss.Seq >= pktSeqHi || minLossIdx < 0 {
				break
			}
			heap.Remove(&s.sendPktPend, minLossIdx)
		}
		if len(s.sendPktPend) == 0 {
			s.sendPktPend = nil
		}
	}

	// Update sender's loss list (by removing all those that has been acknowledged).
	if s.sendLossList != nil {
		for {
			minLoss, minLossIdx := s.sendLossList.Min()
			if minLoss >= pktSeqHi || minLossIdx < 0 {
				break
			}
			heap.Remove(&s.sendLossList, minLossIdx)
		}
		if len(s.sendLossList) == 0 {
			s.sendLossList = nil
		}
	}
}

// owned by: goSendEvent
// ingestNak is called to process an NAK packet
func (s *udtSocket) ingestNak(p *packet.NakPacket, now time.Time) {
	newLossList := make([]uint32, 0)
	clen := len(p.CmpLossInfo)
	for idx := 0; idx < clen; idx++ {
		thisEntry := p.CmpLossInfo[idx]
		if thisEntry&0x80000000 != 0 {
			if idx+1 == clen {
				// not a legal entry, see what we can do about it I guess
				newLossList = append(newLossList, thisEntry&0x7FFFFFFF)
				continue
			}
			lastEntry := p.CmpLossInfo[idx+1]
			if lastEntry&0x80000000 != 0 {
				// not a legal entry, see what we can do about it I guess
				newLossList = append(newLossList, thisEntry&0x7FFFFFFF)
				continue
			}
			idx++
			for span := thisEntry & 0x7FFFFFFF; span <= lastEntry; span++ {
				newLossList = append(newLossList, span)
			}
		} else {
			newLossList = append(newLossList, thisEntry)
		}
	}

	if s.sendLossList == nil {
		s.sendLossList = newLossList
		heap.Init(&s.sendLossList)
	} else {
		llen := len(newLossList)
		for idx := 0; idx < llen; idx++ {
			heap.Push(&s.sendLossList, newLossList[idx])
		}
	}

	//	2) Update the SND period by rate control (see section 3.6).

	//	3) Reset the EXP time variable.
}
