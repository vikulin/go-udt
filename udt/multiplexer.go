package udt

import (
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"

	"github.com/vikulin/go-udt/udt/packet"
)

// packetWrapper is used to explicitly designate the destination of a packet,
// to assist with sending it to its destination
type packetWrapper struct {
	pkt  packet.Packet
	dest *net.UDPAddr
}

/*
A multiplexer multiplexes multiple UDT sockets over a single PacketConn.
*/
type multiplexer struct {
	network       string
	laddr         *net.UDPAddr   // the local address handled by this multiplexer
	conn          net.PacketConn // the UDPConn from which we read/write
	sockets       sync.Map       // the udtSockets handled by this multiplexer, by sockId
	rvSockets     sync.Map       // the list of any sockets currently in rendezvous mode
	listenSock    *listener      // the server socket listening to incoming connections, if there is one
	servSockMutex sync.Mutex
	mtu           uint               // the Maximum Transmission Unit of packets sent from this address
	nextSid       uint32             // the SockID for the next socket created
	pktOut        chan packetWrapper // packets queued for immediate sending
}

/*
multiplexerFor gets or creates a multiplexer for the given local address.  If a
new multiplexer is created, the given init function is run to obtain an
io.ReadWriter.
*/
//			os := runtime.GOOS
//			switch os {
//			case "windows":

//				err = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, 71 /* IP_MTU_DISCOVER for winsock2 */, 2 /* IP_PMTUDISC_DO */)
//			case "linux", "android":
//				err = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, 10 /* IP_MTU_DISCOVER */, 2 /* IP_PMTUDISC_DO */)
//			default:
//				err = syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, 67 /* IP_DONTFRAG */, 1)


func newMultiplexer(network string, laddr *net.UDPAddr, conn net.PacketConn) (m *multiplexer) {
	mtu, _ := discoverMTU(laddr.IP)
	m = &multiplexer{
		network: network,
		laddr:   laddr,
		conn:    conn,
		mtu:     mtu,
		nextSid: randUint32(),                  // Socket ID MUST start from a random value
		pktOut:  make(chan packetWrapper, 100), // todo: figure out how to size this
	}

	go m.goRead()
	go m.goWrite()

	return
}

func (m *multiplexer) key() string {
	return fmt.Sprintf("%s:%s", m.network, m.laddr.String())
}

func (m *multiplexer) listenUDT(l *listener) bool {
	m.servSockMutex.Lock()
	defer m.servSockMutex.Unlock()
	if m.listenSock != nil {
		return false
	}
	m.listenSock = l
	return true
}

func (m *multiplexer) unlistenUDT(l *listener) bool {
	m.servSockMutex.Lock()
	if m.listenSock != l {
		m.servSockMutex.Unlock()
		return false
	}
	m.listenSock = nil
	m.servSockMutex.Unlock()
	m.checkLive()
	return true
}

// Adapted from https://github.com/hlandau/degoutils/blob/master/net/mtu.go
const absMaxDatagramSize = 2147483646 // 2**31-2
func discoverMTU(ourIP net.IP) (uint, error) {

	ifaces, err := net.Interfaces()
	if err != nil {
		log.Printf("MTU for : %s = %s", net.IP, 65535)
		return 65535, err
	}

	var filtered []net.Interface
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			log.Printf("cannot retrieve iface addresses for %s: %s", iface.Name, err.Error())
			continue
		}
		for _, a := range addrs {
			var ipnet *net.IPNet
			switch v := a.(type) {
			case *net.IPAddr:
				ipnet = &net.IPNet{IP: v.IP, Mask: v.IP.DefaultMask()}
			case *net.IPNet:
				ipnet = v
			}
			if ipnet == nil {
				log.Printf("cannot retrieve IPNet from address %s on interface %s", a.String(), iface.Name)
				continue
			}
			if ipnet.Contains(ourIP) {
				filtered = append(filtered, iface)
			}
		}
	}
	if len(filtered) == 0 {
		log.Printf("cannot identify interface(s) associated with %s, doing blind search", ourIP.String())
		filtered = ifaces
	}

	var mtu int = 65535
	for _, iface := range filtered {
		if iface.Flags&(net.FlagUp|net.FlagLoopback) == net.FlagUp && iface.MTU > mtu {
			mtu = iface.MTU
		}
	}
	if mtu > absMaxDatagramSize {
		mtu = absMaxDatagramSize
	}
	log.Printf("MTU for : %s = %s", net.IP, mtu)
	return uint(mtu), nil
}

