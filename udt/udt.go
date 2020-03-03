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
	"crypto/rand"
	"log"
	"math"
	"math/big"
	"net"
	"sync"
)

/*
DialUDT establishes an outbound UDT connection using the supplied net, laddr and
raddr.  See function net.DialUDP for a description of net, laddr and raddr.
*/
func DialUDT(network string, laddr, raddr *net.UDPAddr, isStream bool) (net.Conn, error) {
	m, err := multiplexerFor(network, laddr)
	if err != nil {
		return nil, &net.OpError{Op: "listen", Net: network, Source: nil, Addr: laddr, Err: err}
	}

	s, err := m.newSocket(raddr, false)
	if err != nil {
		return nil, err
	}
	s.isDatagram = !isStream
	err = s.startConnect()

	return s, err
}

// Adapted from https://github.com/hlandau/degoutils/blob/master/net/mtu.go
const absMaxDatagramSize = 2147483646 // 2**31-2
func getMaxDatagramSize() int {
	var m int = 65535
	ifs, err := net.Interfaces()
	if err != nil {
		for i := range ifs {
			here := ifs[i]
			if here.Flags&(net.FlagUp|net.FlagLoopback) == net.FlagUp && here.MTU > m {
				m = ifs[i].MTU
			}
		}
	}
	if m > absMaxDatagramSize {
		m = absMaxDatagramSize
	}
	return m
}

const (
	syn_time = 10000 // in microseconds
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
