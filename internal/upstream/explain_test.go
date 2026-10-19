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

func TestHint(t *testing.T) {
	for msg, want := range map[string]string{
		"Closing Link: 203.0.113.9 (K-Lined: spam)":                                 "refuses connections from this bouncer",
		"the server refused the connection: *** Banned: Please use another server.": "refuses connections from this bouncer",
		"Closing Link: host (Throttled: Reconnecting too fast)":                     "limits how often",
		"Too many connections from your host":                                       "limits how often",
		"Password incorrect":                                                        "password was refused",
		"the server didn't answer with TLS on this port — it said …":                "doesn't speak TLS",
		"the server speaks TLS on this port":                                        "expects TLS",
		"can't resolve irc.example.invalid":                                         "doesn't exist",
		"connection refused":                                                        "nothing listens on that port",
		"connection timed out":                                                      "didn't answer",
		"TLS certificate rejected":                                                  "certificate couldn't be verified",
	} {
		if got := Hint(msg); !strings.Contains(got, want) {
			t.Errorf("%q: hint %q, want it to mention %q", msg, got, want)
		}
	}
	if Hint("something nobody has seen") != "" {
		t.Error("an unknown failure got a hint")
	}
}

// sayAndClose is a server that answers every connection by reading the
// registration, sending lines, and hanging up.
func sayAndClose(t *testing.T, lines ...string) (string, int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					if strings.HasPrefix(sc.Text(), "USER ") {
						break
					}
				}
				for _, l := range lines {
					c.Write([]byte(l))
				}
			}()
		}
	}()
	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p, _ := strconv.Atoi(port)
	return host, p
}

type logRec struct {
	mu sync.Mutex
	l  []LogEntry
}

func (r *logRec) add(e LogEntry) { r.mu.Lock(); r.l = append(r.l, e); r.mu.Unlock() }
func (r *logRec) all() []LogEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]LogEntry(nil), r.l...)
}
func (r *logRec) has(from, sub string, bad bool) bool {
	for _, e := range r.all() {
		if e.From == from && strings.Contains(e.Text, sub) && e.Bad == bad {
			return true
		}
	}
	return false
}

func TestAServersRefusalIsExplainedAndLogged(t *testing.T) {
	host, port := sayAndClose(t,
		":irc.example.org NOTICE * :*** Looking up your hostname\r\n",
		"ERROR :Closing Link: 203.0.113.9 (K-Lined: no bouncers)\r\n")
	var rec logRec
	c := New(store.Network{ID: 31, Nick: "dora", Host: host, Port: port, Password: "nickserv-secret",
		Servers: []store.Server{{Host: host, Port: port, Password: "server-secret"}}}, quiet, Options{OnLog: rec.add})
	run(t, c)
	eventually(t, "the failure recorded", func() bool { return c.Health().Hint != "" })
	h := c.Health()
	if !strings.Contains(h.LastError, "K-Lined: no bouncers") || !strings.Contains(h.Hint, "refuses connections") {
		t.Fatalf("health: %q / %q", h.LastError, h.Hint)
	}
	for _, want := range []struct {
		from, sub string
		bad       bool
	}{
		{"evrd", "Connecting to " + host + " port " + strconv.Itoa(port) + " without TLS", false},
		{"evrd", "Signing in as dora", false},
		{"server", "NOTICE * :*** Looking up your hostname", false},
		{"server", "ERROR :Closing Link: 203.0.113.9 (K-Lined: no bouncers)", true},
		{"evrd", "Couldn't connect: Closing Link", true},
		{"evrd", "refuses connections from this bouncer", false},
		{"evrd", "Trying again in", false},
	} {
		if !rec.has(want.from, want.sub, want.bad) {
			t.Errorf("no %s entry %q (bad=%v) in %+v", want.from, want.sub, want.bad, rec.all())
		}
	}
	// nothing evrd sends is logged: no passwords
	for _, e := range rec.all() {
		if strings.Contains(e.Text, "secret") || strings.HasPrefix(e.Text, "PASS") || strings.HasPrefix(e.Text, "NICK") {
			t.Fatalf("logged what evrd sent: %q", e.Text)
		}
	}
}

func TestATLSPortReachedWithoutTLS(t *testing.T) {
	// a TLS server's alert, as it answers a plain-text "CAP LS"
	host, port := sayAndClose(t, "\x15\x03\x01\x00\x02\x02\x46\r\n")
	var rec logRec
	c := New(store.Network{ID: 32, Nick: "dora", Host: host, Port: port}, quiet, Options{OnLog: rec.add})
	run(t, c)
	eventually(t, "the failure recorded", func() bool { return c.Health().Hint != "" })
	if h := c.Health(); h.LastError != "the server speaks TLS on this port" || !strings.Contains(h.Hint, "Switch TLS on") {
		t.Fatalf("health: %q / %q", h.LastError, h.Hint)
	}
}
