package udt

import (
	"io"
	"log"
	"math"
	"net"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

type sockState int

const (
	sockStateInit sockState = iota
	//sockStateOpened
	//sockStateListening
	sockStateServConnecting
	//sockStateConnecting
	sockStateConnected
	//sockStateBroken
	//sockStateClosing
	//sockStateClosed
	//sockStateNonexist
)

/*
udtSocket encapsulates a UDT socket between a local and remote address pair, as
defined by the UDT specification.  udtSocket implements the net.Conn interface
so that it can be used anywhere that a stream-oriented network connection
(like TCP) would be used.
*/
type udtSocket struct {
	m            *multiplexer // the multiplexer that handles this socket
	raddr        *net.UDPAddr // the remote address
	boundWriter  io.Writer    // a UDP writer that knows which address to send to
	created      time.Time    // the time that this socket was created
	sockState    sockState
	isDatagram   bool
	ackPeriod    uint32                  // in microseconds
	nakPeriod    uint32                  // in microseconds
	expPeriod    uint32                  // in microseconds
	sndPeriod    uint32                  // in microseconds
	ctrlIn       chan *packet.DataPacket // inbound control packets
	dataIn       chan *packet.DataPacket // inbound data packets
	dataOut      *packetQueue            // queue of outbound data packets
	pktSeq       uint32                  // the current packet sequence number
	currDp       *packet.DataPacket      // currently reading data packet (for partial reads)
	currDpOffset int                     // offset in currIn (for partial reads)

	// The below fields mirror what's seen on handshakePacket
	udtVer         uint32
	farPktSeq      uint32
	maxPktSize     uint32
	maxFlowWinSize uint32
	sockID         uint32
	synCookie      uint32
	sockAddr       net.IP
}

/*******************************************************************************
 Implementation of net.Conn interface
*******************************************************************************/

func (s *udtSocket) Read(p []byte) (n int, err error) {
	if s.currDp == nil {
		// Grab the next data packet
		s.currDp = <-s.dataIn
		s.currDpOffset = 0
	}
	n = copy(p, s.currDp.data[s.currDpOffset:])
	s.currDpOffset += n
	if s.currDpOffset >= len(s.currDp.data) {
		// we've exhausted the current data packet, reset to nil
		s.currDp = nil
	}

	return
}

// TODO: implement ReadFrom and WriteTo for performance(?)

func (s *udtSocket) Write(p []byte) (n int, err error) {
	s.pktSeq++
	dp := &dataPacket{
		seq:       s.pktSeq,
		ts:        uint32(time.Now().Sub(s.created) / time.Microsecond),
		dstSockId: s.sockId,
		data:      p,
	}
	s.dataOut.push(dp)
	return
}

func (s *udtSocket) Close() (err error) {
	// todo send shutdown packet

	// Remove from connected socket list
	s.m.socketsMutex.Lock()
	defer s.m.socketsMutex.Unlock()
	delete(s.m.sockets, s.sockId)

	s.sockState = sock_state_closed

	if s.m.mode == mode_client {
		return s.m.conn.Close()
	}

	return
}

func (s *udtSocket) LocalAddr() net.Addr {
	return s.m.laddr
}

func (s *udtSocket) RemoteAddr() net.Addr {
	return s.raddr
}

func (s *udtSocket) SetDeadline(t time.Time) error {
	// todo set timeout through EXP and SND
	//return s.m.conn.SetDeadline(t)
	return nil
}

func (s *udtSocket) SetReadDeadline(t time.Time) error {
	// todo set timeout through EXP
	//return s.m.conn.SetReadDeadline(t)
	return nil
}

func (s *udtSocket) SetWriteDeadline(t time.Time) error {
	// todo set timeout through EXP or SND
	//return s.m.conn.SetWriteDeadline(t)
	return nil
}

/*******************************************************************************
 Private functions
*******************************************************************************/

/*
nextSendTime returns the ts of the next data packet with the lowest ts of
queued packets, or math.MaxUint32 if no packets are queued.
*/
func (s *udtSocket) nextSendTime() (ts uint32) {
	p := s.dataOut.peek()
	if p != nil {
		return p.sendTime()
	}
	return math.MaxUint32
}

// newSocket creates a new UDT socket, which will be configured afterwards as either an incoming our outgoing socket
func newSocket(m *multiplexer, sockID uint32, raddr *net.UDPAddr) (s *udtSocket, err error) {
	//	raddr := (m.conn.RemoteAddr()).(*net.UDPAddr)
	s = &udtSocket{
		m:              m,
		raddr:          raddr,
		boundWriter:    m.conn, // &boundUDPWriter{m.conn, raddr},
		sockState:      sockStateInit,
		udtVer:         4,
		pktSeq:         randUint32(),
		maxPktSize:     uint32(getMaxDatagramSize()),
		maxFlowWinSize: 25600, // todo: turn tunable (minimum 32)
		isDatagram:     true,
		sockID:         sockID,
		sockAddr:       raddr.IP,
		synCookie:      randUint32(),
		dataOut:        newPacketQueue(),
	}

	return
}

// readHandshake is received when a handshake packet is received without a destination, either as part
// of a listening response or as a rendezvous connection
func (s *udtSocket) readHandshake(m *multiplexer, p *packet.HandshakePacket, from *net.UDPAddr) bool {
	if s.sockState != sockStateInit {
		return false // we're not in a position to receive handshakes
	}
	if from != s.raddr {
		log.Printf("huh? initted with %s but handshake with %s", s.raddr.String(), from.String())
		return false
	}
	/*
		reqType        int32      // connection type (regular(1), rendezvous(0), -1/-2 response)
		synCookie      uint32     // SYN cookie
		sockAddr       net.IP     // the IP address of the UDP socket to which this packet is being sent
	*/
	s.udtVer = p.udtVer
	s.farPktSeq = p.initPktSeq
	s.isDatagram = p.sockType == packet.TypeDGRAM
	s.synCookie = randUint32()

	if s.maxPktSize > p.maxPktSize {
		s.maxPktSize = p.maxPktSize
	}
	if s.maxFlowWinSize > p.maxFlowWinSize {
		s.maxFlowWinSize = p.maxFlowWinSize
	}

	s.sockState = sockStateServConnecting
	return true
}

/*******************************************************************************
 Lifecycle functions
*******************************************************************************/

func (s *udtSocket) initHandshake() {
	p := handshakePacket{
		udtVer:         s.udtVer,
		sockType:       s.sockType,
		initPktSeq:     s.initPktSeq,
		maxPktSize:     s.maxPktSize,
		maxFlowWinSize: s.maxFlowWinSize,
		reqType:        1,
		sockId:         s.sockId,
		sockAddr:       s.sockAddr,
	}
	s.sockState = sock_state_connecting
	s.m.ctrlOut <- &p
}

func (s *udtSocket) respondInitHandshake(p *handshakePacket) {
	p.h.dstSockId = p.sockId
	p.synCookie = s.synCookie
	s.sockState = sock_state_connecting
	s.m.ctrlOut <- p
}

func (s *udtSocket) respondAcceptHandshake(p *handshakePacket) {
	p.h.dstSockId = p.sockId
	p.reqType = -1
	s.sockState = sock_state_connected
	s.m.ctrlOut <- p
	close(s.handshaked)
}

func (s *udtSocket) readPacket(m *multiplexer, p packet.Packet, from *net.UDPAddr) {
	switch p := ph.packet.(type) {
	case *handshakePacket:
	}
}
