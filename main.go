package main

import (
	"github.com/vikulin/go-udt/udt"
	"io"
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
	l, err := udt.ListenUDT("udp", addr)
	if err != nil {
		log.Fatalf("Unable to listen: %s", err)
	} else {
		conn, err := l.Accept()
		if err != nil {
			log.Fatalf("Unable to accept: %s", err)
		} else {
			byteArr := make([]byte, 100)
			n, err := io.ReadFull(conn, byteArr)
			if err != nil {
				log.Fatalf("Unable to read: %s", err)
			} else {
				log.Printf("%s",string(byteArr[:n]))
			}
		}
	}
}

func client(addr *net.UDPAddr) {
	conn, err := udt.DialUDT("udp", "0.0.0.0:0", addr, true)
	if err != nil {
		log.Fatalf("Unable to dial: %s", err)
	} else {
		conn.Write([]byte("Hello!"))
	}
}
