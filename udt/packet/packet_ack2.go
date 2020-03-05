package packet

// Structure of packets and functions for writing/reading them

type Ack2Packet struct {
	ctrlHeader
	AckSeqNo uint32 // ACK sequence number
}

func (p *Ack2Packet) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptAck2, p.AckSeqNo)
}

func (p *Ack2Packet) readFrom(data []byte) (err error) {
	p.AckSeqNo, err = p.readHdrFrom(data)
	return
}
