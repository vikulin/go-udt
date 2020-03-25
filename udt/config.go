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
