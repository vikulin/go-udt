package udt

import (
	"net"
	"time"
)

type Connection interface {

	Read(b []byte) (int, error)

	Write(b []byte) (int, error)
	
	LocalAddr() net.Addr

	RemoteAddr() net.Addr

	Close() error

	SetDeadline(t time.Time) error

	SetReadDeadline(t time.Time) error
	
	SetWriteDeadline(t time.Time) error
	
}

type Listener interface {

	// Accept waits for and returns the next connection to the listener.
	Accept() (net.Conn, error)

	// Close closes the listener.
	// Any blocked Accept operations will be unblocked and return errors.
	Close() error

	
	Addr() net.Addr
}
