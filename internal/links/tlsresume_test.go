package links

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"testing"
)

// A client reconnecting through the holder resumes its TLS session: a phone
// back on the network does one round trip less.
func TestTLSSessionsResume(t *testing.T) {
	cl, _ := serveHolder(t, quietHolder())
	addr := handOver(t, cl, true)
	certPEM, keyPEM := testCert(t)
	if err := cl.SetCert(context.Background(), certPEM, keyPEM); err != nil {
		t.Fatal(err)
	}
	ch, _ := watch(t, cl)
	next(t, ch)
	conf := &tls.Config{InsecureSkipVerify: true, ClientSessionCache: tls.NewLRUClientSessionCache(4)}
	connect := func() *tls.Conn {
		t.Helper()
		tc, err := tls.Dial("tcp", addr, conf)
		if err != nil {
			t.Fatal(err)
		}
		cc, err := cl.AttachClient(context.Background(), next(t, ch))
		if err != nil {
			t.Fatal(err)
		}
		// a line from evrd: reading it takes in the session ticket too
		io.WriteString(cc, ":evrd NOTICE * :hi\r\n")
		if _, err := bufio.NewReader(tc).ReadString('\n'); err != nil {
			t.Fatal(err)
		}
		return tc
	}
	first := connect()
	if first.ConnectionState().DidResume {
		t.Fatal("the first connection resumed")
	}
	first.Close()
	second := connect()
	defer second.Close()
	if !second.ConnectionState().DidResume {
		t.Fatal("the second connection didn't resume its TLS session")
	}
}
