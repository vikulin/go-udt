package packet

// Structure of packets and functions for writing/reading them

type KeepAlivePacket struct {
	ctrlHeader
}

func (p *KeepAlivePacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptKeepalive, 0)
}

func (p *KeepAlivePacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}
