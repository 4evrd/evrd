package upstream

import (
	"bufio"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

// scripted is a fake IRC server whose answers a test chooses. Every
// connection's lines are recorded, and the test can send to the newest one.
type scripted struct {
	t     *testing.T
	ln    net.Listener
	reply func(p *peer, line string)

	mu    sync.Mutex
	peers []*peer
}

// peer is one client connection to a scripted server.
type peer struct {
	c    net.Conn
	n    int // which connection (0 = the first)
	nick string
	// ended: CAP END came while the nick was taken (services_test.go)
	ended bool

	mu       sync.Mutex
	lines    []string
	welcomed string // the nick once registered (services_test.go)
}

// me is the client's nick since registering ("" before).
func (p *peer) me() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.welcomed
}

func (p *peer) registered() bool { return p.me() != "" }

func (p *peer) send(format string, a ...any) {
	fmt.Fprintf(p.c, format+"\r\n", a...)
}

func (p *peer) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.lines...)
}

// startScripted runs a server answering each received line with reply (nil:
// register like a plain server, see welcome).
func startScripted(t *testing.T, reply func(p *peer, line string)) *scripted {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &scripted{t: t, ln: ln, reply: reply}
	if s.reply == nil {
		s.reply = welcome
	}
	t.Cleanup(func() {
		ln.Close()
		s.mu.Lock()
		for _, p := range s.peers {
			p.c.Close()
		}
		s.mu.Unlock()
	})
	go s.serve(func(c net.Conn) net.Conn { return c })
	return s
}

func (s *scripted) serve(wrap func(net.Conn) net.Conn) {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		c = wrap(c)
		s.mu.Lock()
		p := &peer{c: c, n: len(s.peers), nick: "*"}
		s.peers = append(s.peers, p)
		s.mu.Unlock()
		go func() {
			defer c.Close()
			sc := bufio.NewScanner(c)
			for sc.Scan() {
				l := sc.Text()
				p.mu.Lock()
				p.lines = append(p.lines, l)
				p.mu.Unlock()
				if f := strings.Fields(l); len(f) > 1 && f[0] == "NICK" {
					p.nick = f[1]
				}
				s.reply(p, l)
			}
		}()
	}
}

func (s *scripted) hostPort() (string, int) {
	host, port, _ := net.SplitHostPort(s.ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return host, p
}

func (s *scripted) network(n store.Network) store.Network {
	n.Host, n.Port = s.hostPort()
	return n
}

// last is the newest connection (nil before the first).
func (s *scripted) last() *peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.peers) == 0 {
		return nil
	}
	return s.peers[len(s.peers)-1]
}

func (s *scripted) conns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

// send sends a line on the newest connection.
func (s *scripted) send(format string, a ...any) { s.last().send(format, a...) }

// has reports whether any connection received a line equal to l.
func (s *scripted) has(l string) bool {
	s.mu.Lock()
	peers := append([]*peer(nil), s.peers...)
	s.mu.Unlock()
	for _, p := range peers {
		for _, x := range p.got() {
			if x == l {
				return true
			}
		}
	}
	return false
}

// count is how many received lines start with prefix.
func (s *scripted) count(prefix string) int {
	s.mu.Lock()
	peers := append([]*peer(nil), s.peers...)
	s.mu.Unlock()
	n := 0
	for _, p := range peers {
		for _, x := range p.got() {
			if strings.HasPrefix(x, prefix) {
				n++
			}
		}
	}
	return n
}

// welcome registers a client without capabilities: an empty CAP LS, and
// 001 once USER arrives.
func welcome(p *peer, l string) {
	switch {
	case strings.HasPrefix(l, "CAP LS"):
		p.send(":srv CAP * LS :")
	case strings.HasPrefix(l, "USER "):
		p.send(":srv 001 %s :Welcome", p.nick)
	case strings.HasPrefix(l, "PING "):
		p.send(":srv PONG srv %s", strings.TrimPrefix(l, "PING "))
	}
}

// index is the position of the first line starting with prefix, or -1.
func index(lines []string, prefix string) int {
	for i, l := range lines {
		if strings.HasPrefix(l, prefix) {
			return i
		}
	}
	return -1
}
