package packet

// Structure of packets and functions for writing/reading them

type CongestionPacket struct {
	ctrlHeader
}

func (p *CongestionPacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptCongestion, 0)
}

func (p *CongestionPacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}
