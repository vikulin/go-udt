package udt

import (
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

type udtSocketCc struct {
	// channels
	closed     <-chan struct{} // closed when socket is closed
	socket     *udtSocket
	congestion CongestionControl // congestion control object for this socket
}

func newUdtSocketCc(s *udtSocket, closed <-chan struct{}) *udtSocketCc {
	newCongestion := s.Config.CongestionForSocket
	if newCongestion == nil {
		newCongestion = DefaultConfig().CongestionForSocket
	}

	sc := &udtSocketCc{
		socket:     s,
		closed:     closed,
		congestion: newCongestion(s),
	}
	//go sr.goCongestionEvent()
	return sc
}

// Init to be called (only) at the start of a UDT connection.
func (s *udtSocketCc) init() {
	s.congestion.Init(s)
}

// Close to be called when a UDT connection is closed.
func (s *udtSocketCc) close() {
	s.congestion.Close(s)
}

// OnACK to be called when an ACK packet is received
func (s *udtSocketCc) onACK(pktID packet.PacketID) {
	s.congestion.OnACK(s, pktID)
}

// OnNAK to be called when a loss report is received
func (s *udtSocketCc) onNAK(loss []packet.PacketID) {
	s.congestion.OnNAK(s, loss)
}

// OnTimeout to be called when a timeout event occurs
func (s *udtSocketCc) onTimeout() {
	s.congestion.OnTimeout(s)
}

// OnPktSent to be called when data is sent
func (s *udtSocketCc) onPktSent(p packet.Packet) {
	s.congestion.OnPktSent(s, p)
}

// OnPktRecv to be called when data is received
func (s *udtSocketCc) onPktRecv(p packet.DataPacket) {
	s.congestion.OnPktRecv(s, p)
}

// OnCustomMsg to process a user-defined packet
func (s *udtSocketCc) onCustomMsg(p packet.UserDefControlPacket) {
	s.congestion.OnCustomMsg(s, p)
}

// GetSndCurrSeqNo is the most recently sent packet ID
func (s *udtSocketCc) GetSndCurrSeqNo() packet.PacketID {
	// TODO
	return packet.PacketID{}
}

// SetCongestionWindowSize sets the size of the congestion window (in packets)
func (s *udtSocketCc) SetCongestionWindowSize(uint) {
	// TODO
}

// GetCongestionWindowSize gets the size of the congestion window (in packets)
func (s *udtSocketCc) GetCongestionWindowSize() uint {
	// TODO
	return 0
}

// GetPacketSendPeriod gets the current delay between sending packets
func (s *udtSocketCc) GetPacketSendPeriod() time.Duration {
	// TODO
	return time.Duration(0)
}

// SetPacketSendPeriod sets the current delay between sending packets
func (s *udtSocketCc) SetPacketSendPeriod(time.Duration) {
	// TODO
}

// GetMaxFlowWindow is the largest number of unacknowledged packets we can receive (in packets)
func (s *udtSocketCc) GetMaxFlowWindow() uint {
	// TODO
	return 0
}

// GetReceiveRate is the current calculated receive rate (in packets/sec)
func (s *udtSocketCc) GetReceiveRate() int {
	// TODO
	return 0
}

// GetBandwidth is the current calculated bandwidth (in packets/sec)
func (s *udtSocketCc) GetBandwidth() int {
	// TODO
	return 0
}

// GetRTT is the current calculated roundtrip time between peers
func (s *udtSocketCc) GetRTT() time.Duration {
	// TODO
	return time.Duration(0)
}

// GetMSS is the largest packet size we can currently send (in bytes)
func (s *udtSocketCc) GetMSS() uint {
	// TODO
	return 0
}

// SetACKPerid sets the time between ACKs sent to the peer
func (s *udtSocketCc) SetACKPeriod(time.Duration) {
	// TODO
}
