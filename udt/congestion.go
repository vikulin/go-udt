package udt

import (
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

type CongestionControlParms interface {
	GetSndCurrSeqNo() packet.PacketID
	SetCongestionWindowSize(uint)
	GetCongestionWindowSize() uint
	GetPacketSendPeriod() time.Duration
	SetPacketSendPeriod(time.Duration)
	GetMaxFlowWindow() uint
	GetReceiveRate() int
	GetRTT() time.Duration
	GetMSS() uint
	SetACKPeriod(time.Duration)
}

// CongestionControl controls how timing is handled and UDT connections tuned
type CongestionControl interface {
	// Init to be called (only) at the start of a UDT connection.
	Init(CongestionControlParms)

	// Close to be called when a UDT connection is closed.
	Close(CongestionControlParms)

	// OnACK to be called when an ACK packet is received
	OnACK(CongestionControlParms, packet.PacketID)

	// OnNAK to be called when a loss report is received
	OnNAK(CongestionControlParms, []packet.PacketID)

	// OnTimeout to be called when a timeout event occurs
	OnTimeout(CongestionControlParms)

	// OnPktSent to be called when data is sent
	OnPktSent(CongestionControlParms, packet.DataPacket)

	// OnPktRecv to be called when data is received
	OnPktRecv(CongestionControlParms, packet.DataPacket)

	// OnCustomMsg to process a user-defined packet
	OnCustomMsg(CongestionControlParms, packet.UserDefControlPacket)
}
