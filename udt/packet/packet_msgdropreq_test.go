package packet

import (
	"testing"
)

func TestMsgDropReqPacket(t *testing.T) {
	testPacket(
		&msgDropReqPacket{
			h: header{
				ts:        100,
				dstSockID: 59,
			},
			msgID:    90,
			firstSeq: 91,
			lastSeq:  92,
		}, t)
}
