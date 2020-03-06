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
				switch sp := evt.pkt.(type) {
				case *packet.AckPacket:
					s.ingestAck(sp, evt.now)
				case *packet.NakPacket:
					s.ingestNak(sp, evt.now)
				}
			case _ = <-s.sndTimer: // SND event
				s.sndTimer = nil
				s.sendState = sendStateIdle
				if !s.processSendLoss() {
					s.processSendExpire()
				}
			}
		case sendStateWaiting: // destination is full, waiting for them to process something and come back
			select {
			case _, ok := <-closed:
				return
			case evt, ok := <-sendEvent:
				if !ok {
					return
				}
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
			s.sendDataPacket(dp)
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
		s.sendDataPacket(dp)
		return
	}
}

// owned by: goSendEvent
// we have a packed packet and a green light to send, so lets send this and mark it
func (s *udtSocket) sendDataPacket(dp *packet.DataPacket) {
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

	if dp.Seq%16 == 0 {
		s.processSendExpire()
		return
	}

	if s.sndPeriod > 0 {
		s.sndTimer = time.After(s.sndPeriod)
		s.sendState = sendStateSending
	}
}

// owned by: goSendEvent
// ingestAck is called to process an ACK packet
func (s *udtSocket) ingestAck(p *packet.AckPacket, now time.Time) {
	// 1) Update the largest acknowledged sequence number.

	// 2) Send back an ACK2 with the same ACK sequence number in this ACK.
	err := s.sendPacket(&packet.Ack2Packet{
		AckSeqNo: p.AckSeqNo,
	})
	if err != nil {
		log.Printf("Cannot send ACK2: %s", err.Error())
	}

	// 3) Update RTT and RTTVar.

	// 4) Update both ACK and NAK period to 4 * RTT + RTTVar + SYN.
	s.resetAckNakPeriods()

	// 5) Update flow window size.

	if p.Rtt != 0 || p.RttVar != 0 || p.BuffAvail != 0 || p.PktRecvRate != 0 || p.EstLinkCap != 0 {
		// 7) Update packet arrival rate: A = (A * 7 + a) / 8, where a is the value carried in the ACK.
		// 8) Update estimated link capacity: B = (B * 7 + b) / 8, where b is the value carried in the ACK.
	}

	// 9) Update sender's buffer (by releasing the buffer that has been acknowledged).
	// 10) Update sender's loss list (by removing all those that has been acknowledged).
}

// owned by: goSendEvent
// ingestNak is called to process an NAK packet
func (s *udtSocket) ingestNak(p *packet.NakPacket, now time.Time) {
	/*
	   1) Add all sequence numbers carried in the NAK into the sender's loss list.
	   2) Update the SND period by rate control (see section 3.6).
	   3) Reset the EXP time variable.
	*/
}
