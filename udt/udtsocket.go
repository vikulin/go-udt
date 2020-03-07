package udt

import (
	"errors"
	"log"
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

type recvPktEvent struct {
	pkt packet.Packet
	now time.Time
}

/*
type CongestionControlParms interface {
	GetRTT() time.Duration
	GetMTU() int
	GetEstBandwidth() int
	GetLastSentPktID() uint32
	GetPktArrivalRate() int
	SetAckInterval(numPkts int)
	SetAckTimer(t time.Duration)
	SetRTO(int)
}

type CongestionControl interface {
	Init(CongestionControlParms)
	Close(CongestionControlParms)
	OnACK(CongestionControlParms)
	OnNAK(CongestionControlParms)
	OnTimeout(CongestionControlParms)
	OnPktSent(CongestionControlParms)
	OnPktRecv(CongestionControlParms)
}
*/
type sendState int

const (
	sendStateIdle        sendState = iota // not waiting for anything, can send immediately
	sendStateSending                      // recently sent something, waiting for SND before sending more
	sendStateWaiting                      // destination is full, waiting for them to process something and come back
	sendStateProcessDrop                  // immediately re-process any drop list requests
)

/*
udtSocket encapsulates a UDT socket between a local and remote address pair, as
defined by the UDT specification.  udtSocket implements the net.Conn interface
so that it can be used anywhere that a stream-oriented network connection
(like TCP) would be used.
*/
type udtSocket struct {
	// this data not changed after the socket is initialized and/or handshaked
	m          *multiplexer // the multiplexer that handles this socket
	raddr      *net.UDPAddr // the remote address
	created    time.Time    // the time that this socket was created
	udtVer     int          // UDT protcol version (normally 4.  Will we be supporting others?)
	isDatagram bool         // if true then we're sending and receiving datagrams, otherwise we're a streaming socket
	isServer   bool         // if true then we are behaving like a server, otherwise client (or rendezvous). Only useful during handshake
	sockID     uint32       // our sockID
	farSockID  uint32       // the peer's sockID

	sockState      sockState // socket state - used mostly during handshakes
	mtu            int       // the negotiated maximum packet size
	maxFlowWinSize uint      // receiver: maximum unacknowledged packet count
	//rtoPeriod      time.Duration // set by congestion control, standardized on 4 * RTT + RTTVar
	//	ackPeriod      time.Duration       // receiver: used to (re-)send an ACK. Set by the congestion control module, never greater than 0.01s
	//	nakPeriod      time.Duration       // receiver: used to (re-)send a NAK. 4 * RTT + RTTVar + 0.01s
	//	expPeriod      time.Duration       // sender: expCount * (4 * RTT + RTTVar + 0.01s)
	sndPeriod       time.Duration // sender: delay between sending packets.  Owned by congestion control, read by sendDataPacket
	currPartialRead []byte        // stream connections: currently reading message (for partial reads). Owned by client caller (Read)
	rtt             uint32        // receiver: estimated roundtrip time (microseconds). ***TODO -- is this updated by the sender???
	rttVar          uint32        // receiver: roundtrip variance (in microseconds). ***TODO -- is this updated by the sender???

	// channels
	messageIn  chan []byte       // inbound messages. Sender is goReceiveEvent->ingestData, Receiver is client caller (Read)
	messageOut chan []byte       // outbound messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	recvEvent  chan recvPktEvent // receiver: ingest the specified packet. Sender is readPacket, receiver is goReceiveEvent
	sendEvent  chan recvPktEvent // sender: ingest the specified packet. Sender is readPacket, receiver is goSendEvent
	closed     chan struct{}     // closed when socket is closed

	// receiver-owned data -- to only be changed by goReceiveEvent or functions it calls
	farNextPktSeq uint32          // the peer's next largest packet ID expected.
	largestACK    uint32          // receiver: largest ACK packet we've sent that has been acknowledged (by an ACK2).
	recvPktPend   dataPacketHeap  // receiver: list of packets that are waiting to be processed.
	recvLossList  receiveLossHeap // receiver: loss list. Owned by ????? (at minimum goReceiveEvent->ingestData, goReceiveEvent->ingestMsgDropReq)
	ackHistory    ackHistoryHeap  // receiver: list of sent ACKs. Owned by ???? (at minimum goReceiveEvent->ingestAck2)

	// receiver-owned data -- to only be changed by readPacket or functions it calls
	recvPktHistory     []time.Time     // receiver: list of recently received packets.
	recvPktPairHistory []time.Duration // receiver: probing packet window.

	// sender-owned data -- to only be changed by goSendEvent or functions it calls
	sendState      sendState        // sender: current state
	sendPktPend    dataPacketHeap   // sender: list of packets that have been sent but not yet acknoledged
	pktSeq         uint32           // sender: the current packet sequence number
	msgPartialSend []byte           // sender: when a message can only partially fit in a socket, this is the remainder
	msgSeq         uint32           // sender: the current message sequence number
	farFlowWinSize uint             // sender: the estimated peer available window size
	expCount       int              // sender: number of continuous EXP timeouts.
	expResetCount  time.Time        // sender: the last time expCount was set to 1
	sendLossList   sendLossHeap     // sender: loss list. Owned by ????? (at minimum goSendEvent->processSendLoss, goSendEvent->ingestNak)
	sndEvent       <-chan time.Time // sender: if a packet is recently sent, this timer fires when SND completes
}

/*******************************************************************************
 Implementation of net.Conn interface
*******************************************************************************/

func (s *udtSocket) Read(p []byte) (n int, err error) {
	if s.isDatagram {
		// for datagram sockets, block until we have a message to return and then return it
		// if the buffer isn't big enough, return a truncated message (discarding the rest) and return an error
		msg := <-s.messageIn
		n = copy(p, msg)
		if n < len(msg) {
			err = errors.New("Message truncated")
		}
	} else {
		// for streaming sockets, block until we have at least something to return, then
		// fill up the passed buffer as far as we can without blocking again
		idx := 0
		l := len(p)
		n = 0
		for idx < l {
			if s.currPartialRead == nil {
				// Grab the next data packet
				if n == 0 {
					s.currPartialRead = <-s.messageIn
				} else {
					select {
					case s.currPartialRead = <-s.messageIn:
						// ok we have a message
					default:
						// ok we've read some stuff and there's nothing immediately available
						return
					}
				}
			}
			thisN := copy(p[idx:], s.currPartialRead)
			n = n + thisN
			idx = idx + thisN
			if n >= len(s.currPartialRead) {
				// we've exhausted the current data packet, reset to nil
				s.currPartialRead = nil
			} else {
				s.currPartialRead = s.currPartialRead[n:]
			}
		}
	}
	return
}

func (s *udtSocket) Write(p []byte) (n int, err error) {
	// at the moment whatever we have right now we'll shove it into a channel and return
	// on the other side:
	//  for datagram sockets: this is a distinct message to be broken into as few packets as possible
	//  for streaming sockets: collect as much as can fit into a packet and send them out
	n = len(p)
	s.messageOut <- p
	return
}

func (s *udtSocket) Close() error {
	if s.sockState == sockStateClosed {
		return nil // already closed
	}

	// send shutdown packet
	err := s.sendPacket(&packet.ShutdownPacket{})
	if err != nil {
		return err
	}

	s.handleClose()
	close(s.messageOut)
	return nil
}

func (s *udtSocket) handleClose() (err error) {
	// Remove from connected socket list
	s.sockState = sockStateClosed
	s.m.closeSocket(s.sockID)

	close(s.closed)
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
	return nil
}

func (s *udtSocket) SetReadDeadline(t time.Time) error {
	// todo set timeout through EXP
	return nil
}

func (s *udtSocket) SetWriteDeadline(t time.Time) error {
	// todo set timeout through EXP or SND
	return nil
}

/*******************************************************************************
 Private functions
*******************************************************************************/

// newSocket creates a new UDT socket, which will be configured afterwards as either an incoming our outgoing socket
func newSocket(m *multiplexer, sockID uint32, isServer bool, raddr *net.UDPAddr) (s *udtSocket, err error) {
	now := time.Now()
	s = &udtSocket{
		m:              m,
		raddr:          raddr,
		created:        now,
		expResetCount:  now,
		sockState:      sockStateInit,
		udtVer:         4,
		isServer:       isServer,
		pktSeq:         randUint32(),
		mtu:            m.mtu,
		maxFlowWinSize: 25600, // todo: turn tunable (minimum 32)
		isDatagram:     true,
		sockID:         sockID,
		messageIn:      make(chan []byte, 256),
		messageOut:     make(chan []byte, 256),
		recvEvent:      make(chan recvPktEvent, 256),
		sendEvent:      make(chan recvPktEvent, 256),
		closed:         make(chan struct{}, 1),
	}
	go s.goReceiveEvent()
	go s.goSendEvent()

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

	return s.sendPacket(&packet.HandshakePacket{
		UdtVer:         uint32(s.udtVer),
		SockType:       sockType,
		InitPktSeq:     s.pktSeq,
		MaxPktSize:     uint32(s.mtu),            // maximum packet size (including UDP/IP headers)
		MaxFlowWinSize: uint32(s.maxFlowWinSize), // maximum flow window size
		ReqType:        1,
		SockID:         s.sockID,
		SynCookie:      synCookie,
		SockAddr:       s.raddr.IP,
	})
}

func (s *udtSocket) sendPacket(p packet.Packet) error {
	ts := uint32(time.Now().Sub(s.created) / time.Microsecond)
	return s.m.sendPacket(s.raddr, s.farSockID, ts, p)
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
		s.udtVer = int(p.UdtVer)
		s.farSockID = p.SockID
		s.farNextPktSeq = p.InitPktSeq
		s.isDatagram = p.SockType == packet.TypeDGRAM
		s.farFlowWinSize = uint(p.MaxFlowWinSize)

		if s.mtu > int(p.MaxPktSize) {
			s.mtu = int(p.MaxPktSize)
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
		s.farNextPktSeq = p.InitPktSeq
		s.farFlowWinSize = uint(p.MaxFlowWinSize)

		if s.mtu > int(p.MaxPktSize) {
			s.mtu = int(p.MaxPktSize)
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
		if s.mtu > int(p.MaxPktSize) {
			s.mtu = int(p.MaxPktSize)
		}
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

// owned by: multiplexer read loop
// called by the multiplexer read loop when a packet is received for this socket.
// Minimal processing is permitted but try not to stall the caller
func (s *udtSocket) readPacket(m *multiplexer, p packet.Packet, from *net.UDPAddr) {
	now := time.Now()
	if s.sockState == sockStateClosed {
		return
	}
	if from != s.raddr {
		log.Printf("Socket connected to %s received a packet from %s? Discarded", s.raddr.String(), from.String())
		return
	}

	s.recvEvent <- recvPktEvent{pkt: p, now: now}

	switch sp := p.(type) {
	case *packet.HandshakePacket: // sent by both peers
		s.readHandshake(m, sp, from)
	case *packet.ShutdownPacket: // sent by either peer
		s.handleClose()
	case *packet.AckPacket, *packet.NakPacket: // receiver -> sender
		s.sendEvent <- recvPktEvent{pkt: p, now: now}
	case *packet.DataPacket: // sender -> receiver
		s.readData(sp, now)
	}
}
