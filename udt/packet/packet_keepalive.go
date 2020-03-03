package packet

// Structure of packets and functions for writing/reading them

type keepAlivePacket struct {
	ctrlHeader
}

func (p *keepAlivePacket) WriteTo(buf []byte) (uint, error) {
	return p.WriteHdrTo(buf, ptKeepalive, 0)
}

func (p *keepAlivePacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}
