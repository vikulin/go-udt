package main

import (
	"github.com/vikulin/go-udt/udt"
	"log"
	"net"
	"time"
)

func main() {
	if addr, err := net.ResolveUDPAddr("udp", "localhost:47008"); err != nil {
		log.Fatalf("Unable to resolve address: %s", err)
	} else {
		go server("localhost:47008")
		time.Sleep(200 * time.Millisecond)
		go client(addr)

		time.Sleep(50 * time.Second)
	}
}

func server(addr string) {
	if _, err := udt.ListenUDT("udp", addr); err != nil {
		log.Fatalf("Unable to listen: %s", err)
	}
}

func client(addr *net.UDPAddr) {
	if _, err := udt.DialUDT("udp", "0.0.0.0:0", addr, true); err != nil {
		log.Fatalf("Unable to dial: %s", err)
	}
}
