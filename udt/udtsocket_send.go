package udt

import (
	"log"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

func (s *udtSocket) goSendEvent() {
	sendEvent := s.sendEvent
	for {
		select {
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
	// 6) If this is a Light ACK, stop.
	// 7) Update packet arrival rate: A = (A * 7 + a) / 8, where a is the value carried in the ACK.
	// 8) Update estimated link capacity: B = (B * 7 + b) / 8, where b is the value carried in the ACK.
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
