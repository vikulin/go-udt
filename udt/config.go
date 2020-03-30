package udt

import (
	"context"
	"net"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

// Config controls behavior of sockets created with it
type Config struct {
	CanAcceptDgram     bool          // can this listener accept datagrams?
	CanAcceptStream    bool          // can this listener accept streams?
	ListenReplayWindow time.Duration // length of time to wait for repeated incoming connections
	MaxPacketSize      uint          // Upper limit on maximum packet size (0 = unlimited)
	MaxBandwidth       uint64        // Maximum bandwidth to take with this connection (in bytes/sec, 0 = unlimited)

	CanAccept           func(hsPacket *packet.HandshakePacket, from *net.UDPAddr) error // can this listener accept this connection?
	CongestionForSocket func(sock *udtSocket) CongestionControl                         // create or otherwise return the CongestionControl for this socket

	// imported from reference implementation
	UDT_FC       uint          // m_iFlightFlagSize (max 32)
	UDT_SNDBUF   uint          // m_iSndBufSize
	UDT_RCVBUF   uint          // m_iRcvBufSize (min m_iFlightFlagSize packets)
	UDT_LINGER   time.Duration // m_Linger
	UDT_SNDTIMEO int           // m_iSndTimeOut
	UDT_RCVTIMEO int           // m_iRcvTimeOut
}

// Listen listens for incoming UDT connections addressed to the local address laddr.
// See function net.ListenUDP for a description of net and laddr.
func (c *Config) Listen(ctx context.Context, network string, addr string) (net.Listener, error) {
	return listenUDT(ctx, c, network, addr)
}

// Dial establishes an outbound UDT connection using the supplied net, laddr and raddr.  See function net.DialUDP for a description of net, laddr and raddr.
func (c *Config) Dial(ctx context.Context, network string, laddr string, raddr *net.UDPAddr, isStream bool) (net.Conn, error) {
	return dialUDT(ctx, c, network, laddr, raddr, isStream)
}

// Rendezvous establishes an outbound UDT connection using the supplied net, laddr and raddr.  See function net.DialUDP for a description of net, laddr and raddr.
func (c *Config) Rendezvous(ctx context.Context, network string, laddr string, raddr *net.UDPAddr, isStream bool) (net.Conn, error) {
	return rendezvousUDT(ctx, c, network, laddr, raddr, isStream)
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
