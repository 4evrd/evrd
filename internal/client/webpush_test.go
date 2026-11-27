package client

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/managed"
	"github.com/4evrd/evrd/internal/webpush"
)

// pushDevice is a push subscriber's keys, and the pushes evrd sent it.
type pushDevice struct {
	priv *ecdh.PrivateKey
	auth []byte
	srv  *httptest.Server

	mu     sync.Mutex
	status int
	paths  []string
	bodies [][]byte
}

func newPushDevice(t *testing.T) *pushDevice {
	t.Helper()
	priv, _ := ecdh.P256().GenerateKey(rand.Reader)
	d := &pushDevice{priv: priv, auth: make([]byte, 16), status: http.StatusCreated}
	rand.Read(d.auth)
	d.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		d.mu.Lock()
		d.paths, d.bodies = append(d.paths, r.URL.Path), append(d.bodies, b)
		st := d.status
		d.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *pushDevice) keys() string {
	return "p256dh=" + base64.RawURLEncoding.EncodeToString(d.priv.PublicKey().Bytes()) + ";auth=" + base64.RawURLEncoding.EncodeToString(d.auth)
}

func (d *pushDevice) endpoint(path string) string { return d.srv.URL + path }

func (d *pushDevice) setStatus(s int) { d.mu.Lock(); d.status = s; d.mu.Unlock() }

func (d *pushDevice) count() int { d.mu.Lock(); defer d.mu.Unlock(); return len(d.bodies) }

