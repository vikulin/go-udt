package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
	"net"
)

type HandshakePacket struct {
	ctrlHeader
	UdtVer         uint32     // UDT version
	SockType       socketType // Socket Type (1 = STREAM or 2 = DGRAM)
	InitPktSeq     uint32     // initial packet sequence number
	MaxPktSize     uint32     // maximum packet size (including UDP/IP headers)
	MaxFlowWinSize uint32     // maximum flow window size
	ReqType        int32      // connection type (regular(1), rendezvous(0), -1/-2 response)
	SockID         uint32     // socket ID
	SynCookie      uint32     // SYN cookie
	SockAddr       net.IP     // the IP address of the UDP socket to which this packet is being sent
}

func (p *HandshakePacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 64 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.WriteHdrTo(buf, ptHandshake, 0); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:19], p.UdtVer)
	endianness.PutUint32(buf[20:23], uint32(p.SockType))
	endianness.PutUint32(buf[24:27], p.InitPktSeq)
	endianness.PutUint32(buf[28:31], p.MaxPktSize)
	endianness.PutUint32(buf[32:35], p.MaxFlowWinSize)
	endianness.PutUint32(buf[36:39], uint32(p.ReqType))
	endianness.PutUint32(buf[40:43], p.SockID)
	endianness.PutUint32(buf[44:47], p.SynCookie)

	sockAddr := make([]byte, 16)
	copy(sockAddr, p.SockAddr)
	copy(buf[48:63], sockAddr)

	return 64, nil
}

func (p *HandshakePacket) readFrom(data []byte) error {
	l := len(data)
	if l < 64 {
		return errors.New("packet too small")
	}
	if _, err := p.readHdrFrom(data); err != nil {
		return err
	}
	p.UdtVer = endianness.Uint32(data[16:19])
	p.SockType = socketType(endianness.Uint32(data[20:23]))
	p.InitPktSeq = endianness.Uint32(data[24:27])
	p.MaxPktSize = endianness.Uint32(data[28:31])
	p.MaxFlowWinSize = endianness.Uint32(data[32:35])
	p.ReqType = int32(endianness.Uint32(data[36:39]))
	p.SockID = endianness.Uint32(data[40:43])
	p.SynCookie = endianness.Uint32(data[44:47])

	p.SockAddr = make(net.IP, 16)
	copy(p.SockAddr, data[48:63])

	return nil
}
