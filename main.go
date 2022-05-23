package main

import (
	"github.com/vikulin/go-udt/udt"
	"log"
	"net"
	"time"
	"flag"
)

func main() {
	startServer := flag.Bool("s", false, "server")
	startClient := flag.Bool("c", false, "client")
	host := flag.String("h", "localhost:48000", "host")
	flag.Parse()
	if *startServer {
		go server(*host)
	}
	if *startClient {
		if addr, err := net.ResolveUDPAddr("udp", *host); err != nil {
        	        log.Fatalf("Unable to resolve address: %s", err)
	        } else {
	                go client(addr)
        	}
	}
	time.Sleep(time.Hour)
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
