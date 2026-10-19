package upstream

import (
	"bufio"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// authServer is an IRC server that offers SASL or not, and records what it's
// sent.
type authServer struct {
	mu    sync.Mutex
	lines []string
}

func (s *authServer) got() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.lines...)
}

func startAuthServer(t *testing.T, sasl bool) (*authServer, string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &authServer{}
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		send := func(l string) { c.Write([]byte(l + "\r\n")) }
		nick := "*"
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			l := sc.Text()
			s.mu.Lock()
			s.lines = append(s.lines, l)
			s.mu.Unlock()
			f := strings.Fields(l)
			if len(f) == 0 {
				continue
			}
			switch strings.ToUpper(f[0]) {
			case "NICK":
				nick = f[1]
			case "CAP":
				switch {
				case len(f) > 1 && f[1] == "LS":
					if sasl {
						send(":srv CAP * LS :sasl")
					} else {
						send(":srv CAP * LS :multi-prefix")
					}
				case len(f) > 1 && f[1] == "REQ":
					send(":srv CAP * ACK :sasl")
				case len(f) > 1 && f[1] == "END":
					send(":srv 001 " + nick + " :Welcome")
				}
			case "AUTHENTICATE":
				if f[1] == "PLAIN" {
					send("AUTHENTICATE +")
				} else {
					send(":srv 903 " + nick + " :SASL authentication successful")
				}
			case "PING":
				send("PONG " + strings.Join(f[1:], " "))
			}
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return s, host, p
}

func TestNickServIdentifyWithoutSASL(t *testing.T) {
	srv, host, port := startAuthServer(t, false)
	c := New(store.Network{ID: 21, Nick: "thelion", Host: host, Port: port, Password: "s3cret", Channels: []string{"#home"}}, quiet, Options{})
	run(t, c)
	eventually(t, "joined", func() bool {
		for _, l := range srv.got() {
			if strings.HasPrefix(l, "JOIN") {
				return true
			}
		}
		return false
	})
	identify, join := -1, -1
	for i, l := range srv.got() {
		switch {
		case l == "PRIVMSG NickServ :IDENTIFY s3cret":
			identify = i
		case strings.HasPrefix(l, "JOIN") && join < 0:
			join = i
		}
	}
	if identify < 0 || identify > join {
		t.Fatalf("want IDENTIFY before JOIN; got %q", srv.got())
	}
}

func TestNickServIdentifyUsesTheAccountName(t *testing.T) {
	n := store.Network{Nick: "thelion_", SASLUser: "thelion", Password: "pw"}
	if got := identifyLine(n); got != "PRIVMSG NickServ :IDENTIFY thelion pw" {
		t.Fatalf("%q", got)
	}
	n.SASLUser = "TheLion_"
	if got := identifyLine(n); got != "PRIVMSG NickServ :IDENTIFY pw" {
		t.Fatalf("%q", got)
	}
}

func TestNoNickServIdentifyAfterSASL(t *testing.T) {
	srv, host, port := startAuthServer(t, true)
	c := New(store.Network{ID: 22, Nick: "thelion", Host: host, Port: port, Password: "s3cret", Channels: []string{"#home"}}, quiet, Options{})
	run(t, c)
	eventually(t, "joined", func() bool {
		for _, l := range srv.got() {
			if strings.HasPrefix(l, "JOIN") {
				return true
			}
		}
		return false
	})
	sawSASL := false
	for _, l := range srv.got() {
		if strings.HasPrefix(l, "AUTHENTICATE ") && l != "AUTHENTICATE PLAIN" {
			sawSASL = true
		}
		if strings.Contains(l, "NickServ") {
			t.Fatalf("identified again after SASL: %q", srv.got())
		}
	}
	if !sawSASL {
		t.Fatalf("no SASL: %q", srv.got())
	}
}
