package udt

import (
	"context"
	"crypto/sha1"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"log"
	"net"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

var (
	endianness = binary.BigEndian
)

/*
Listener implements the io.Listener interface for UDT.
*/
type listener struct {
	m         *multiplexer
	accept    chan *udtSocket
	closed    chan struct{}
	synEpoch  uint32
	synCookie uint32
	synHash   hash.Hash
}

// resolveAddr resolves addr, which may be a literal IP
// address or a DNS name, and returns a list of internet protocol
// family addresses. The result contains at least one address when
// error is nil.
func resolveAddr(ctx context.Context, network, addr string) (*net.UDPAddr, error) {
	var (
		err        error
		host, port string
		portnum    int
	)
	switch network {
	case "udp", "udp4", "udp6":
		if addr != "" {
			if host, port, err = net.SplitHostPort(addr); err != nil {
				return nil, err
			}
			if portnum, err = net.DefaultResolver.LookupPort(ctx, network, port); err != nil {
				return nil, err
			}
		}
	default:
		return nil, net.UnknownNetworkError(network)
	}

	inetaddr := func(ip net.IPAddr) *net.UDPAddr {
		return &net.UDPAddr{IP: ip.IP, Port: portnum, Zone: ip.Zone}
	}

	if host == "" {
		return inetaddr(net.IPAddr{}), nil
	}

	// Try as a literal IP address, then as a DNS name.
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	// Issue 18806: if the machine has halfway configured
	// IPv6 such that it can bind on "::" (IPv6unspecified)
	// but not connect back to that same address, fall
	// back to dialing 0.0.0.0.
	if len(ips) == 1 && ips[0].IP.Equal(net.IPv6unspecified) {
		ips = append(ips, net.IPAddr{IP: net.IPv4zero})
	}

	var filter func(net.IPAddr) bool
	if network != "" && network[len(network)-1] == '4' {
		filter = func(addr net.IPAddr) bool {
			return addr.IP.To4() != nil
		}
	}
	if network != "" && network[len(network)-1] == '6' {
		filter = func(addr net.IPAddr) bool {
			return len(addr.IP) == net.IPv6len && addr.IP.To4() == nil
		}
	}

	var addrs []*net.UDPAddr
	for _, ip := range ips {
		if filter == nil || filter(ip) {
			addrs = append(addrs, inetaddr(ip))
		}
	}
	if len(addrs) == 0 {
		return nil, &net.AddrError{Err: "no suitable address found", Addr: host}
	}
	return addrs[0], nil
}

func Listen(ctx context.Context, network, address string) (Listener, error) {
	switch network {
	case "udp", "udp4", "udp6":
	default:
		return nil, net.UnknownNetworkError(network)
	}

	addr, err := resolveAddr(ctx, network, address)
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: nil, Err: err}
	}

	m, err := multiplexerFor(network, addr)
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: addr, Err: err}
	}

	l := &listener{
		m:         m,
		synCookie: randUint32(),
		synEpoch:  randUint32(),
		accept:    make(chan *udtSocket, 100),
		closed:    make(chan struct{}, 1),
		synHash:   sha1.New(), // it's weak but fast, hopefully we don't need *that* much security here
	}

	if ok := m.listenUDT(l); !ok {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: addr, Err: errors.New("Port in use")}
	}
	go l.goBumpSynEpoch()

	return l, nil
}

func (l *listener) goBumpSynEpoch() {
	closed := l.closed
	for {
		select {
		case _, ok := <-closed:
			return
		case <-time.After(64 * time.Second):
			l.synEpoch++
		}
	}
}

func (l *listener) Accept() (io.ReadWriteCloser, error) {
	socket, ok := <-l.accept
	if ok {
		return socket, nil
	}
	return nil, errors.New("Listener closed")
}

func (l *listener) Close() (err error) {
	a := l.accept
	c := l.closed
	l.accept = nil
	l.closed = nil
	if a == nil || c == nil {
		return errors.New("Listener closed")
	}
	close(a)
	close(c)

	l.m.unlistenUDT(l)
	return nil
}

func (l *listener) Addr() net.Addr {
	return l.m.laddr
}

func (l *listener) genSynCookie(from *net.UDPAddr) uint32 {
	bCookie := make([]byte, 4)
	endianness.PutUint32(bCookie, l.synCookie)
	bPort := make([]byte, 2)
	endianness.PutUint16(bPort, uint16(from.Port))
	val := append(bCookie, append([]byte(from.IP), bPort...)...)
	hash := endianness.Uint32(l.synHash.Sum(val))
	return ((l.synEpoch & 0x1f) << 11) | (hash & 0x07ff)
}

func (l *listener) checkSynCookie(cookie uint32, from *net.UDPAddr) (bool, uint32) {
	newCookie := l.genSynCookie(from)
	if (newCookie & 0x07ff) != (cookie & 0x07ff) {
		return false, newCookie
	}
	epoch := (cookie & 0xf100) >> 11
	return (epoch == (l.synEpoch & 0x1f)) || (epoch == ((l.synEpoch - 1) & 0x1f)), newCookie
}

func (l *listener) readHandshake(m *multiplexer, hsPacket *packet.HandshakePacket, from *net.UDPAddr) bool {

	isSYN, newCookie := l.checkSynCookie(hsPacket.SynCookie, from)
	if !isSYN {
		err := m.sendControl(from, hsPacket.SockID, 0, &packet.HandshakePacket{
			//ts        uint32
			//DstSockID  = hsPacket.SockID
			UdtVer:   hsPacket.UdtVer,
			SockType: hsPacket.SockType,
			// InitPkgSeq = 0
			//MaxPktSize     uint32     // maximum packet size (including UDP/IP headers)
			//MaxFlowWinSize uint32     // maximum flow window size
			ReqType: 1,
			// SockID = 0
			SynCookie: newCookie,
			SockAddr:  from.IP,
		})
		if err != nil {
			log.Printf("Listener handshake response failed: %s", err.Error())
			return false
		}
		return true
	}

	s, err := l.m.newClientSocket(from)
	if err != nil {
		log.Printf("New socket creation from listener failed: %s", err.Error())
		return false
	}
	if !s.readHandshake(m, hsPacket, from) {
		return false
	}

	l.accept <- s
	return true
}
