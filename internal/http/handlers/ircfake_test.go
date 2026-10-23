package handlers_test

import (
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/testirc"
)

// fakeIRC is a testirc server for upstream connections, with helpers to
// wait for what it receives.
type fakeIRC struct {
	t    *testing.T
	log  *testirc.Log
	addr string
}

func newFakeIRC(t *testing.T) *fakeIRC {
	t.Helper()
	f := &fakeIRC{t: t, log: &testirc.Log{}}
	f.addr = testirc.StartLogged(t, f.log)
	return f
}

func (f *fakeIRC) port() int {
	_, p, _ := net.SplitHostPort(f.addr)
	n, _ := strconv.Atoi(p)
	return n
}

// send has the server send a raw line to every connected client.
func (f *fakeIRC) send(line string) { f.log.Send(line) }

// expect waits for a received line starting with prefix and returns it.
func (f *fakeIRC) expect(prefix string) string {
	f.t.Helper()
	var got string
	eventually(f.t, "the IRC server to get "+strconv.Quote(prefix), func() bool {
		for _, l := range f.log.Lines() {
			if strings.HasPrefix(l, prefix) {
				got = l
				return true
			}
		}
		return false
	})
	return got
}

// eventually polls cond until it holds or 5s pass.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}
