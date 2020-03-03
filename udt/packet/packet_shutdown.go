package packet

// Structure of packets and functions for writing/reading them

type shutdownPacket struct {
	ctrlHeader
}

func (p *shutdownPacket) WriteTo(buf []byte) (uint, error) {
	return p.WriteHdrTo(buf, ptShutdown, 0)
}

func (p *shutdownPacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}
