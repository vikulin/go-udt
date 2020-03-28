package udt

import (
	"context"
	"net"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

// Config controls behavior of sockets created with it
type Config struct {
	CanAcceptDgram      bool                                                            // can this listener accept datagrams?
	CanAcceptStream     bool                                                            // can this listener accept streams?
	CanAccept           func(hsPacket *packet.HandshakePacket, from *net.UDPAddr) error // can this listener accept this connection?
	ListenReplayWindow  time.Duration                                                   // length of time to wait for repeated incoming connections
	CongestionForSocket func(sock *udtSocket) CongestionControl                         // create or otherwise return the CongestionControl for this socket

	// imported from reference implementation
	MaxPacketSize  uint        // Upper limit on maximum packet size (0 = unlimited)
	UDT_SNDSYN     bool        // m_bSynSending
	UDT_RCVSYN     bool        // m_bSynRecving
	UDT_FC         uint        // m_iFlightFlagSize (max 32)
	UDT_SNDBUF     uint        // m_iSndBufSize
	UDT_RCVBUF     uint        // m_iRcvBufSize (min m_iFlightFlagSize packets)
	UDT_LINGER     interface{} // m_Linger
	UDP_SNDBUF     uint        // m_iUDPSndBufSize (min m_iMSS)
	UDP_RCVBUF     uint        // m_iUDPRcvBufSize (min m_iMSS)
	UDT_RENDEZVOUS bool        // m_bRendezvous
	UDT_SNDTIMEO   int         // m_iSndTimeOut
	UDT_RCVTIMEO   int         // m_iRcvTimeOut
	UDT_REUSEADDR  bool        // m_bReuseAddr
	UDT_MAXBW      int64       // m_llMaxBW
}

func (c *Config) Listen(ctx context.Context, network string, addr string) (net.Listener, error) {
	return listenUDT(ctx, c, network, addr)
}

func (c *Config) Dial(ctx context.Context, network string, laddr string, raddr *net.UDPAddr, isStream bool) (net.Conn, error) {
	return dialUDT(ctx, c, network, laddr, raddr, isStream)
}

// DefaultConfig constructs a Config with default values
func DefaultConfig() *Config {
	return &Config{
		CanAcceptDgram:     true,
		CanAcceptStream:    true,
		ListenReplayWindow: 5 * time.Minute,
		CongestionForSocket: func(sock *udtSocket) CongestionControl {
			return &NativeCongestionControl{}
		},
	}
}
