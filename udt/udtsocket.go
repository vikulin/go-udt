package udt

import (
	"errors"
	"log"
	"math"
	"net"
	"time"

	"github.com/odysseus654/go-udt/udt/packet"
)

type sockState int

const (
	sockStateInit sockState = iota
	sockStateConnecting
	sockStateConnected
	sockStateClosed
)

/*
udtSocket encapsulates a UDT socket between a local and remote address pair, as
defined by the UDT specification.  udtSocket implements the net.Conn interface
so that it can be used anywhere that a stream-oriented network connection
(like TCP) would be used.
*/
type udtSocket struct {
	m              *multiplexer // the multiplexer that handles this socket
	raddr          *net.UDPAddr // the remote address
	created        time.Time    // the time that this socket was created
	sockState      sockState
	udtVer         uint32
	pktSeq         uint32 // the current packet sequence number
	maxPktSize     uint32
	maxFlowWinSize uint32
	isDatagram     bool
	isServer       bool
	sockID         uint32
	farSockID      uint32
	farPktSeq      uint32
	//	ackPeriod      uint32       // in microseconds
	//	nakPeriod      uint32       // in microseconds
	//	expPeriod      uint32       // in microseconds
	//	sndPeriod      uint32       // in microseconds
	messageIn chan []byte // inbound messages
	//	dataOut        *packetQueue // queue of outbound data packets
	currDp       []byte // stream connections: currently reading message (for partial reads)
	currDpOffset int    // stream connections: offset in currDp (for partial reads)
}

/*******************************************************************************
 Implementation of net.Conn interface
*******************************************************************************/

func (s *udtSocket) Read(p []byte) (n int, err error) {
	if s.isDatagram {
		msg := <-s.messageIn
		n = copy(p, msg)
		if n < len(msg) {
			err = errors.New("Message truncated")
		}
	} else {
		if s.currDp == nil {
			// Grab the next data packet
			s.currDp = <-s.messageIn
			s.currDpOffset = 0
		}
		n = copy(p, s.currDp[s.currDpOffset:])
		s.currDpOffset += n
		if s.currDpOffset >= len(s.currDp) {
			// we've exhausted the current data packet, reset to nil
			s.currDp = nil
		}
	}
	return
}

func (s *udtSocket) Write(p []byte) (n int, err error) {
	s.pktSeq++
	dp := &packet.DataPacket{
		seq:       s.pktSeq,
		ts:        uint32(time.Now().Sub(s.created) / time.Microsecond),
		dstSockId: s.sockId,
		data:      p,
	}
	s.dataOut.push(dp)
	return
}

func (s *udtSocket) Close() error {
	// todo send shutdown packet
	err := s.sendControl(&packet.ShutdownPacket{})
	if err != nil {
		return err
	}

	return s.handleClose()
}

