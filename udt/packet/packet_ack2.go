package packet

// Structure of packets and functions for writing/reading them

type ack2Packet struct {
	ctrlHeader
	ackSeqNo uint32 // ACK sequence number
}

func (p *ack2Packet) WriteTo(buf []byte) (uint, error) {
	return p.WriteHdrTo(buf, ptAck2, p.ackSeqNo)
}

func (p *ack2Packet) readFrom(data []byte) (err error) {
	p.ackSeqNo, err = p.readHdrFrom(data)
	return
}
