package udt

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/odysseus654/go-udt/udt/packet"
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
	laddr         *net.UDPAddr // the local address handled by this multiplexer
	conn          *net.UDPConn // the UDPConn from which we read/write
	sockets       sync.Map     // the udtSockets handled by this multiplexer, by sockId
	rvSockets     []*udtSocket // the list of any sockets currently in rendezvous mode
	listenSock    *listener    // the server socket listening to incoming connections, if there is one
	servSockMutex sync.Mutex
	//sendQ        *udtSocketQueue // priority queue of udtSockets awaiting a send (actually includes ones with no packets waiting too)
	pktOut chan packetWrapper // packets queued for immediate sending
	//in chan packetHolder // packets inbound from the PacketConn
	//out             chan packet       // packets outbound to the PacketConn
	//writeBufferPool *bpool.BufferPool // leaky buffer pool for writing to conn
	//readBytePool *bpool.BytePool // leaky byte pool for reading from conn
}

/*
multiplexerFor gets or creates a multiplexer for the given local address.  If a
new multiplexer is created, the given init function is run to obtain an
io.ReadWriter.
*/
func multiplexerFor(network string, laddr *net.UDPAddr) (*multiplexer, error) {
	key := fmt.Sprintf("%s:%s", network, laddr.String())
	if ifM, ok := multiplexers.Load(key); ok {
		m := ifM.(*multiplexer)
		if m.isLive() { // checking this in case we have a race condition with multiplexer destruction
			return m, nil
		}
	}

	// No multiplexer, need to create connection
	conn, err := net.ListenUDP(network, laddr)
	if err != nil {
		return nil, err
	}

	m := newMultiplexer(network, laddr, conn)
	multiplexers.Store(key, m)
	return m, nil
}

func newMultiplexer(network string, laddr *net.UDPAddr, conn *net.UDPConn) (m *multiplexer) {
	m = &multiplexer{
		network: network,
		laddr:   laddr,
		conn:    conn,
		//sendQ:        newUdtSocketQueue(),
		pktOut: make(chan packetWrapper, 100), // todo: figure out how to size this
		//in:           make(chan packetHolder, 100),  // todo: make this tunable
		//out:             make(chan packet, 100),                         // todo: make this tunable
		//writeBufferPool: bpool.NewBufferPool(25600), // todo: make this tunable
		//readBytePool:    bpool.NewBytePool(25600, getMaxDatagramSize()), // todo: make this tunable
	}

	go m.coordinate()
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

func (m *multiplexer) checkLive() bool {
	if m.conn == nil { // have we already been destructed ?
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
	m.conn = nil
	close(m.pktOut)
	m.pktOut = nil
	return false
}

func (m *multiplexer) isLive() bool {
	if m.conn == nil {
		return false
	}
	m.servSockMutex.Lock()
	defer m.servSockMutex.Unlock()

	if m.listenSock != nil {
		return true
	}
	if m.rvSockets != nil {
		if len(m.rvSockets) > 0 {
			return true
		}
	}

	isEmpty := true
	m.sockets.Range(func(key, val interface{}) bool {
		isEmpty = false
		return false
	})
	return !isEmpty
}

/*
read runs in a goroutine and reads packets from conn using a buffer from the
readBufferPool, or a new buffer.
*/
func (m *multiplexer) goRead() {
	buf := make([]byte, getMaxDatagramSize())
	for {
		numBytes, from, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			log.Printf("Unable to read into buffer: %s", err)
			continue
		}

		r := bytes.NewReader(buf)
		p, err := packet.ReadPacketFrom(buf[0:numBytes])
		if err != nil {
			log.Printf("Unable to read packet: %s", err)
			continue
		}

		// attempt to route the packet
		sockID := p.SocketID()
		if sockID == 0 {
			var hsPacket *packet.HandshakePacket
			var ok bool
			if hsPacket, ok = p.(*packet.HandshakePacket); !ok {
				log.Printf("Received non-handshake packet with destination socket = 0")
				continue
			}

			m.servSockMutex.Lock()
			if m.rvSockets != nil {
				foundMatch := false
				for _, sock := range m.rvSockets {
					if sock.readHandshake(m, hsPacket, from) {
						foundMatch = true
						break
					}
				}
				if foundMatch {
					m.servSockMutex.Unlock()
					continue
				}
			}
			if m.listenSock != nil {
				m.listenSock.readHandshake(m, hsPacket, from)
				m.servSockMutex.Unlock()
			}
		}
		if ifDestSock, ok := m.sockets.Load(sockID); ok {
			ifDestSock.(*udtSocket).readPacket(m, p, from)
		}
	}
}

/*
write runs in a goroutine and writes packets to conn using a buffer from the
writeBufferPool, or a new buffer.
*/
func (m *multiplexer) goWrite() {
	buf := make([]byte, getMaxDatagramSize())
	for {
		select {
		case pw, ok := <-m.pktOut:
			if !ok {
				return
			}
			plen, err := pw.pkt.WriteTo(buf)
			if err != nil {
				// TODO: handle write error
				log.Fatalf("Unable to buffer out: %s", err.Error())
				continue
			}

			log.Printf("Writing to %s", pw.pkt.SocketID())
			if _, err = m.conn.WriteToUDP(buf[0:plen], pw.dest); err != nil {
				// TODO: handle write error
				log.Fatalf("Unable to write out: %s", err.Error())
			}
		}
	}
}

func (m *multiplexer) sendControl(destAddr *net.UDPAddr, destSockID uint32, ts uint32, p packet.ControlPacket) error {
	p.SetHeader(destSockID, ts)
	m.pktOut <- packetWrapper{pkt: p, dest: destAddr}
}

// coordinate runs in a goroutine and coordinates all of the multiplexer's work
func (m *multiplexer) coordinate() {
	for {
		select {
		case p := <-m.in:
			m.handleInbound(p)
		}
	}
}

func (m *multiplexer) handleInbound(ph packetHolder) {
	switch p := ph.packet.(type) {
	case *handshakePacket:
		// Only process packet if version and type are supported
		log.Println("Got handshake packet")
		if p.udtVer == 4 && p.sockType == DGRAM {
			log.Println("Right version and type")
			dstSockID := p.h.dstSockId
			if dstSockID == 0 {
				dstSockID = p.sockId
				// create a new udt socket and remember it
				if s, err := newServerSocket(m, ph.from, p); err == nil {
					m.sockets[dstSockID] = s
					log.Printf("Responding to handshake from %s", dstSockID)
					s.respondInitHandshake(p)
				}

			} else {
				s := m.sockets[dstSockID]
				if s == nil {
					return
				}
				if p.reqType > 0 {
					log.Println("Accepting handshake from server")
					s.respondAcceptHandshake(p)

				} else if p.synCookie == s.synCookie {
					log.Println("Server acknowledge handshake")
					s.acknowledgeHanshake()
				}
			}
		}
	}
}
