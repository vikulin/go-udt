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
	sockStateInit       sockState = iota // object is being constructed
	sockStateConnecting                  // attempting to create a connection
	sockStateConnected                   // connection is established
	sockStateClosed                      // connection has been closed (by either end)
	sockStateRefused                     // connection rejected by remote host
	sockStateCorrupted                   // peer behaved in an improper manner
)

type recvPktEvent struct {
	pkt packet.Packet
	now time.Time
}

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
	Config     *Config      // configuration parameters for this socket
	udtVer     int          // UDT protcol version (normally 4.  Will we be supporting others?)
	isDatagram bool         // if true then we're sending and receiving datagrams, otherwise we're a streaming socket
	isServer   bool         // if true then we are behaving like a server, otherwise client (or rendezvous). Only useful during handshake
	sockID     uint32       // our sockID
	farSockID  uint32       // the peer's sockID

	sockState      sockState // socket state - used mostly during handshakes
	mtu            uint      // the negotiated maximum packet size
	maxFlowWinSize uint      // receiver: maximum unacknowledged packet count
	//rtoPeriod      time.Duration // set by congestion control, standardized on 4 * RTT + RTTVar
	//	ackPeriod      time.Duration       // receiver: used to (re-)send an ACK. Set by the congestion control module, never greater than 0.01s
	//	nakPeriod      time.Duration       // receiver: used to (re-)send a NAK. 4 * RTT + RTTVar + 0.01s
	//	expPeriod      time.Duration       // sender: expCount * (4 * RTT + RTTVar + 0.01s)
	sndPeriod       time.Duration // sender: delay between sending packets.  Owned by congestion control, read by sendDataPacket
	currPartialRead []byte        // stream connections: currently reading message (for partial reads). Owned by client caller (Read)
	rtt             time.Duration // receiver: estimated roundtrip time. ***TODO -- is this updated by the sender???
	rttVar          time.Duration // receiver: roundtrip variance. ***TODO -- is this updated by the sender???

	// channels
	messageIn  <-chan []byte       // inbound messages. Sender is goReceiveEvent->ingestData, Receiver is client caller (Read)
	messageOut chan<- []byte       // outbound messages. Sender is client caller (Write), Receiver is goSendEvent. Closed when socket is closed
	recvEvent  chan<- recvPktEvent // receiver: ingest the specified packet. Sender is readPacket, receiver is goReceiveEvent
	sendEvent  chan<- recvPktEvent // sender: ingest the specified packet. Sender is readPacket, receiver is goSendEvent
	closed     chan<- struct{}     // closed when socket is closed

	send *udtSocketSend // reference to sending side of this socket
	recv *udtSocketRecv // reference to receiving side of this socket
	cong *udtSocketCc   // reference to contestion control

	// performance metrics
	//PktSent      uint64        // number of sent data packets, including retransmissions
	//PktRecv      uint64        // number of received packets
	//PktSndLoss   uint          // number of lost packets (sender side)
	//PktRcvLoss   uint          // number of lost packets (receiver side)
	//PktRetrans   uint          // number of retransmitted packets
	//PktSentACK   uint          // number of sent ACK packets
	//PktRecvACK   uint          // number of received ACK packets
	//PktSentNAK   uint          // number of sent NAK packets
	//PktRecvNAK   uint          // number of received NAK packets
	//MbpsSendRate float64       // sending rate in Mb/s
	//MbpsRecvRate float64       // receiving rate in Mb/s
	//SndDuration  time.Duration // busy sending time (i.e., idle time exclusive)

	// instant measurements
	//PktSndPeriod        time.Duration // packet sending period
	//PktFlowWindow       uint          // flow window size, in number of packets
	//PktCongestionWindow uint          // congestion window size, in number of packets
	//PktFlightSize       uint          // number of packets on flight
	//MsRTT               time.Duration // RTT
	//MbpsBandwidth       float64       // estimated bandwidth, in Mb/s
	//ByteAvailSndBuf     uint          // available UDT sender buffer size
	//ByteAvailRcvBuf     uint          // available UDT receiver buffer size
}

/*******************************************************************************
 Implementation of net.Conn interface
*******************************************************************************/

// Grab the next data packet
func (s *udtSocket) fetchReadPacket(blocking bool) []byte {
	var result []byte
	if blocking {
		result = <-s.messageIn
		return result
	}

	select {
	case result = <-s.messageIn:
		// ok we have a message
	default:
		// ok we've read some stuff and there's nothing immediately available
		return nil
	}
	return result
}

