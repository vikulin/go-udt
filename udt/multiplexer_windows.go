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
	"golang.org/x/sys/windows"
	"github.com/vikulin/go-udt/udt/packet"
)

const (
	// same for both IPv4 and IPv6 on Windows
	// https://microsoft.github.io/windows-docs-rs/doc/windows/Win32/Networking/WinSock/constant.IP_DONTFRAG.html
	// https://microsoft.github.io/windows-docs-rs/doc/windows/Win32/Networking/WinSock/constant.IPV6_DONTFRAG.html
	IP_DONTFRAGMENT = 14
	IPV6_DONTFRAG   = 14
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
			errIPv4 := windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IP, IP_DONTFRAGMENT, 1)
			errIPv6 := windows.SetsockoptInt(windows.Handle(fd), windows.IPPROTO_IPV6, IPV6_DONTFRAG, 1)
			if errIPv4 != nil {
				log.Printf("Error on setSockOpt: %s", errIPv4.Error())
			}
			if errIPv6 != nil {
				log.Printf("Error on setSockOpt IPv6: %s", errIPv6.Error())
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

