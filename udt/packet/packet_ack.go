package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
)

type AckPacket struct {
	ctrlHeader
	ackSeqNo uint32 // ACK sequence number
	pktSeqHi uint32 // The packet sequence number to which all the previous packets have been received (excluding)

	// The below are optional
	rtt         uint32 // RTT (in microseconds)
	rttVar      uint32 // RTT variance
	buffAvail   uint32 // Available buffer size (in bytes)
	pktRecvRate uint32 // Packets receiving rate (in number of packets per second)
	estLinkCap  uint32 // Estimated link capacity (in number of packets per second)
}

func (p *AckPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 20 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptAck, p.ackSeqNo); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:19], p.pktSeqHi)

	if p.rtt == 0 && p.rttVar == 0 && p.buffAvail == 0 && p.pktRecvRate == 0 && p.estLinkCap == 0 {
		return 20, nil
	}

	if l < 40 {
		return 0, errors.New("packet too small")
	}

	endianness.PutUint32(buf[20:23], p.rtt)
	endianness.PutUint32(buf[24:27], p.rttVar)
	endianness.PutUint32(buf[28:31], p.buffAvail)
	endianness.PutUint32(buf[32:35], p.pktRecvRate)
	endianness.PutUint32(buf[36:39], p.estLinkCap)

	return 40, nil
}

func (p *AckPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 20 {
		return errors.New("packet too small")
	}
	if p.ackSeqNo, err = p.readHdrFrom(data); err != nil {
		return err
	}
	p.pktSeqHi = endianness.Uint32(data[16:19])
	if l >= 24 {
		p.rtt = endianness.Uint32(data[20:23])
		if l >= 28 {
			p.rttVar = endianness.Uint32(data[24:27])
			if l >= 32 {
				p.buffAvail = endianness.Uint32(data[28:31])
				if l >= 36 {
					p.pktRecvRate = endianness.Uint32(data[32:35])
					if l >= 40 {
						p.estLinkCap = endianness.Uint32(data[36:39])
					}
				}
			}
		}
	}

	return nil
}
