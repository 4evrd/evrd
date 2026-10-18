// Package testirc is a fake IRC server for tests.
package testirc

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
)

// Start runs a minimal IRC server until the test ends and returns its
// address. It registers clients, echoes JOIN, PART and NICK back the way a
// real server confirms them, and sends "SIMULATE <line>" back verbatim.
func Start(t testing.TB) string {
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
			go func(c net.Conn) {
				defer c.Close()
				nick := ""
				sc := bufio.NewScanner(c)
				out := func(f string, a ...any) { fmt.Fprintf(c, f+"\r\n", a...) }
				for sc.Scan() {
					f := strings.Fields(sc.Text())
					if len(f) == 0 {
						continue
					}
					switch f[0] {
					case "CAP":
						if len(f) > 1 && f[1] == "LS" {
							out(":fake CAP * LS :")
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
					case "SIMULATE": // test hook: the server sends a line as-is
						out("%s", strings.TrimPrefix(sc.Text(), "SIMULATE "))
					}
				}
			}(c)
		}
	}()
	return ln.Addr().String()
}
