/*
Package udt provides a pure Go implementation of the UDT protocol per
http://udt.sourceforge.net/doc/draft-gg-udt-03.txt.

udt does not implement all of the spec.  In particular, the following are not
implemented:

- Rendezvous mode
- STREAM mode (only UDP is supported)

*/
package udt

import (
	"context"
	"crypto/rand"
	"log"
	"math"
	"math/big"
	"net"
	"sync"
	"time"
)

/*
DialUDT establishes an outbound UDT connection using the supplied net, laddr and
raddr.  See function net.DialUDP for a description of net, laddr and raddr.
*/
func DialUDT(ctx context.Context, network string, laddr string, raddr *net.UDPAddr, isStream bool) (net.Conn, error) {
	m, err := multiplexerFor(ctx, network, laddr)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: raddr, Err: err}
	}

	s, err := m.newSocket(raddr, false, !isStream)
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: raddr, Err: err}
	}
	err = s.startConnect()
	if err != nil {
		return nil, &net.OpError{Op: "dial", Net: network, Source: nil, Addr: raddr, Err: err}
	}

	return s, nil
}

const (
	synTime time.Duration = 10000 // in microseconds
)

var (
	multiplexers sync.Map
	bigMaxUint32 *big.Int
)

func init() {
	bigMaxUint32 = big.NewInt(math.MaxUint32)
}

/*
randInt32 generates a secure random value between 0 and the max possible uint32
*/
func randUint32() (r uint32) {
	if _r, err := rand.Int(rand.Reader, bigMaxUint32); err != nil {
		log.Fatalf("Unable to generate random uint32: %s", err)
	} else {
		r = uint32(_r.Uint64())
	}
	return
}