func (s *udtSocket) handleClose() (err error) {
	// Remove from connected socket list
	s.m.closeSocket(s.sockID)
	s.sockState = sockStateClosed

	close(s.messageIn)
	return nil
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
func newSocket(m *multiplexer, sockID uint32, isServer bool, raddr *net.UDPAddr) (s *udtSocket, err error) {
	//	raddr := (m.conn.RemoteAddr()).(*net.UDPAddr)
	s = &udtSocket{
		m:              m,
		raddr:          raddr,
		created:        time.Now(),
		sockState:      sockStateInit,
		udtVer:         4,
		isServer:       isServer,
		pktSeq:         randUint32(),
		maxPktSize:     uint32(getMaxDatagramSize()),
		maxFlowWinSize: 25600, // todo: turn tunable (minimum 32)
		isDatagram:     true,
		sockID:         sockID,
		dataOut:        newPacketQueue(),
		messageIn:      make(chan []byte, 256),
	}

	return
}

func (s *udtSocket) startConnect() error {
	s.sockState = sockStateConnecting
	return s.sendHandshake(0)
}

func (s *udtSocket) sendHandshake(synCookie uint32) error {
	sockType := packet.TypeSTREAM
	if s.isDatagram {
		sockType = packet.TypeDGRAM
	}

	return s.sendControl(&packet.HandshakePacket{
		UdtVer:         s.udtVer,
		SockType:       sockType,
		InitPktSeq:     s.pktSeq,
		MaxPktSize:     s.maxPktSize,     // maximum packet size (including UDP/IP headers)
		MaxFlowWinSize: s.maxFlowWinSize, // maximum flow window size
		ReqType:        1,
		SockID:         s.sockID,
		SynCookie:      synCookie,
		SockAddr:       s.raddr.IP,
	})
}

func (s *udtSocket) sendControl(p packet.ControlPacket) error {
	ts := uint32(time.Now().Sub(s.created) / time.Microsecond)
	return s.m.sendControl(s.raddr, s.farSockID, ts, p)
}

// readHandshake is received when a handshake packet is received without a destination, either as part
// of a listening response or as a rendezvous connection
func (s *udtSocket) readHandshake(m *multiplexer, p *packet.HandshakePacket, from *net.UDPAddr) bool {
	if from != s.raddr {
		log.Printf("huh? initted with %s but handshake with %s", s.raddr.String(), from.String())
		return false
	}

	switch s.sockState {
	case sockStateInit: // server accepting a connection from a client
		s.udtVer = p.UdtVer
		s.farSockID = p.SockID
		s.farPktSeq = p.InitPktSeq
		s.isDatagram = p.SockType == packet.TypeDGRAM

		if s.maxPktSize > p.MaxPktSize {
			s.maxPktSize = p.MaxPktSize
		}
		if s.maxFlowWinSize > p.MaxFlowWinSize {
			s.maxFlowWinSize = p.MaxFlowWinSize
		}
		s.sockState = sockStateConnected

		err := s.sendHandshake(p.SynCookie)
		if err != nil {
			log.Printf("Socket handshake response failed: %s", err.Error())
			return false
		}
		return true

	case sockStateConnecting: // client attempting to connect to server
		s.farSockID = p.SockID
		s.farPktSeq = p.InitPktSeq

		if s.maxPktSize > p.MaxPktSize {
			s.maxPktSize = p.MaxPktSize
		}
		if s.maxFlowWinSize > p.MaxFlowWinSize {
			s.maxFlowWinSize = p.MaxFlowWinSize
		}
		if s.farSockID != 0 {
			// we've received a sockID from the server, hopefully this means we've finished the handshake
			s.sockState = sockStateConnected
		} else {
			// handshake isn't done yet, send it back with the cookie we received
			err := s.sendHandshake(p.SynCookie)
			if err != nil {
				log.Printf("Socket handshake response failed: %s", err.Error())
				return false
			}
		}
		return true

	case sockStateConnected: // server repeating a handshake to a client
		if s.isServer {
			err := s.sendHandshake(p.SynCookie)
			if err != nil {
				log.Printf("Socket handshake response failed: %s", err.Error())
				return false
			}
		}
		return true
	}

	return false
}

func (s *udtSocket) readPacket(m *multiplexer, p packet.Packet, from *net.UDPAddr) {
	if s.sockState == sockStateClosed {
		return
	}

	switch sp := p.(type) {
	case *packet.HandshakePacket: // sent by both peers
		s.readHandshake(m, sp, from)
	case *packet.ShutdownPacket: // sent by either peer
		s.handleClose()
	case *packet.AckPacket: // receiver -> sender
	case *packet.NakPacket: // receiver -> sender
	case *packet.Ack2Packet: // sender -> receiver
	case *packet.MsgDropReqPacket: // sender -> receiver
	case *packet.DataPacket: // sender -> receiver
	}
}

// called by the multiplexer on every SYN
func (s *udtSocket) onReceiveTick(m *multiplexer, t time.Time) {
}
