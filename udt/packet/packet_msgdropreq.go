package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

type MsgDropReqPacket struct {
	ctrlHeader
	msgID    uint32 // Message ID
	firstSeq uint32 // First sequence number in the message
	lastSeq  uint32 // Last sequence number in the message
}

func (p *MsgDropReqPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 24 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.WriteHdrTo(buf, ptMsgDropReq, p.msgID); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:19], p.firstSeq)
	endianness.PutUint32(buf[20:23], p.lastSeq)

	return 24, nil
}

func (p *MsgDropReqPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 24 {
		return errors.New("packet too small")
	}
	if p.msgID, err = p.readHdrFrom(data); err != nil {
		return
	}
	p.firstSeq = endianness.Uint32(data[16:19])
	p.lastSeq = endianness.Uint32(data[20:23])
	return
}
