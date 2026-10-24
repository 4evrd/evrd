package client

import (
	"crypto/tls"
	"net"
	"testing"
)

func TestTuneClientConn(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close()
		}
	}()
	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	tuneClientConn(c)                  // plain TCP
	tuneClientConn(tls.Client(c, nil)) // TLS on TCP: tunes the TCP under it
	a, b := net.Pipe()                 // not TCP: left alone
	defer a.Close()
	defer b.Close()
	tuneClientConn(a)
}
