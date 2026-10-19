package irctls

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// plainServer answers every connection with say, in plain text, and closes.
func plainServer(t *testing.T, say string) string {
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
			c.Write([]byte(say))
			time.Sleep(50 * time.Millisecond)
			c.Close()
		}
	}()
	return ln.Addr().String()
}

func TestHandshakeSaysWhatAPlainTextServerSaid(t *testing.T) {
	for _, c := range []struct{ say, want string }{
		{"ERROR :*** Banned: Please use another server.\r\n", "the server refused the connection: *** Banned: Please use another server."},
		{":irc.example.org NOTICE AUTH :*** Looking up your hostname\r\n", `didn't answer with TLS on this port — it said ":irc.example.org NOTICE AUTH :*** Looking up your hostname"`},
	} {
		raw, err := net.Dial("tcp", plainServer(t, c.say))
		if err != nil {
			t.Fatal(err)
		}
		_, err = Handshake(context.Background(), raw, Config("irc.example.org", Trust{TrustPKI: true}, nil))
		raw.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%q: got %v", c.say, err)
		}
	}
}