// line is the i-th push, decrypted (RFC 8291).
func (d *pushDevice) line(t *testing.T, i int) string {
	t.Helper()
	d.mu.Lock()
	body := d.bodies[i]
	d.mu.Unlock()
	salt, idLen := body[:16], int(body[20])
	as, err := ecdh.P256().NewPublicKey(body[21 : 21+idLen])
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := d.priv.ECDH(as)
	info := append(append([]byte("WebPush: info\x00"), d.priv.PublicKey().Bytes()...), as.Bytes()...)
	ikm, _ := hkdf.Key(sha256.New, shared, d.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idLen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(string(plain), "\x02")
}

// pushService is a push service for the rig's store, trusting d's server.
func pushService(t *testing.T, st webpush.Store, d *pushDevice, opts webpush.Options) *webpush.Service {
	t.Helper()
	k, err := webpush.GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	p := webpush.New(st, k, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.HTTP = d.srv.Client()
	return p
}

func TestWebPushCommand(t *testing.T) {
	r := newRig(t)
	d := newPushDevice(t)
	push := pushService(t, r.st, d, webpush.Options{MaxSubscriptions: 2})
	push.Attached = func(uid int64) bool { return r.b.Attached(uid) > 0 }
	r.srv.SetPush(push)

	c := r.attach("alice/one:alice-password", "draft/webpush", "draft/read-marker")
	if l := c.expect(" 005 "); !strings.Contains(l, "VAPID="+push.PublicKey()) {
		t.Fatalf("ISUPPORT %q", l)
	}
	c.expect("JOIN #one")
	ep := d.endpoint("/sub/1")
	for _, tc := range [][2]string{
		{"WEBPUSH", " 461 alice_one WEBPUSH :Not enough parameters"},
		{"WEBPUSH REGISTER", " 461 alice_one WEBPUSH :Not enough parameters"},
		{"WEBPUSH REGISTER http://push.example/x " + d.keys(), "FAIL WEBPUSH INVALID_PARAMS REGISTER http://push.example/x :Invalid web push URL"},
		{"WEBPUSH REGISTER " + ep, "FAIL WEBPUSH INVALID_PARAMS REGISTER " + ep + " :Insufficient parameters for WEBPUSH REGISTER"},
		{"WEBPUSH REGISTER " + ep + " p256dh=AAAA;auth=BBBB", "FAIL WEBPUSH INVALID_PARAMS REGISTER " + ep + " :Invalid subscription keys for WEBPUSH REGISTER"},
		{"WEBPUSH FROB " + ep, "FAIL WEBPUSH INVALID_PARAMS FROB " + ep + " :Unknown subcommand"},
		{"WEBPUSH REGISTER :not a url", "FAIL WEBPUSH INVALID_PARAMS REGISTER * :Invalid web push URL"},
	} {
		c.send(tc[0])
		c.expect(tc[1])
	}
	if d.count() != 0 {
		t.Fatal("pushed for a refused registration")
	}
	// a test push that doesn't go through
	d.setStatus(http.StatusBadRequest)
	c.send("WEBPUSH REGISTER " + ep + " " + d.keys())
	c.expect("FAIL WEBPUSH INVALID_PARAMS REGISTER " + ep + " :Test push message failed to send")
	d.setStatus(http.StatusCreated)

	c.send("WEBPUSH register " + ep + " " + d.keys())
	c.expect(":evrd WEBPUSH REGISTER " + ep + " " + d.keys())
	if d.count() != 2 || d.line(t, 1) != "PING webpush" {
		t.Fatalf("test push: %d", d.count())
	}
	c.send("WEBPUSH REGISTER " + ep + " " + d.keys()) // renewed: no test push
	c.expect(":evrd WEBPUSH REGISTER " + ep)
	c.send("WEBPUSH REGISTER " + d.endpoint("/sub/2") + " " + d.keys())
	c.expect(":evrd WEBPUSH REGISTER " + d.endpoint("/sub/2"))
	c.send("WEBPUSH REGISTER " + d.endpoint("/sub/3") + " " + d.keys())
	c.expect("FAIL WEBPUSH MAX_REGISTRATIONS REGISTER " + d.endpoint("/sub/3") + " :You have too many push subscriptions already")
	c.send("WEBPUSH UNREGISTER " + d.endpoint("/sub/2"))
	c.expect(":evrd WEBPUSH UNREGISTER " + d.endpoint("/sub/2"))
	c.send("WEBPUSH UNREGISTER " + d.endpoint("/never")) // always answered
	c.expect(":evrd WEBPUSH UNREGISTER " + d.endpoint("/never"))
	if subs, _ := r.st.PushSubscriptions(r.ctx, r.user.ID); len(subs) != 1 || subs[0].Endpoint != ep || subs[0].NetworkID != r.network("one").ID {
		t.Fatalf("subscriptions %+v", subs)
	}
}

func TestWebPushOff(t *testing.T) {
	r := newRig(t)
	c := r.attach("alice/one:alice-password")
	c.send("CAP LS 302")
	if l := c.expect(" CAP "); strings.Contains(l, "webpush") {
		t.Fatalf("offered while off: %q", l)
	}
	c.send("WEBPUSH REGISTER https://push.example/x p256dh=a;auth=b")
	c.expect(":evrd FAIL WEBPUSH FORBIDDEN REGISTER https://push.example/x :Web push is disabled")
}

// The user's DMs and highlights are pushed while none of their clients is
// attached; with one attached, they aren't.
func TestWebPushDelivers(t *testing.T) {
	r := newRig(t)
	d := newPushDevice(t)
	push := pushService(t, r.st, d, webpush.Options{Delay: 100 * time.Millisecond})
	push.Attached = func(uid int64) bool { return r.b.Attached(uid) > 0 }
	r.srv.SetPush(push)
	r.b.SetPusher(push)

	c := r.attach("alice/one:alice-password", "draft/webpush", "message-tags", "server-time")
	c.expect("JOIN #one")
	c.send("WEBPUSH REGISTER " + d.endpoint("/phone") + " " + d.keys())
	c.expect("WEBPUSH REGISTER " + d.endpoint("/phone"))

	// attached: nothing's pushed
	r.irc.Send(":bob!b@host PRIVMSG alice_one :are you there?")
	c.expect("are you there?")
	push.Wait()
	if d.count() != 1 {
		t.Fatalf("pushed with a client attached: %d", d.count())
	}

	c.c.Close()
	eventually(t, "the client to detach", func() bool { return r.b.Attached(r.user.ID) == 0 })
	r.irc.Send(":bob!b@host PRIVMSG alice_one :hello?")
	r.irc.Send(":carol!c@host PRIVMSG #one :just chatting")
	r.irc.Send(":carol!c@host PRIVMSG #one :alice_one: look at this")
	r.irc.Send(":carol!c@host NOTICE alice_one :a notice")
	eventually(t, "the pushes", func() bool { return d.count() >= 3 })
	time.Sleep(300 * time.Millisecond)
	if d.count() != 3 {
		t.Fatalf("pushes %d", d.count())
	}
	lines := d.line(t, 1) + "\n" + d.line(t, 2)
	for _, want := range []string{":bob!bob@one PRIVMSG alice_one :hello?", ":carol!carol@one PRIVMSG #one :alice_one: look at this", "@time=", ";msgid="} {
		if !strings.Contains(lines, want) {
			t.Errorf("no %q in\n%s", want, lines)
		}
	}
}

// In managed mode, only a Boson account's client signed in with its token
// may register: never a guest.
func TestWebPushManaged(t *testing.T) {
	r, _ := guestRig(t)
	d := newPushDevice(t)
	push := pushService(t, r.st, d, webpush.Options{})
	r.srv.SetPush(push)
	r.prov.SetLive(t.Context(), managed.LiveInput{Guests: new(true)})

	g := r.guest("203.0.113.7", "visitor")
	g.expect(" 001 visitor ")
	g.send("WEBPUSH REGISTER " + d.endpoint("/g") + " " + d.keys())
	g.expect(":irc.example.org FAIL WEBPUSH FORBIDDEN REGISTER " + d.endpoint("/g") + " :You must be logged in to receive push messages")

	r.put("alice", managed.UserInput{Subject: new("usr_alice")})
	c := r.bearer("OAUTHBEARER", r.issuer.Token("usr_alice", "alice"), "draft/webpush")
	c.expect("903")
	c.send("CAP END")
	if l := c.expect(" 005 "); !strings.Contains(l, "VAPID="+push.PublicKey()) {
		t.Fatalf("ISUPPORT %q", l)
	}
	c.send("WEBPUSH REGISTER " + d.endpoint("/a") + " " + d.keys())
	c.expect(":irc.example.org WEBPUSH REGISTER " + d.endpoint("/a"))
	if d.count() != 1 {
		t.Fatalf("pushes %d", d.count())
	}
}
