//go:build linux || netbsd || freebsd || openbsd || dragonflybsd || darwin || ios
// +build linux netbsd freebsd openbsd dragonflybsd darwin ios

package udt

import (
	"context"
	"fmt"
	"log"
	"net"
	"syscall"
	"golang.org/x/sys/unix"
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
			err := syscall.SetsockoptInt(int(fd), unix.IPPROTO_IP, unix.IP_MTU_DISCOVER, unix.IP_PMTUDISC_DO)
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

