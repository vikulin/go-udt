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

// SocketType describes the kind of socket this is (i.e. streaming vs message)
type SocketType uint16

const (
	// TypeSTREAM describes a reliable streaming protocol (e.g. TCP)
	TypeSTREAM SocketType = 1
	// TypeDGRAM describes a partially-reliable messaging protocol
	TypeDGRAM SocketType = 2
)

type packetType uint16

const (
	// Control packet types
	ptHandshake  packetType = 0x0
	ptKeepalive  packetType = 0x1
	ptAck        packetType = 0x2
	ptNak        packetType = 0x3
	ptCongestion packetType = 0x4 // unused in ver4
	ptShutdown   packetType = 0x5
	ptAck2       packetType = 0x6
	ptMsgDropReq packetType = 0x7
	ptSpecialErr packetType = 0x8 // undocumented but reference implementation seems to use it
	ptUserDefPkt packetType = 0x7FFF
)

var (
	endianness = binary.BigEndian
)

// Packet represents a UDT packet
type Packet interface {
	// socketId retrieves the socket id of a packet
	SocketID() (sockID uint32)

	// SendTime retrieves the timesamp of the packet
	SendTime() (ts uint32)

	// WriteTo writes this packet to the provided buffer, returning the length of the packet
	WriteTo(buf []byte) (uint, error)

	// readFrom reads the packet from a Reader
	readFrom(data []byte) (err error)

	SetHeader(destSockID uint32, ts uint32)
}

// ControlPacket represents a UDT control packet.
type ControlPacket interface {
	// socketId retrieves the socket id of a packet
	SocketID() (sockID uint32)

	// SendTime retrieves the timesamp of the packet
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

func (h *ctrlHeader) writeHdrTo(buf []byte, msgType packetType, info uint32) (uint, error) {
	l := len(buf)
	if l < 16 {
		return 0, errors.New("packet too small")
	}

	// Sets the flag bit to indicate this is a control packet
	endianness.PutUint16(buf[0:2], uint16(msgType)|flagBit16)
	endianness.PutUint16(buf[2:4], uint16(0)) // Write 16 bit reserved data

	endianness.PutUint32(buf[4:8], info)
	endianness.PutUint32(buf[8:12], h.ts)
	endianness.PutUint32(buf[12:16], h.DstSockID)

	return 16, nil
}

func (h *ctrlHeader) readHdrFrom(data []byte) (addtlInfo uint32, err error) {
	l := len(data)
	if l < 16 {
		return 0, errors.New("packet too small")
	}
	addtlInfo = endianness.Uint32(data[4:8])
	h.ts = endianness.Uint32(data[8:12])
	h.DstSockID = endianness.Uint32(data[12:16])
	return
}

// ReadPacketFrom takes the contents of a UDP packet and decodes it into a UDT packet
func ReadPacketFrom(data []byte) (p Packet, err error) {
	h := endianness.Uint32(data[0:4])
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
			if len(data) == 20 {
				p = &LightAckPacket{}
			} else {
				p = &AckPacket{}
			}
		case ptNak:
			p = &NakPacket{}
		case ptCongestion:
			p = &CongestionPacket{}
		case ptShutdown:
			p = &ShutdownPacket{}
		case ptAck2:
			p = &Ack2Packet{}
		case ptMsgDropReq:
			p = &MsgDropReqPacket{}
		case ptSpecialErr:
			p = &ErrPacket{}
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
		Seq: PacketID{h},
	}
	err = p.readFrom(data)
	return
}
