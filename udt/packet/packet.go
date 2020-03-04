package packet

// Structure of packets and functions for writing/reading them

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// Leading bit for distinguishing control from data packets
	flagBit32 = 1 << 31 // 32 bit
	flagBit16 = 1 << 15 // 16 bit
)

type socketType uint16

const (
	// Socket types
	TypeSTREAM socketType = 1
	TypeDGRAM             = 2
)

type packetType uint16

const (
	// Control packet types
	ptHandshake  packetType = 0x0
	ptKeepalive             = 0x1
	ptAck                   = 0x2
	ptNak                   = 0x3
	ptCongestion            = 0x4 // unused
	ptShutdown              = 0x5
	ptAck2                  = 0x6
	ptMsgDropReq            = 0x7
	ptUserDefPkt            = 0x7FFF
)

var (
	endianness = binary.BigEndian
)

type Packet interface {
	// socketId retrieves the socket id of a packet
	SocketID() (sockID uint32)

	// sendTime retrieves the timesamp of the packet
	SendTime() (ts uint32)

	WriteTo(buf []byte) (uint, error)

	// readFrom reads the packet from a Reader
	readFrom(data []byte) (err error)

	SetHeader(destSockID uint32, ts uint32)
}

type ControlPacket interface {
	// socketId retrieves the socket id of a packet
	SocketID() (sockID uint32)

	// sendTime retrieves the timesamp of the packet
	SendTime() (ts uint32)

	WriteTo(buf []byte) (uint, error)

	// readFrom reads the packet from a Reader
	readFrom(data []byte) (err error)

	SetHeader(destSockID uint32, ts uint32)
}

type ctrlHeader struct {
	ts        uint32
	DstSockID uint32
}

func (h *ctrlHeader) SocketID() (sockID uint32) {
	return h.DstSockID
}

func (h *ctrlHeader) SendTime() (ts uint32) {
	return h.ts
}

func (h *ctrlHeader) SetHeader(destSockID uint32, ts uint32) {
	h.DstSockID = destSockID
	h.ts = ts
}

func (h *ctrlHeader) WriteHdrTo(buf []byte, msgType packetType, info uint32) (uint, error) {
	l := len(buf)
	if l < 16 {
		return 0, errors.New("packet too small")
	}

	// Sets the flag bit to indicate this is a control packet
	endianness.PutUint16(buf[0:1], uint16(msgType)|flagBit16)
	endianness.PutUint16(buf[2:3], uint16(0)) // Write 16 bit reserved data

	endianness.PutUint32(buf[4:7], info)
	endianness.PutUint32(buf[8:11], h.ts)
	endianness.PutUint32(buf[12:15], h.DstSockID)

	return 16, nil
}

func (h *ctrlHeader) readHdrFrom(data []byte) (addtlInfo uint32, err error) {
	l := len(data)
	if l < 16 {
		return 0, errors.New("packet too small")
	}
	addtlInfo = endianness.Uint32(data[4:7])
	h.ts = endianness.Uint32(data[8:11])
	h.DstSockID = endianness.Uint32(data[12:15])
	return
}

func ReadPacketFrom(data []byte) (p Packet, err error) {
	h := endianness.Uint32(data[0:3])
	if h&flagBit32 == flagBit32 {
		// this is a control packet
		// Remove flag bit
		h = h &^ flagBit32
		// Message type is leading 16 bits
		msgType := packetType(h >> 16)

		switch msgType {
		case ptHandshake:
			p = &HandshakePacket{}
		case ptKeepalive:
			p = &KeepAlivePacket{}
		case ptAck:
			p = &AckPacket{}
		case ptNak:
			p = &NakPacket{}
		case ptShutdown:
			p = &ShutdownPacket{}
		case ptAck2:
			p = &Ack2Packet{}
		case ptMsgDropReq:
			p = &MsgDropReqPacket{}
		case ptUserDefPkt:
			p = &UserDefControlPacket{msgType: uint16(h & 0xffff)}
		default:
			return nil, fmt.Errorf("Unknown control packet type: %X", msgType)
		}
		err = p.readFrom(data)
		return
	}

	// this is a data packet
	p = &DataPacket{
		Seq: h,
	}
	err = p.readFrom(data)
	return
}
