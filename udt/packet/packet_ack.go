package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

type AckPacket struct {
	ctrlHeader
	AckSeqNo  uint32   // ACK sequence number
	PktSeqHi  PacketID // The packet sequence number to which all the previous packets have been received (excluding)
	Rtt       uint32   // RTT (in microseconds)
	RttVar    uint32   // RTT variance
	BuffAvail uint32   // Available buffer size (in bytes)

	// the following data is optional (not sent more than SYN)
	includeLink bool
	PktRecvRate uint32 // Packets receiving rate (in number of packets per second)
	EstLinkCap  uint32 // Estimated link capacity (in number of packets per second)
}

func (p *AckPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 32 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptAck, p.AckSeqNo); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:19], p.PktSeqHi.Seq)
	endianness.PutUint32(buf[20:23], p.Rtt)
	endianness.PutUint32(buf[24:27], p.RttVar)
	endianness.PutUint32(buf[28:31], p.BuffAvail)
	if p.includeLink {
		if l < 40 {
			return 0, errors.New("packet too small")
		}
		endianness.PutUint32(buf[32:35], p.PktRecvRate)
		endianness.PutUint32(buf[36:39], p.EstLinkCap)
		return 40, nil
	}

	return 32, nil
}

func (p *AckPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 32 {
		return errors.New("packet too small")
	}
	if p.AckSeqNo, err = p.readHdrFrom(data); err != nil {
		return err
	}
	p.PktSeqHi = PacketID{endianness.Uint32(data[16:19])}
	p.Rtt = endianness.Uint32(data[20:23])
	p.RttVar = endianness.Uint32(data[24:27])
	p.BuffAvail = endianness.Uint32(data[28:31])
	if l >= 36 {
		p.includeLink = true
		p.PktRecvRate = endianness.Uint32(data[32:35])
		if l >= 40 {
			p.EstLinkCap = endianness.Uint32(data[36:39])
		}
	}

	return nil
}
