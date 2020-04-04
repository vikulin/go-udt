package packet

// Structure of packets and functions for writing/reading them

import (
	"errors"
	"net"
)

type HandshakeReqType int32

const (
	HsRequest    HandshakeReqType = 1
	HsRendezvous HandshakeReqType = 0
	HsResponse   HandshakeReqType = -1
	HsResponse2  HandshakeReqType = -2
	HsRefused    HandshakeReqType = 1002
)

type HandshakePacket struct {
	ctrlHeader
	UdtVer         uint32           // UDT version
	SockType       socketType       // Socket Type (1 = STREAM or 2 = DGRAM)
	InitPktSeq     PacketID         // initial packet sequence number
	MaxPktSize     uint32           // maximum packet size (including UDP/IP headers)
	MaxFlowWinSize uint32           // maximum flow window size
	ReqType        HandshakeReqType // connection type (regular(1), rendezvous(0), -1/-2 response)
	SockID         uint32           // socket ID
	SynCookie      uint32           // SYN cookie
	SockAddr       net.IP           // the IP address of the UDP socket to which this packet is being sent
}

func (p *HandshakePacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	if l < 64 {
		return 0, errors.New("packet too small")
	}

	if _, err := p.writeHdrTo(buf, ptHandshake, 0); err != nil {
		return 0, err
	}

	endianness.PutUint32(buf[16:20], p.UdtVer)
	endianness.PutUint32(buf[20:24], uint32(p.SockType))
	endianness.PutUint32(buf[24:28], p.InitPktSeq.Seq)
	endianness.PutUint32(buf[28:32], p.MaxPktSize)
	endianness.PutUint32(buf[32:36], p.MaxFlowWinSize)
	endianness.PutUint32(buf[36:40], uint32(p.ReqType))
	endianness.PutUint32(buf[40:44], p.SockID)
	endianness.PutUint32(buf[44:48], p.SynCookie)

	sockAddr := make([]byte, 16)
	copy(sockAddr, p.SockAddr)
	copy(buf[48:64], sockAddr)

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
	p.UdtVer = endianness.Uint32(data[16:20])
	p.SockType = socketType(endianness.Uint32(data[20:24]))
	p.InitPktSeq = PacketID{endianness.Uint32(data[24:28])}
	p.MaxPktSize = endianness.Uint32(data[28:32])
	p.MaxFlowWinSize = endianness.Uint32(data[32:36])
	p.ReqType = HandshakeReqType(endianness.Uint32(data[36:40]))
	p.SockID = endianness.Uint32(data[40:44])
	p.SynCookie = endianness.Uint32(data[44:48])

	p.SockAddr = make(net.IP, 16)
	copy(p.SockAddr, data[48:64])

	return nil
}
