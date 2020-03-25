package udt

import (
	"container/heap"
	"fmt"
	"log"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

type sendState int

const (
	sendStateIdle        sendState = iota // not waiting for anything, can send immediately
	sendStateSending                      // recently sent something, waiting for SND before sending more
	sendStateWaiting                      // destination is full, waiting for them to process something and come back
	sendStateProcessDrop                  // immediately re-process any drop list requests
)

type udtSocketSend struct {
	// channels
	closed     <-chan struct{}     // closed when socket is closed
	sendEvent  <-chan recvPktEvent // sender: ingest the specified packet. Sender is readPacket, receiver is goSendEvent
	messageOut <-chan []byte       // outbound messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	socket     *udtSocket

	sendState      sendState        // current sender state
	sendPktPend    dataPacketHeap   // list of packets that have been sent but not yet acknoledged
	sendPktSeq     packet.PacketID  // the current packet sequence number
	msgPartialSend []byte           // when a message can only partially fit in a socket, this is the remainder
	msgSeq         uint32           // the current message sequence number
	farFlowWinSize uint             // the estimated peer available window size
	expCount       uint             // number of continuous EXP timeouts.
	expResetCount  time.Time        // the last time expCount was set to 1
	recvAckSeq     packet.PacketID  // largest packetID we've received an ACK from
	sentAck2       uint32           // largest ACK2 packet we've sent
	sendLossList   packetIDHeap     // loss list
	sndEvent       <-chan time.Time // if a packet is recently sent, this timer fires when SND completes
	ack2SentEvent  <-chan time.Time // if an ACK2 packet has recently sent, wait SYN before sending another one
	sndPeriod      time.Duration    // delay between sending packets
}

func newUdtSocketSend(s *udtSocket, closed <-chan struct{}, sendEvent <-chan recvPktEvent, messageOut <-chan []byte) *udtSocketSend {
	ss := &udtSocketSend{
		socket:        s,
		expResetCount: s.created,
		sendPktSeq:    packet.PacketID{randUint32()},
		closed:        closed,
		sendEvent:     sendEvent,
		messageOut:    messageOut,
	}
	go ss.goSendEvent()
	return ss
}

func (s *udtSocketSend) configureHandshake(p *packet.HandshakePacket) {
	s.recvAckSeq = p.InitPktSeq
	s.sendPktSeq = p.InitPktSeq
	s.farFlowWinSize = uint(p.MaxFlowWinSize)
}

func (s *udtSocketSend) goSendEvent() {
	sendEvent := s.sendEvent
	messageOut := s.messageOut
	closed := s.closed
	for {
		thisMsgChan := messageOut
		switch s.sendState {
		case sendStateIdle: // not waiting for anything, can send immediately
			if s.msgPartialSend != nil { // we have a partial message waiting, try to send more of it now
				s.processDataMsg(false, messageOut)
				continue
			}
		case sendStateProcessDrop: // immediately re-process any drop list requests
			s.sendState = s.reevalSendState() // try to reconstruct what our state should be if it wasn't sendStateProcessDrop
			if !s.processSendLoss() || s.pktSeq%16 == 0 {
				s.processSendExpire()
			}
			continue
		default:
			thisMsgChan = nil
		}

		select {
		case _, ok := <-closed:
			return
		case msg, ok := <-thisMsgChan: // nil if we can't process outgoing messages right now
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
			case *packet.LightAckPacket:
				s.ingestLightAck(sp, evt.now)
			case *packet.NakPacket:
				s.ingestNak(sp, evt.now)
			case *packet.CongestionPacket:
				s.ingestCongestion(sp, evt.now)
			}
			s.sendState = s.reevalSendState()
		case _ = <-s.sndEvent: // SND event
			s.sndEvent = nil
			if s.sendState == sendStateSending {
				s.sendState = s.reevalSendState()
				if !s.processSendLoss() || s.sendPktSeq.Seq%16 == 0 {
					s.processSendExpire()
				}
			}
		case _ = <-s.ack2SentEvent: // ACK2 unlocked
			s.ack2SentEvent = nil
		}
	}
}

func (s *udtSocketSend) reevalSendState() sendState {
	if s.sndEvent != nil {
		return sendStateSending
	}
	if s.farFlowWinSize == 0 {
		return sendStateWaiting
	}
	return sendStateIdle
}

