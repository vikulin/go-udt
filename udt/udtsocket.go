package udt

import (
	"container/heap"
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

type ackHistoryEntry struct {
	ackID    uint32
	sendTime time.Time
}

type recvDataEvent struct {
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
	m              *multiplexer // the multiplexer that handles this socket
	raddr          *net.UDPAddr // the remote address
	created        time.Time    // the time that this socket was created
	sockState      sockState
	udtVer         uint32
	pktSeq         uint32 // the current packet sequence number
	msgSeq         uint32 // the current message sequence number
	mtu            int    // the negotiated maximum packet size
	maxFlowWinSize uint32
	isDatagram     bool   // if true then we're sending and receiving datagrams, otherwise we're a streaming socket
	isServer       bool   // if true then we are behaving like a server, otherwise client (or rendezvous)
	sockID         uint32 // our sockID
	farSockID      uint32 // the peer's sockID
	farNextPktSeq  uint32 // the peer's next largest packet ID expected. Owned by goReceiveEvent->ingestData
	//farMsgSeq      uint32 // the peer's message sequence number. Owned by ????
	//	ackPeriod      uint32       // in microseconds
	//	nakPeriod      uint32       // in microseconds
	//	expPeriod      uint32       // in microseconds
	//	sndPeriod      uint32       // in microseconds
	messageIn  chan []byte // inbound messages. Owned by goReceiveEvent->ingestData
	messageOut chan []byte // outbound messages. Owned by client caller
	//	dataOut        *packetQueue // queue of outbound data packets
	currDp         []byte             // stream connections: currently reading message (for partial reads). Owned by client caller
	currDpOffset   int                // stream connections: offset in currDp (for partial reads). Owned by client caller
	recvLossList   *receiveLossHeap   // receiver: loss list. Owned by ?????
	recvPktPend    *dataPacketHeap    // receiver: list of packets that are waiting to be processed.  Owned by goReceiveEvent->ingestData
	ackHistory     []*ackHistoryEntry // receiver: list of sent ACKs. Owned by ?????
	pktHistory     []time.Time        // receiver: list of recently received packets. Owned by: readPacket->readData
	pktPairHistory []time.Duration    // receiver: probing packet window. Owned by: readPacket->readData
	expCount       int                // receiver: number of continuous EXP timeouts. Owned by: goReceiveEvent
	recvEvent      chan recvDataEvent // receiver: ingest the specified packet
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
	n = len(p)
	s.messageOut <- p
	/*
		state := packet.MbFirst
		for len(p) > s.mtu {
			dp := &packet.DataPacket{
				Seq:  s.pktSeq,
				Data: p[0 : s.mtu-1],
			}
			s.pktSeq++
			dp.SetMsg(state, true, s.msgSeq)
			s.dataOut.push(dp)
			state = packet.MbMiddle
			p = p[s.mtu:]
		}
		dp := &packet.DataPacket{
			Seq:  s.pktSeq,
			Data: p,
		}
		if state == packet.MbFirst {
			state = packet.MbOnly
		} else {
			state = packet.MbLast
		}
		s.pktSeq++
		dp.SetMsg(state, true, s.msgSeq)
		s.msgSeq++
		s.dataOut.push(dp)
	*/
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

	//close(s.messageIn)
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
		mtu:            m.mtu,
		maxFlowWinSize: 25600, // todo: turn tunable (minimum 32)
		isDatagram:     true,
		sockID:         sockID,
		//dataOut:        newPacketQueue(),
		messageIn:  make(chan []byte, 256),
		messageOut: make(chan []byte, 256),
		recvEvent:  make(chan recvDataEvent, 256),
	}
	go s.goReceiveEvent()

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
		UdtVer:         s.udtVer,
		SockType:       sockType,
		InitPktSeq:     s.pktSeq,
		MaxPktSize:     uint32(s.mtu),    // maximum packet size (including UDP/IP headers)
		MaxFlowWinSize: s.maxFlowWinSize, // maximum flow window size
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
		s.udtVer = p.UdtVer
		s.farSockID = p.SockID
		s.farNextPktSeq = p.InitPktSeq
		s.isDatagram = p.SockType == packet.TypeDGRAM

		if s.mtu > int(p.MaxPktSize) {
			s.mtu = int(p.MaxPktSize)
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
		s.farNextPktSeq = p.InitPktSeq

		if s.mtu > int(p.MaxPktSize) {
			s.mtu = int(p.MaxPktSize)
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
		if s.mtu > int(p.MaxPktSize) {
			s.mtu = int(p.MaxPktSize)
		}
		if s.maxFlowWinSize > p.MaxFlowWinSize {
			s.maxFlowWinSize = p.MaxFlowWinSize
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

	s.recvEvent <- recvDataEvent{pkt: p, now: now}

	switch sp := p.(type) {
	case *packet.HandshakePacket: // sent by both peers
		s.readHandshake(m, sp, from)
	case *packet.ShutdownPacket: // sent by either peer
		s.handleClose()
	//case *packet.AckPacket: // receiver -> sender
	//	s.readAck(m, sp, now)
	//case *packet.NakPacket: // receiver -> sender
	//	s.readNak(m, sp, now)
	//case *packet.Ack2Packet: // sender -> receiver
	//	s.readAck2(m, sp, now)
	//case *packet.MsgDropReqPacket: // sender -> receiver
	//	s.readMsgDropReq(m, sp, now)
	case *packet.DataPacket: // sender -> receiver
		s.readData(sp, now)
	}
}

func (s *udtSocket) goReceiveEvent() {
	recvEvent := s.recvEvent
	for {
		select {
		case evt, ok := <-recvEvent:
			if !ok {
				return
			}
			s.expCount = 1
			switch sp := evt.pkt.(type) {
			//case *packet.AckPacket: // receiver -> sender
			//	s.ingestAck(m, sp, now)
			//case *packet.NakPacket: // receiver -> sender
			//	s.ingestNak(m, sp, now)
			//case *packet.Ack2Packet: // sender -> receiver
			//	s.ingestAck2(m, sp, now)
			//case *packet.MsgDropReqPacket: // sender -> receiver
			//	s.ingestMsgDropReq(m, sp, now)
			case *packet.DataPacket: // sender -> receiver
				s.ingestData(sp)
			}
		}

	}
}

// owned by: multiplexer SYN loop
// called by the multiplexer on every SYN
func (s *udtSocket) onReceiveTick(m *multiplexer, t time.Time) {
	/* TODO: Query the system time to check if ACK, NAK, or EXP timer has
	expired. If there is any, process the event (as described below
	in this section) and reset the associated time variables. For
	ACK, also check the ACK packet interval. */
}

/*
ACK is used to trigger an acknowledgement (ACK). Its period is set by
   the congestion control module. However, UDT will send an ACK no
   longer than every 0.01 second, even though the congestion control
   does not need timer-based ACK. Here, 0.01 second is defined as the
   SYN time, or synchronization time, and it affects many of the other
   timers used in UDT.

   NAK is used to trigger a negative acknowledgement (NAK). Its period
   is dynamically updated to 4 * RTT_+ RTTVar + SYN, where RTTVar is the
   variance of RTT samples.

   EXP is used to trigger data packets retransmission and maintain
   connection status. Its period is dynamically updated to N * (4 * RTT
   + RTTVar + SYN), where N is the number of continuous timeouts. To
   avoid unnecessary timeout, a minimum threshold (e.g., 0.5 second)
   should be used in the implementation.
*/

// owned by: readPacket
// readData is called when a data packet has been received for this socket
func (s *udtSocket) readData(p *packet.DataPacket, now time.Time) {

	/* If the sequence number of the current data packet is 16n + 1,
	where n is an integer, record the time interval between this
	packet and the last data packet in the Packet Pair Window. */
	if (p.Seq-1)&0xf == 0 && s.pktHistory != nil {
		lastTime := s.pktHistory[len(s.pktHistory)-1]
		pairDist := now.Sub(lastTime)
		if s.pktPairHistory == nil {
			s.pktPairHistory = []time.Duration{pairDist}
		} else {
			s.pktPairHistory = append(s.pktPairHistory, pairDist)
			if len(s.pktPairHistory) > 16 {
				s.pktPairHistory = s.pktPairHistory[len(s.pktPairHistory)-16:]
			}
		}
	}

	// Record the packet arrival time in PKT History Window.
	if s.pktHistory == nil {
		s.pktHistory = []time.Time{now}
	} else {
		s.pktHistory = append(s.pktHistory, now)
		if len(s.pktHistory) > 16 {
			s.pktHistory = s.pktHistory[len(s.pktHistory)-16:]
		}
	}
}

// owned by: goReceiveEvent
// ingestData is called to process a data packet
func (s *udtSocket) ingestData(p *packet.DataPacket) {
	seq := p.Seq
	/* If the sequence number of the current data packet is greater
	than LRSN + 1, put all the sequence numbers between (but
	excluding) these two values into the receiver's loss list and
	send them to the sender in an NAK packet. */
	if seq > s.farNextPktSeq {
		newLoss := make(receiveLossHeap, 0, seq-s.farNextPktSeq)
		for idx := s.farNextPktSeq; idx < seq; idx++ {
			newLoss = append(newLoss, recvLossEntry{packetID: seq})
		}

		if s.recvLossList == nil {
			s.recvLossList = &newLoss
			heap.Init(s.recvLossList)
		} else {
			for idx := s.farNextPktSeq; idx < seq; idx++ {
				heap.Push(s.recvLossList, recvLossEntry{packetID: seq})
			}
		}

		s.sendNAK(newLoss)
		s.farNextPktSeq = seq + 1

	} else if seq < s.farNextPktSeq {
		// If the sequence number is less than LRSN, remove it from the receiver's loss list.
		if !s.recvLossList.Remove(seq) {
			return // already previously received packet -- ignore
		}

		if len(*s.recvLossList) == 0 {
			s.recvLossList = nil
		}
	}

	// can we process this packet?
	if s.recvLossList != nil && p.GetMsgOrderFlag() {
		minLostSeq := s.recvLossList.Min()
		if minLostSeq < seq {
			// we're required to order these packets and we're missing prior packets, so push and return
			if s.recvPktPend == nil {
				s.recvPktPend = &dataPacketHeap{p}
				heap.Init(s.recvPktPend)
			} else {
				heap.Push(s.recvPktPend, p)
			}
		}
	}

	// can we find the start of this message?
	boundary := p.GetMsgBoundary()
	msgID := p.GetMsg()
	pieces := make([]*packet.DataPacket, 0)
	cannotContinue := false
	switch boundary {
	case packet.MbLast, packet.MbMiddle:
		// we need prior packets, let's make sure we have them
		if s.recvPktPend != nil {
			pieceSeq := seq - 1
			for {
				prevPiece := s.recvPktPend.Find(pieceSeq)
				if prevPiece == nil {
					// we don't have the previous piece, is it missing?
					if s.recvLossList != nil {
						if lossEntry := s.recvLossList.Find(pieceSeq); lossEntry != nil {
							// it's missing, stop processing
							cannotContinue = true
						}
					}
					// in any case we can't continue with this
					log.Printf("Message with id %s appears to be a broken fragment", msgID)
					break
				}
				prevMsg := prevPiece.GetMsg()
				if prevMsg != msgID {
					// ...oops? previous piece isn't in the same message
					log.Printf("Message with id %s appears to be a broken fragment", msgID)
					break
				}
				prevBoundary := prevPiece.GetMsgBoundary()
				pieces = append([]*packet.DataPacket{prevPiece}, pieces...)
				if prevBoundary == packet.MbFirst {
					break
				}
				pieceSeq--
			}
		}
	}
	if !cannotContinue {
		pieces = append(pieces, p)

		switch boundary {
		case packet.MbFirst, packet.MbMiddle:
			// we need following packets, let's make sure we have them
			if s.recvPktPend != nil {
				pieceSeq := seq + 1
				for {
					nextPiece := s.recvPktPend.Find(pieceSeq)
					if nextPiece == nil {
						// we don't have the previous piece, is it missing?
						if pieceSeq >= s.farNextPktSeq {
							// hasn't been received yet
							cannotContinue = true
						} else if s.recvLossList != nil {
							if lossEntry := s.recvLossList.Find(pieceSeq); lossEntry != nil {
								// it's missing, stop processing
								cannotContinue = true
							}
						} else {
							log.Printf("Message with id %s appears to be a broken fragment", msgID)
						}
						// in any case we can't continue with this
						break
					}
					nextMsg := nextPiece.GetMsg()
					if nextMsg != msgID {
						// ...oops? previous piece isn't in the same message
						log.Printf("Message with id %s appears to be a broken fragment", msgID)
						break
					}
					prevBoundary := nextPiece.GetMsgBoundary()
					pieces = append(pieces, nextPiece)
					if prevBoundary == packet.MbLast {
						break
					}
				}
			}
		}
	}

	if cannotContinue {
		// we need to wait for more packets, store and return
		if s.recvPktPend == nil {
			s.recvPktPend = &dataPacketHeap{p}
			heap.Init(s.recvPktPend)
		} else {
			heap.Push(s.recvPktPend, p)
		}
		return
	}

	// we have a message, pull it from the pending heap (if necessary), assemble it into a message, and return it
	if s.recvPktPend != nil {
		for _, piece := range pieces {
			s.recvPktPend.Remove(piece.Seq)
		}
		if len(*s.recvPktPend) == 0 {
			s.recvPktPend = nil
		}
	}

	msg := make([]byte, 0)
	for _, piece := range pieces {
		msg = append(msg, piece.Data...)
	}
	s.messageIn <- msg
}

func (s *udtSocket) sendNAK(rl receiveLossHeap) {
	lossInfo := make([]uint32, 0)
	firstPkt := rl[0].packetID
	lastPkt := rl[0].packetID
	len := len(rl)
	for idx := 1; idx < len; idx++ {
		thisID := rl[idx].packetID
		if thisID == lastPkt+1 {
			lastPkt = thisID
		} else {
			if firstPkt == lastPkt {
				lossInfo = append(lossInfo, firstPkt&0x7FFF)
			} else {
				lossInfo = append(lossInfo, firstPkt|0x7FFF, lastPkt&0x7FFF)
			}
			firstPkt = thisID
			lastPkt = thisID
		}
	}
	if firstPkt == lastPkt {
		lossInfo = append(lossInfo, firstPkt&0x7FFF)
	} else {
		lossInfo = append(lossInfo, firstPkt|0x7FFF, lastPkt&0x7FFF)
	}

	err := s.sendPacket(&packet.NakPacket{
		CmpLossInfo: lossInfo,
	})
	if err != nil {
		log.Printf("Cannot send NAK: %s", err.Error())
	}
}
