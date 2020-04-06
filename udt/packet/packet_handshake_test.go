package packet

import (
	"net"
	"testing"
)

func TestHandshakePacket(t *testing.T) {
	pkt1 := &HandshakePacket{
		UdtVer:         4,
		SockType:       TypeDGRAM,
		InitPktSeq:     PacketID{Seq: 50},
		MaxPktSize:     1000,
		MaxFlowWinSize: 500,
		ReqType:        1,
		SockID:         59,
		SynCookie:      978,
		SockAddr:       net.ParseIP("127.0.0.1"),
	}
	pkt1.SetHeader(59, 100)
	read := testPacket(pkt1, t)

	t.Log((read.(*HandshakePacket)).SockAddr)
}
