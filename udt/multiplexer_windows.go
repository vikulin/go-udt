//go:build windows
// +build windows

package udt

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/vikulin/go-udt/udt/packet"
)

/*
multiplexerFor gets or creates a multiplexer for the given local address.  If a
new multiplexer is created, the given init function is run to obtain an
io.ReadWriter.
*/
func multiplexerFor(ctx context.Context, network string, laddr string) (*multiplexer, error) {
	key := fmt.Sprintf("%s:%s", network, laddr)
	if ifM, ok := multiplexers.Load(key); ok {
		m := ifM.(*multiplexer)
		if m.isLive() { // checking this in case we have a race condition with multiplexer destruction
			return m, nil
		}
	}

	// No multiplexer, need to create connection

	// try to avoid fragmentation (and hopefully be notified if we exceed path MTU)
	config := net.ListenConfig{}
	config.Control = func(network, address string, c syscall.RawConn) error {
		return c.Control(func(fd uintptr) {
			err := syscall.SetsockoptInt(syscall.Handle(fd), syscall.IPPROTO_IP, 71 /* IP_MTU_DISCOVER for winsock2 */, 2 /* IP_PMTUDISC_DO */)
			if err != nil {
				log.Printf("error on setSockOpt: %s", err.Error())
			}
		})
	}

	//conn, err := net.ListenUDP(network, laddr)
	conn, err := config.ListenPacket(ctx, network, laddr)
	if err != nil {
		return nil, err
	}

	addr := conn.LocalAddr().(*net.UDPAddr)

	m := newMultiplexer(network, addr, conn)
	multiplexers.Store(key, m)
	return m, nil
}

