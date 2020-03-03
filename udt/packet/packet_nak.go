package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

type nakPacket struct {
	ctrlHeader
	cmpLossInfo []uint32 // integer array of compressed loss information
}

func (p *nakPacket) WriteTo(buf []byte) (uint, error) {

	off, err := p.WriteHdrTo(buf, ptNak, 0)
	if err != nil {
		return 0, err
	}

	l := uint(len(buf))
	if l < off+uint(4*len(p.cmpLossInfo)) {
		return 0, errors.New("packet too small")
	}

	for _, elm := range p.cmpLossInfo {
		endianness.PutUint32(buf[off:off+3], elm)
		off = off + 4
	}

	return off, nil
}

func (p *nakPacket) readFrom(data []byte) error {
	if _, err := p.readHdrFrom(data); err != nil {
		return err
	}
	l := len(data)
	numEntry := (l - 16) / 4
	p.cmpLossInfo = make([]uint32, numEntry)
	for idx := range p.cmpLossInfo {
		st := 16 + 4*idx
		p.cmpLossInfo[idx] = endianness.Uint32(data[st : st+3])
	}
	return nil
}
