package udt

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

const (
	serverPort = 9000
	clientPort = 9001
	totalNum   = 10000
)

type testFunc func(*testing.T, *sync.WaitGroup)

func Test1(t *testing.T) {
	t.Run("server", test1srv)
	t.Run("client", test1cli)
}

func test1srv(t *testing.T) {
	t.Parallel()
	t.Log("Testing simple data transfer.")

	serv, err := ListenUDT("udp", fmt.Sprintf("127.0.0.1:%d", serverPort))
	if err != nil {
		t.Errorf("error calling ListenUDT: %s", err.Error())
		return
	}

	newSock, err := serv.Accept()
	if err != nil {
		t.Errorf("error calling Accept: %s", err.Error())
		return
	}

	totalRecv := totalNum * 4
	off := 0
	buffer := make([]byte, totalRecv)
	for off < totalRecv {
		recvd, err := newSock.Read(buffer[off:])
		if err != nil {
			t.Errorf("error calling Read: %s", err.Error())
			return
		}
		off += recvd
	}

	// check data
	for i := 0; i < totalNum; i++ {
		val := endianness.Uint32(buffer[i*4 : i*4+4])
		if val != uint32(i) {
			t.Errorf("DATA ERROR %d %d", i, val)
			break
		}
	}

	newSock.Close()
}

func test1cli(t *testing.T) {
	t.Parallel()
	remoteAddr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("127.0.0.1:%d", serverPort))
	if err != nil {
		t.Errorf("error calling ResolveUDPAddr: %s", err.Error())
		return
	}

	client, err := DialUDT("udp", fmt.Sprintf("127.0.0.1:%d", clientPort), remoteAddr, true)
	if err != nil {
		t.Errorf("error calling DialUDT: %s", err.Error())
		return
	}

	totalSend := totalNum * 4
	buffer := make([]byte, totalSend)
	for i := 0; i < totalNum; i++ {
		endianness.PutUint32(buffer[i*4:i*4+4], uint32(i))
	}

	sent, err := client.Write(buffer)
	if err != nil {
		t.Errorf("error calling Write: %s", err.Error())
		return
	}
	if sent != totalSend {
		t.Errorf("asked to send %d, actually sent %d", totalSend, sent)
		return
	}

	client.Close()
}
