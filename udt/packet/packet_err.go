package packet

// Structure of packets and functions for writing/reading them

type ErrPacket struct {
	ctrlHeader
	Errno uint32 // error code
}

func (p *ErrPacket) WriteTo(buf []byte) (uint, error) {
	return p.writeHdrTo(buf, ptSpecialErr, p.Errno)
}

func (p *ErrPacket) readFrom(data []byte) (err error) {
	p.Errno, err = p.readHdrFrom(data)
	return
}
