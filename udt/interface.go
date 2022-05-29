package udt

type Connection interface {

	Read(b []byte) (int, error)

	Write(b []byte) (int, error)
	
	LocalAddr() net.Addr

	RemoteAddr() net.Addr

	Close()

	SetDeadline(t time.Time)

	SetReadDeadline(t time.Time)
	
	SetWriteDeadline(t time.Time)
	
}