func (m *multiplexer) newSocket(config *Config, peer *net.UDPAddr, isServer bool, isDatagram bool) (s *udtSocket) {
	sid := atomic.AddUint32(&m.nextSid, ^uint32(0))

	s = newSocket(m, config, sid, isServer, isDatagram, peer)

	m.sockets.Store(sid, s)
	return s
}

func (m *multiplexer) closeSocket(sockID uint32) bool {
	if _, ok := m.sockets.Load(sockID); !ok {
		return false
	}
	m.sockets.Delete(sockID)
	m.checkLive()
	return true
}

func (m *multiplexer) checkLive() bool {
	if m.conn == nil { // have we already been destructed ?
		log.Printf("Connection closed")
		return false
	}
	if m.isLive() { // are we currently in use?
		return true
	}

	// deregister this multiplexer
	key := m.key()
	multiplexers.Delete(key)
	if m.isLive() { // checking this in case we have a race condition with multiplexer destruction
		multiplexers.Store(key, m)
		return true
	}

	// tear everything down
	m.conn.Close()
	close(m.pktOut)
	log.Printf("Connection closed")
	return false
}

func (m *multiplexer) isLive() bool {
	if m.conn == nil {
		return false
	}
	m.servSockMutex.Lock()
	if m.listenSock != nil {
		m.servSockMutex.Unlock()
		return true
	}
	m.servSockMutex.Unlock()

	isEmpty := true
	m.rvSockets.Range(func(key, val interface{}) bool {
		isEmpty = false
		return false
	})
	if !isEmpty {
		return true
	}

	m.sockets.Range(func(key, val interface{}) bool {
		isEmpty = false
		return false
	})
	return !isEmpty
}

func (m *multiplexer) startRendezvous(s *udtSocket) {
	peer := s.raddr.String()
	m.rvSockets.Store(peer, s)
}

func (m *multiplexer) endRendezvous(s *udtSocket) {
	peer := s.raddr.String()
	sock, ok := m.rvSockets.Load(peer)
	if ok && sock.(*udtSocket) == s {
		m.rvSockets.Delete(peer)
	}
}

/*
read runs in a goroutine and reads packets from conn using a buffer from the
readBufferPool, or a new buffer.
*/
func (m *multiplexer) goRead() {
	buf := make([]byte, m.mtu)
	for {
		numBytes, from, err := m.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		m.readPacket(buf, numBytes, from)
	}
}

func (m *multiplexer) readPacket(buf []byte, numBytes int, from net.Addr) {
	p, err := packet.ReadPacketFrom(buf[0:numBytes])
	if err != nil {
		log.Printf("Unable to read packet: %s", err)
		return
	}

	// attempt to route the packet
	sockID := p.SocketID()
	if sockID == 0 {
		var hsPacket *packet.HandshakePacket
		var ok bool
		if hsPacket, ok = p.(*packet.HandshakePacket); !ok {
			log.Printf("Received non-handshake packet with destination socket = 0")
			return
		}

		foundMatch := false
		m.rvSockets.Range(func(key, val interface{}) bool {
			if val.(*udtSocket).readHandshake(m, hsPacket, from.(*net.UDPAddr)) {
				foundMatch = true
				return false
			}
			return true
		})
		if foundMatch {
			return
		}
		m.servSockMutex.Lock()
		if m.listenSock != nil {
			m.listenSock.readHandshake(m, hsPacket, from.(*net.UDPAddr))
		}
		m.servSockMutex.Unlock()
	}
	if ifDestSock, ok := m.sockets.Load(sockID); ok {
		ifDestSock.(*udtSocket).readPacket(m, p, from.(*net.UDPAddr))
	}
}

/*
write runs in a goroutine and writes packets to conn using a buffer from the
writeBufferPool, or a new buffer.
*/
func (m *multiplexer) goWrite() {
	buf := make([]byte, m.mtu)
	pktOut := m.pktOut
	for {
		select {
		case pw, ok := <-pktOut:
			if !ok {
				return
			}
			plen, err := pw.pkt.WriteTo(buf)
			if err != nil {
				// TODO: handle write error
				log.Fatalf("Unable to buffer out: %s", err.Error())
				continue
			}

			if _, err = m.conn.WriteTo(buf[0:plen], pw.dest); err != nil {
				// TODO: handle write error
				log.Fatalf("Unable to write out: %s", err.Error())
			}
		}
	}
}

func (m *multiplexer) sendPacket(destAddr *net.UDPAddr, destSockID uint32, ts uint32, p packet.Packet) {
	p.SetHeader(destSockID, ts)
	if destSockID == 0 {
		if _, ok := p.(*packet.HandshakePacket); !ok {
			log.Fatalf("Sending non-handshake packet with destination socket = 0")
		}
	}
	m.pktOut <- packetWrapper{pkt: p, dest: destAddr}
}
