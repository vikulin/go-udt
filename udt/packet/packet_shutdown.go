package packet

// Structure of packets and functions for writing/reading them

type ShutdownPacket struct {
	ctrlHeader
}

func (p *ShutdownPacket) WriteTo(buf []byte) (uint, error) {
	return p.WriteHdrTo(buf, ptShutdown, 0)
}

func (p *ShutdownPacket) readFrom(data []byte) (err error) {
	_, err = p.readHdrFrom(data)
	return
}
