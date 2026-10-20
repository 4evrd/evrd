// Package testirc is a fake IRC server for tests.
package testirc

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

// Start runs a minimal IRC server until the test ends and returns its
// address. It registers clients, echoes JOIN, PART and NICK back the way a
// real server confirms them, and sends "SIMULATE <line>" back verbatim.
func Start(t testing.TB) string {
	t.Helper()
	return StartLogged(t, nil)
}

// Log records the lines a fake server received.
type Log struct {
	mu    sync.Mutex
	lines []string
	conns []net.Conn
}

// Send has the server send line to every client connected now (a message
// arriving while nobody's looking, say).
func (l *Log) Send(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, c := range l.conns {
		fmt.Fprintf(c, "%s\r\n", line)
	}
}

// Connections is how many clients have connected so far.
func (l *Log) Connections() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.conns)
}

// Lines returns what was received so far.
func (l *Log) Lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// Has reports whether a received line contains s.
func (l *Log) Has(s string) bool {
	for _, x := range l.Lines() {
		if strings.Contains(x, s) {
			return true
		}
	}
	return false
}

// StartLogged is Start, recording every line received in log (if not nil).
func StartLogged(t testing.TB, log *Log) string {
	t.Helper()
	return StartCaps(t, log, "")
}

// StartCaps is StartLogged with capabilities to offer in CAP LS: it ACKs
// any of them requested and lists the acknowledged ones on CAP LIST.
func StartCaps(t testing.TB, log *Log, caps string) string {
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
			if log != nil {
				log.mu.Lock()
				log.conns = append(log.conns, c)
				log.mu.Unlock()
			}
			go func(c net.Conn) {
				defer c.Close()
				nick := ""
				acked := []string{}
				sc := bufio.NewScanner(c)
				out := func(f string, a ...any) { fmt.Fprintf(c, f+"\r\n", a...) }
				for sc.Scan() {
					if log != nil {
						log.mu.Lock()
						log.lines = append(log.lines, sc.Text())
						log.mu.Unlock()
					}
					f := strings.Fields(sc.Text())
					if len(f) == 0 {
						continue
					}
					switch f[0] {
					case "CAP":
						switch {
						case len(f) > 1 && f[1] == "LS":
							out(":fake CAP * LS :%s", caps)
						case len(f) > 2 && f[1] == "REQ":
							req := strings.TrimPrefix(f[2], ":")
							if offered(caps, req) {
								acked = append(acked, req)
								out(":fake CAP %s ACK :%s", orStar(nick), req)
							} else {
								out(":fake CAP %s NAK :%s", orStar(nick), req)
							}
						case len(f) > 1 && f[1] == "LIST":
							out(":fake CAP %s LIST :%s", orStar(nick), strings.Join(acked, " "))
						}
					case "NICK":
						if nick == "" {
							nick = f[1]
						} else {
							out(":%s!u@h NICK %s", nick, f[1])
							nick = f[1]
						}
					case "USER":
						out(":fake 001 %s :welcome", nick)
					case "JOIN":
						out(":%s!u@h JOIN %s", nick, f[1])
					case "PART":
						out(":%s!u@h PART %s :bye", nick, f[1])
					case "NAMES":
						if len(f) > 1 {
							out(":fake 353 %s = %s :%s @op", nick, f[1], nick)
							out(":fake 366 %s %s :End of /NAMES list.", nick, f[1])
						}
					case "TOPIC":
						if len(f) > 1 {
							out(":fake 332 %s %s :the topic", nick, f[1])
						}
					case "SIMULATE": // test hook: the server sends a line as-is
						out("%s", strings.TrimPrefix(sc.Text(), "SIMULATE "))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}

// offered reports whether caps (CAP LS words, maybe with values) has name.
func offered(caps, name string) bool {
	for _, c := range strings.Fields(caps) {
		if n, _, _ := strings.Cut(c, "="); n == name {
			return true
		}
	}
	return false
}

func orStar(nick string) string {
	if nick == "" {
		return "*"
	}
	return nick
}
