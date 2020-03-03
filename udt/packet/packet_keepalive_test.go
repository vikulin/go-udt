package packet

import (
	"testing"
)

func TestKeepAlivePacket(t *testing.T) {
	testPacket(
		&keepAlivePacket{
			h: header{
				ts:        100,
				dstSockID: 59,
			},
		}, t)
}
