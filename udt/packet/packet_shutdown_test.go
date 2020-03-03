package packet

import (
	"testing"
)

func TestShutdownPacket(t *testing.T) {
	testPacket(
		&shutdownPacket{
			h: header{
				ts:        100,
				dstSockID: 59,
			},
		}, t)
}
