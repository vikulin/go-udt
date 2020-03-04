package packet

import "errors"

type MessageBoundary uint8

const (
	// Message boundary flags
	MbFirst  MessageBoundary = 2
	MbLast                   = 1
	MbOnly                   = 3
	MbMiddle                 = 0
)

type DataPacket struct {
	Seq       uint32 // packet sequence number (top bit = 0)
	msg       uint32 // message sequence number (top three bits = message control)
	ts        uint32 // timestamp when message is sent
	DstSockID uint32 // destination socket
	Data      []byte // payload
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

func (dp *DataPacket) SetMsg(boundary MessageBoundary, order bool, msg uint32) {
	var iOrder uint32 = 0
	if order {
		iOrder = 1
	}
	dp.msg = (uint32(boundary) << 30) | (iOrder << 29) | (msg & 0x1FFFFFFF)
}

func (dp *DataPacket) GetMsgBoundary() MessageBoundary {
	return MessageBoundary(dp.msg >> 30)
}

func (dp *DataPacket) GetMsgOrderFlag() bool {
	return (1 == ((dp.msg >> 29) & 1))
}

func (dp *DataPacket) GetMsg() uint32 {
	return dp.msg & 0x1FFFFFFF
}

func (dp *DataPacket) WriteTo(buf []byte) (uint, error) {
	l := len(buf)
	ol := 16 + len(dp.Data)
	if l < ol {
		return 0, errors.New("packet too small")
	}
	endianness.PutUint32(buf[0:3], dp.Seq&0x7FFFFFFF)
	endianness.PutUint32(buf[4:7], dp.msg)
	endianness.PutUint32(buf[8:11], dp.ts)
	endianness.PutUint32(buf[12:15], dp.DstSockID)
	copy(buf[16:], dp.Data)

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
	dp.Data = make([]byte, l-16)
	copy(dp.Data, data[16:])

	return
}
