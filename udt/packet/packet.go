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

type DataPacket struct {
	seq       uint32
	msg       uint32
	ts        uint32
	DstSockID uint32
	data      []byte
}

func (dp *DataPacket) SetHeader(destSockID uint32, ts uint32) {
	dp.DstSockID = destSockID
	dp.ts = ts
}

func (dp *DataPacket) SocketID() (sockID uint32) {
	return dp.DstSockID
}

func (dp *DataPacket) SendTime() (ts uint32) {
	return dp.ts
}

func (dp *DataPacket) setMsg(boundary uint32, order uint32, msg uint32) {
	dp.msg = (boundary << 30) | (order << 29) | (msg & 0x1FFFFFFF)
}

func (dp *DataPacket) getMsgBoundary() uint32 {
	return dp.msg >> 30
}

func (dp *DataPacket) getMsgOrderFlag() bool {
	return (1 == ((dp.msg >> 29) & 1))
}

func (dp *DataPacket) getMsg() uint32 {
	return dp.msg & 0x1FFFFFFF
}

func (dp *DataPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	ol := 16 + len(dp.data)
	if l < ol {
		return 0, errors.New("packet too small")
	}
	endianness.PutUint32(buf[0:3], dp.seq&0x7FFFFFFF)
	endianness.PutUint32(buf[4:7], dp.msg)
	endianness.PutUint32(buf[8:11], dp.ts)
	endianness.PutUint32(buf[12:15], dp.DstSockID)
	copy(buf[16:], dp.data)

	return uint(ol), nil
}

func (dp *DataPacket) readFrom(data []byte) (err error) {
	l := len(data)
	if l < 16 {
		return errors.New("packet too small")
	}
	//dp.seq = endianness.Uint32(data[0:3])
	dp.msg = endianness.Uint32(data[4:7])
	dp.ts = endianness.Uint32(data[8:11])
	dp.DstSockID = endianness.Uint32(data[12:15])

	// The data is whatever is what comes after the 16 bytes of header
	dp.data = make([]byte, l-16)
	copy(dp.data, data[16:])

	return
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
		seq: h,
	}
	err = p.readFrom(data)
	return
}
