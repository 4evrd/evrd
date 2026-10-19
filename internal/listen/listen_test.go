package listen

import (
	"net"
	"strings"
	"testing"
)

func TestAddr(t *testing.T) {
	for in, want := range map[string]string{
		"ircs://:6697":                  ":6697",
		"irc+insecure://127.0.0.1:6667": "127.0.0.1:6667",
		"wss://:8097/":                  ":8097",
		":8098":                         ":8098",
		"":                              "",
	} {
		if got := Addr(in); got != want {
			t.Errorf("Addr(%q) = %q, want %q", in, got, want)
		}
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

func TestPrebindHandsTheListenerOverOnce(t *testing.T) {
	addr := freeAddr(t)
	// Empty addresses are skipped; the same address twice binds once.
	if err := Prebind("", "irc://"+addr, addr); err != nil {
		t.Fatal(err)
	}
	ln, err := TCP("ircs://" + addr)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if ln.Addr().String() != addr {
		t.Fatalf("got %s", ln.Addr())
	}
	// It's handed over once: asking again binds anew, which fails while the
	// first is open.
	if again, err := TCP(addr); err == nil {
		again.Close()
		t.Fatal("the prebound listener was handed out twice")
	}
}

func TestTCPBindsWhenNotPrebound(t *testing.T) {
	ln, err := TCP("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
}

func TestPrebindReportsTheAddress(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	err = Prebind(busy.Addr().String())
	if err == nil || !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Fatalf("binding a busy port: %v", err)
	}
}
