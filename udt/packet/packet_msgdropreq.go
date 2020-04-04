package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

type MsgDropReqPacket struct {
	ctrlHeader
	MsgID    uint32   // Message ID
	FirstSeq PacketID // First sequence number in the message
	LastSeq  PacketID // Last sequence number in the message
}

func (p *MsgDropReqPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 24 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptMsgDropReq, p.MsgID); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:20], p.FirstSeq.Seq)
	endianness.PutUint32(buf[20:24], p.LastSeq.Seq)

	return 24, nil
}

func (p *MsgDropReqPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 24 {
		return errors.New("packet too small")
	}
	if p.MsgID, err = p.readHdrFrom(data); err != nil {
		return
	}
	p.FirstSeq = PacketID{endianness.Uint32(data[16:20])}
	p.LastSeq = PacketID{endianness.Uint32(data[20:24])}
	return
}