// owned by: goSendEvent
// try to pack a new data packet and send it
func (s *udtSocketSend) processDataMsg(isFirst bool, inChan <-chan []byte) {
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
func (s *udtSocketSend) processSendLoss() bool {
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
func (s *udtSocketSend) sendDataPacket(dp *packet.DataPacket, isResend bool) {
	if s.sendPktPend == nil {
		s.sendPktPend = dataPacketHeap{dp}
		heap.Init(&s.sendPktPend)
	} else {
		heap.Push(&s.sendPktPend, dp)
	}

	s.socket.cong.onDataPktSent(dp.Seq)
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
// ingestLightAck is called to process a "light" ACK packet
func (s *udtSocketSend) ingestLightAck(p *packet.LightAckPacket, now time.Time) {
	// Update the largest acknowledged sequence number.

	pktSeqHi := p.PktSeqHi
	diff := p.recvAckSeq.BlindDiff(pktSeqHi)
	if diff > 0 {
		p.iFlowWindowSize += diff
		p.recvAckSeq = pktSeqHi
	}
}

// owned by: goSendEvent
// ingestAck is called to process an ACK packet
func (s *udtSocketSend) ingestAck(p *packet.AckPacket, now time.Time) {
	// Update the largest acknowledged sequence number.

	// Send back an ACK2 with the same ACK sequence number in this ACK.
	if s.ack2SentEvent == nil && p.AckSeqNo == s.sentAck2 {
		s.sentAck2 = p.AckSeqNo
		err := s.sendPacket(&packet.Ack2Packet{AckSeqNo: p.AckSeqNo})
		if err != nil {
			log.Printf("Cannot send ACK2: %s", err.Error())
		} else {
			s.ack2SentEvent = time.After(synTime)
		}
	}

	pktSeqHi := p.PktSeqHi
	if !s.assertValidSentPktID("ACK", pktSeqHi) {
		//		s.senderFault(fmt.Errorf("FAULT: Received an ACK for packet %d, but the largest packet we've sent has been %d", p.sendPktSeq, pktSeqHi))
		return
	}
	diff := p.recvAckSeq.BlindDiff(pktSeqHi)
	if diff <= 0 {
		return
	}

	p.iFlowWindowSize = p.BuffAvail
	p.recvAckSeq = pktSeqHi

	// Update RTT and RTTVar.
	s.rttVar = (s.rttVar*3 + absdiff(p.Rtt, s.rtt)) >> 2
	s.rtt = (s.rtt*7 + p.Rtt) >> 3
	m_pCC.setRTT(m_iRTT)

	// Update both ACK and NAK period to 4 * RTT + RTTVar + SYN.
	s.resetAckNakPeriods()

	// Update flow window size.
	if p.includeLink {

		// Update Estimated Bandwidth and packet delivery rate
		if p.PktRecvRate > 0 {
			m_iDeliveryRate = (m_iDeliveryRate*7 + p.PktRecvRate) >> 3
		}

		if p.EstLinkCap > 0 {
			m_iBandwidth = (m_iBandwidth*7 + p.EstLinkCap) >> 3
		}

		m_pCC.setRcvRate(m_iDeliveryRate)
		m_pCC.setBandwidth(m_iBandwidth)
	}

	s.socket.cong.onACK(pktSeqHi)

	// Update packet arrival rate: A = (A * 7 + a) / 8, where a is the value carried in the ACK.
	// Update estimated link capacity: B = (B * 7 + b) / 8, where b is the value carried in the ACK.

	// Update sender's buffer (by releasing the buffer that has been acknowledged).
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
func (s *udtSocketSend) ingestNak(p *packet.NakPacket, now time.Time) {
	newLossList := make([]packet.PacketID, 0)
	clen := len(p.CmpLossInfo)
	for idx := 0; idx < clen; idx++ {
		thisEntry := p.CmpLossInfo[idx]
		if thisEntry&0x80000000 != 0 {
			thisEntry = thisEntry & 0x7FFFFFFF
			if idx+1 == clen {
				s.senderFault(fmt.Errorf("FAULT: While unpacking a NAK, the last entry (%x) was describing a start-of-range", thisEntry))
				return
			}
			if !s.assertValidSentPktID("NAK", thisEntry) {
				return
			}
			lastEntry := p.CmpLossInfo[idx+1]
			if lastEntry&0x80000000 != 0 {
				s.senderFault(fmt.Errorf("FAULT: While unpacking a NAK, a start-of-range (%x) was followed by another start-of-range (%x)", thisEntry, lastEntry))
				return
			}
			if !s.assertValidSentPktID("NAK", lastEntry) {
				return
			}
			idx++
			for span := thisEntry; span <= lastEntry; span++ {
				newLossList = append(newLossList, span)
			}
		} else {
			if !s.assertValidSentPktID("NAK", thisEntry) {
				return
			}
			newLossList = append(newLossList, thisEntry)
		}
	}

	s.socket.cong.onNAK(newLossList)

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

// owned by: goSendEvent
// ingestCongestion is called to process a (retired?) Congestion packet
func (s *udtSocketSend) ingestCongestion(p *packet.NakPacket, now time.Time) {
	// One way packet delay is increasing, so decrease the sending rate
	s.sndPeriod = s.sndPeriod * 1125 / 1000
	m_iLastDecSeq = s.sendPktSeq
}