// TODO: int sendmsg(const char* data, int len, int msttl, bool inorder)
func (s *udtSocket) Read(p []byte) (n int, err error) {
	switch s.sockState {
	case sockStateRefused:
		err = errors.New("Connection refused by remote host")
		return
	case sockStateCorrupted:
		err = errors.New("Connection closed due to protocol error")
		return
	}
	if s.isDatagram {
		// for datagram sockets, block until we have a message to return and then return it
		// if the buffer isn't big enough, return a truncated message (discarding the rest) and return an error
		msg := s.fetchReadPacket(s.sockState != sockStateClosed)
		if msg == nil && s.sockState == sockStateClosed {
			err = errors.New("Connection closed")
			return
		}
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
				s.currPartialRead = s.fetchReadPacket(n == 0 && s.sockState != sockStateClosed)
				if s.currPartialRead == nil {
					if n != 0 {
						return
					}
					if s.sockState == sockStateClosed {
						err = errors.New("Connection closed")
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
	switch s.sockState {
	case sockStateRefused:
		err = errors.New("Connection refused by remote host")
		return
	case sockStateCorrupted:
		err = errors.New("Connection closed due to protocol error")
		return
	case sockStateClosed:
		err = errors.New("Connection closed")
		return
	}

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

	s.cong.close()

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
func newSocket(m *multiplexer, config *Config, sockID uint32, isServer bool, isDatagram bool, raddr *net.UDPAddr) (s *udtSocket) {
	now := time.Now()

	closed := make(chan struct{}, 1)
	recvEvent := make(chan recvPktEvent, 256)
	sendEvent := make(chan recvPktEvent, 256)
	messageIn := make(chan []byte, 256)
	messageOut := make(chan []byte, 256)

	s = &udtSocket{
		m:              m,
		Config:         config,
		raddr:          raddr,
		created:        now,
		sockState:      sockStateInit,
		udtVer:         4,
		isServer:       isServer,
		mtu:            m.mtu,
		maxFlowWinSize: 25600, // todo: turn tunable (minimum 32)
		isDatagram:     isDatagram,
		sockID:         sockID,
		messageIn:      messageIn,
		messageOut:     messageOut,
		recvEvent:      recvEvent,
		sendEvent:      sendEvent,
		closed:         closed,
	}
	s.send = newUdtSocketSend(s, closed, sendEvent, messageOut)
	s.recv = newUdtSocketRecv(s, closed, recvEvent, messageIn)
	s.cong = newUdtSocketCc(s, closed)

	return
}

func (s *udtSocket) startConnect() error {
	s.cong.init()
	s.sockState = sockStateConnecting
	return s.sendHandshake(0, packet.HsRequest)
}

func (s *udtSocket) sendHandshake(synCookie uint32, reqType packet.HandshakeReqType) error {
	sockType := packet.TypeSTREAM
	if s.isDatagram {
		sockType = packet.TypeDGRAM
	}

	return s.sendPacket(&packet.HandshakePacket{
		UdtVer:         uint32(s.udtVer),
		SockType:       sockType,
		InitPktSeq:     s.send.sendPktSeq,
		MaxPktSize:     uint32(s.mtu),            // maximum packet size (including UDP/IP headers)
		MaxFlowWinSize: uint32(s.maxFlowWinSize), // maximum flow window size
		ReqType:        reqType,
		SockID:         s.sockID,
		SynCookie:      synCookie,
		SockAddr:       s.raddr.IP,
	})
}

func (s *udtSocket) sendPacket(p packet.Packet) error {
	ts := uint32(time.Now().Sub(s.created) / time.Microsecond)
	s.cong.onPktSent(p)
	return s.m.sendPacket(s.raddr, s.farSockID, ts, p)
}

// checkValidHandshake checks to see if we want to accept a new connection with this handshake.
func (s *udtSocket) checkValidHandshake(m *multiplexer, p *packet.HandshakePacket, from *net.UDPAddr) bool {
	if s.udtVer != 4 {
		return false
	}
	return true
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
		s.cong.init()
		s.udtVer = int(p.UdtVer)
		s.farSockID = p.SockID
		s.isDatagram = p.SockType == packet.TypeDGRAM

		if s.mtu > uint(p.MaxPktSize) {
			s.mtu = uint(p.MaxPktSize)
		}
		s.recv.configureHandshake(p)
		s.send.configureHandshake(p)
		s.sockState = sockStateConnected

		err := s.sendHandshake(p.SynCookie, packet.HsResponse)
		if err != nil {
			log.Printf("Socket handshake response failed: %s", err.Error())
			return false
		}
		return true

	case sockStateConnecting: // client attempting to connect to server
		if p.ReqType == packet.HsRefused {
			s.sockState = sockStateRefused
			return true
		}
		if p.ReqType != packet.HsResponse {
			return true // not a response packet, ignore
		}
		if p.InitPktSeq != s.send.sendPktSeq {
			s.sockState = sockStateCorrupted
			return true
		}
		s.farSockID = p.SockID

		if s.mtu > uint(p.MaxPktSize) {
			s.mtu = uint(p.MaxPktSize)
		}
		s.recv.configureHandshake(p)
		s.send.configureHandshake(p)
		if s.farSockID != 0 {
			// we've received a sockID from the server, hopefully this means we've finished the handshake
			s.sockState = sockStateConnected
		} else {
			// handshake isn't done yet, send it back with the cookie we received
			err := s.sendHandshake(p.SynCookie, packet.HsRequest)
			if err != nil {
				log.Printf("Socket handshake response failed: %s", err.Error())
				return false
			}
		}
		return true

	case sockStateConnected: // server repeating a handshake to a client
		if s.isServer && p.ReqType == packet.HsRequest {
			err := s.sendHandshake(p.SynCookie, packet.HsResponse)
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
	case *packet.AckPacket, *packet.LightAckPacket, *packet.NakPacket: // receiver -> sender
		s.sendEvent <- recvPktEvent{pkt: p, now: now}
	case *packet.UserDefControlPacket:
		s.cong.onCustomMsg(*sp)
	}
}
