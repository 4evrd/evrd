package webpush

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
)

// device is a push subscriber: its keys, as a browser or the Boson app
// makes them, and what it can decrypt.
type device struct {
	priv *ecdh.PrivateKey
	auth []byte
}

func newDevice(t *testing.T) *device {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	auth := make([]byte, 16)
	rand.Read(auth)
	return &device{priv: priv, auth: auth}
}

// param is the keys as WEBPUSH REGISTER sends them.
func (d *device) param() string {
	return "p256dh=" + base64.RawURLEncoding.EncodeToString(d.priv.PublicKey().Bytes()) + ";auth=" + base64.RawURLEncoding.EncodeToString(d.auth)
}

func (d *device) keys(t *testing.T) SubKeys {
	t.Helper()
	k, err := ParseKeys(d.param())
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// decrypt opens an aes128gcm push body (RFC 8291) as the device does.
func (d *device) decrypt(body []byte) ([]byte, error) {
	if len(body) < headerLen+17 {
		return nil, errors.New("short body")
	}
	salt := body[:16]
	rs := binary.BigEndian.Uint32(body[16:20])
	idLen := int(body[20])
	if idLen != 65 || rs != recordSize {
		return nil, errors.New("bad header")
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idLen])
	if err != nil {
		return nil, err
	}
	shared, err := d.priv.ECDH(asPub)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), d.priv.PublicKey().Bytes()...), asPub.Bytes()...)
	ikm, _ := hkdf.Key(sha256.New, shared, d.auth, string(info), 32)
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idLen:], nil)
	if err != nil {
		return nil, err
	}
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("no last-record delimiter")
	}
	return plain[:len(plain)-1], nil
}

// verifyJWT checks an ES256 JWT with the public key pub (base64url, an
// uncompressed point) and returns its claims.
func verifyJWT(t *testing.T, token, pub string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(pub)
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), raw)
	if err != nil {
		t.Fatal(err)
	}
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(key, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the JWT's signature doesn't verify")
	}
	var head, claims map[string]any
	h, _ := base64.RawURLEncoding.DecodeString(parts[0])
	c, _ := base64.RawURLEncoding.DecodeString(parts[1])
	if json.Unmarshal(h, &head) != nil || head["alg"] != "ES256" || head["typ"] != "JWT" {
		t.Fatalf("header %s", h)
	}
	if err := json.Unmarshal(c, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestEncryptRoundTrip(t *testing.T) {
	d := newDevice(t)
	for _, msg := range []string{"", "PING webpush", strings.Repeat("é", MaxPayload/2)} {
		body, err := encrypt([]byte(msg), d.keys(t))
		if err != nil {
			t.Fatal(err)
		}
		if len(body) > recordSize {
			t.Fatalf("body %d bytes", len(body))
		}
		got, err := d.decrypt(body)
		if err != nil || string(got) != msg {
			t.Fatalf("decrypted %q %v", got, err)
		}
	}
	if _, err := encrypt(make([]byte, MaxPayload+1), d.keys(t)); !errors.Is(err, ErrTooLong) {
		t.Fatalf("too long: %v", err)
	}
	// another device can't read it
	body, _ := encrypt([]byte("secret"), d.keys(t))
	if _, err := newDevice(t).decrypt(body); err == nil {
		t.Fatal("decrypted with another device's key")
	}
}

// RFC 8291's example (section 5): the same keys and salt give the same
// body.
func TestEncryptRFC8291Example(t *testing.T) {
	b64 := func(s string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	eph, err := ecdh.P256().NewPrivateKey(b64("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	keys, err := DecodeKeys("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4", "BTBZMqHH6r4Tts7J_aSIgg")
	if err != nil {
		t.Fatal(err)
	}
	body, err := seal([]byte("When I grow up, I want to be a watermelon"), keys, eph, b64("DGv6ra1nlYgDCS1FRnbzlw"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if got := base64.RawURLEncoding.EncodeToString(body); got != want {
		t.Fatalf("body\n got %s\nwant %s", got, want)
	}
}

func TestParseKeys(t *testing.T) {
	d := newDevice(t)
	pub := d.priv.PublicKey().Bytes()
	for _, p := range []string{
		d.param(),
		"auth=" + base64.URLEncoding.EncodeToString(d.auth) + ";p256dh=" + base64.URLEncoding.EncodeToString(pub),
		"p256dh=" + base64.StdEncoding.EncodeToString(pub) + ";auth=" + base64.StdEncoding.EncodeToString(d.auth),
	} {
		k, err := ParseKeys(p)
		if err != nil || !k.P256DH.Equal(d.priv.PublicKey()) || string(k.Auth) != string(d.auth) {
			t.Fatalf("%q: %v", p, err)
		}
	}
	for _, p := range []string{
		"", "p256dh=" + base64.RawURLEncoding.EncodeToString(pub),
		"p256dh=" + base64.RawURLEncoding.EncodeToString(pub) + ";auth=" + base64.RawURLEncoding.EncodeToString(d.auth[:8]),
		"p256dh=" + base64.RawURLEncoding.EncodeToString(pub[:33]) + ";auth=" + base64.RawURLEncoding.EncodeToString(d.auth),
		"p256dh=!!!;auth=" + base64.RawURLEncoding.EncodeToString(d.auth),
	} {
		if _, err := ParseKeys(p); !errors.Is(err, ErrInvalidKeys) {
			t.Fatalf("%q: %v", p, err)
		}
	}
	p, a := d.keys(t).Strings()
	if "p256dh="+p+";auth="+a != d.param() {
		t.Fatal("Strings")
	}
}

func TestVAPIDJWT(t *testing.T) {
	k, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := base64.RawURLEncoding.DecodeString(k.PublicKey()); len(raw) != 65 || raw[0] != 4 {
		t.Fatalf("public key %q", k.PublicKey())
	}
	now := time.Now()
	for sub, want := range map[string]string{"https://bnc.example.org": "https://bnc.example.org", "admin@example.org": "mailto:admin@example.org", "mailto:a@b": "mailto:a@b"} {
		h, err := k.authorization("https://push.example.com:8443/v1/push/abc?x=1", sub, now)
		if err != nil {
			t.Fatal(err)
		}
		tok, kpart, ok := strings.Cut(strings.TrimPrefix(h, "vapid t="), ", k=")
		if !ok || !strings.HasPrefix(h, "vapid t=") || kpart != k.PublicKey() {
			t.Fatalf("header %q", h)
		}
		c := verifyJWT(t, tok, k.PublicKey())
		if c["aud"] != "https://push.example.com:8443" || c["sub"] != want || int64(c["exp"].(float64)) != now.Add(jwtLife).Unix() {
			t.Fatalf("claims %v", c)
		}
	}
	if _, err := k.authorization("://bad", "x", now); err == nil {
		t.Fatal("a bad endpoint")
	}
}

func TestKeysPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "webpush-vapid.pem")
	k1, created, err := LoadOrCreateKeys(path)
	if err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	k2, created, err := LoadOrCreateKeys(path)
	if err != nil || created || k2.PublicKey() != k1.PublicKey() {
		t.Fatalf("load: %v %v", created, err)
	}
	os.WriteFile(path, []byte("junk"), 0o600)
	if _, _, err := LoadOrCreateKeys(path); err == nil {
		t.Fatal("junk loaded")
	}
	// an SEC 1 key file (openssl ecparam -genkey) works too
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := keysOf(priv); err != nil {
		t.Fatal(err)
	}
	other, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if _, err := keysOf(other); err == nil {
		t.Fatal("a P-384 key")
	}
	if _, _, err := LoadOrCreateKeys(t.TempDir()); err == nil {
		t.Fatal("a directory")
	}
}

func TestParseKeysSEC1AndOthers(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509MarshalEC(priv)
	if _, err := parseKeys(pemBlock("EC PRIVATE KEY", der)); err != nil {
		t.Fatal(err)
	}
	if _, err := parseKeys(pemBlock("PRIVATE KEY", []byte("nonsense"))); err == nil {
		t.Fatal("nonsense")
	}
	ed := ed25519PKCS8(t)
	if _, err := parseKeys(pemBlock("PRIVATE KEY", ed)); err == nil {
		t.Fatal("an Ed25519 key")
	}
}

func TestCheckEndpointAndInternalIPs(t *testing.T) {
	for _, ok := range []string{"https://push.example.com/v1/abc", "https://[2001:db8::1]:8443/x"} {
		if CheckEndpoint(ok) != nil {
			t.Errorf("%s refused", ok)
		}
	}
	for _, bad := range []string{"http://push.example.com/x", "ftp://x/y", "https://", "https://u:p@push.example.com/", "::", "push.example.com/x"} {
		if CheckEndpoint(bad) == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	c := externalClient(2 * time.Second)
	for _, u := range []string{
		"https://127.0.0.2/test", "https://127.0.0.1:8201/x", "https://0.0.0.0/asdf", "https://100.64.0.1",
		"https://10.1.2.3/", "https://192.168.1.1/", "https://169.254.169.254/latest", "https://[::1]/",
		"https://[0:0:0:0:0:ffff:6440:0001]", "https://[fe80::1]/", "https://224.0.0.1/", "https://localhost:1/",
	} {
		_, err := c.Post(u, "text/plain", nil)
		if !errors.Is(err, errInternalIP) {
			t.Errorf("%s: %v", u, err)
		}
	}
	if internalIP(netip.MustParseAddr("8.8.8.8")) || internalIP(netip.MustParseAddr("2001:4860::8888")) {
		t.Error("public addresses are internal?")
	}
}

// --- the service ---------------------------------------------------------------

// pushServer is a push service: it records what it gets and answers with
// status (201 unless set).
type pushServer struct {
	*httptest.Server
	mu     sync.Mutex
	got    []*http.Request
	bodies [][]byte
	status int
}

func newPushServer(t *testing.T) *pushServer {
	ps := &pushServer{status: http.StatusCreated}
	ps.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		ps.mu.Lock()
		ps.got, ps.bodies = append(ps.got, r), append(ps.bodies, b)
		st := ps.status
		ps.mu.Unlock()
		w.WriteHeader(st)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func (ps *pushServer) setStatus(s int) { ps.mu.Lock(); ps.status = s; ps.mu.Unlock() }

func (ps *pushServer) count() int { ps.mu.Lock(); defer ps.mu.Unlock(); return len(ps.got) }

func (ps *pushServer) last() (*http.Request, []byte) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.got[len(ps.got)-1], ps.bodies[len(ps.bodies)-1]
}

type rig struct {
	t   *testing.T
	st  store.Store
	svc *Service
	ps  *pushServer
	u   store.User
	n   store.Network
	att bool
	mu  sync.Mutex
}

func newRig(t *testing.T, opts Options) *rig {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	u, _ := st.CreateUser(ctx, store.User{Username: "alice"})
	n, _ := st.CreateNetwork(ctx, store.Network{UserID: u.ID, Name: "Example Net", Nick: "alice", Host: "h"})
	k, _ := GenerateKeys()
	r := &rig{t: t, st: st, u: u, n: n, ps: newPushServer(t)}
	r.svc = New(st, k, opts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.svc.HTTP = r.ps.Client()
	r.svc.Attached = func(int64) bool { r.mu.Lock(); defer r.mu.Unlock(); return r.att }
	return r
}

func (r *rig) setAttached(on bool) { r.mu.Lock(); r.att = on; r.mu.Unlock() }

func (r *rig) register(d *device, path string, networkID int64) error {
	return r.svc.Register(context.Background(), r.u.ID, networkID, r.ps.URL+path, d.keys(r.t))
}

func (r *rig) subs() []store.PushSubscription {
	s, _ := r.st.PushSubscriptions(context.Background(), r.u.ID)
	return s
}

func dm(from, text string, at time.Time) history.Message {
	return history.Message{NetworkID: 1, Target: from, Sender: from, Kind: "message", Text: text, Time: at, Seq: 7}
}

func TestRegisterSendsATestPushAndRenews(t *testing.T) {
	r := newRig(t, Options{MaxSubscriptions: 2, Subscriber: "https://bnc.example.org"})
	d := newDevice(t)
	if err := r.register(d, "/a", r.n.ID); err != nil {
		t.Fatal(err)
	}
	if r.ps.count() != 1 {
		t.Fatalf("pushes %d", r.ps.count())
	}
	req, body := r.ps.last()
	if got, _ := d.decrypt(body); string(got) != "PING webpush" {
		t.Fatalf("test push %q", got)
	}
	for h, want := range map[string]string{"Content-Encoding": "aes128gcm", "TTL": "604800", "Urgency": "high", "Content-Type": "application/octet-stream"} {
		if req.Header.Get(h) != want {
			t.Errorf("%s: %q", h, req.Header.Get(h))
		}
	}
	tok, k, _ := strings.Cut(strings.TrimPrefix(req.Header.Get("Authorization"), "vapid t="), ", k=")
	if k != r.svc.PublicKey() {
		t.Fatalf("k=%q", k)
	}
	if c := verifyJWT(t, tok, k); c["aud"] != r.ps.URL || c["sub"] != "https://bnc.example.org" {
		t.Fatalf("claims %v", c)
	}
	before := r.subs()[0].LastRefresh
	time.Sleep(5 * time.Millisecond)
	// again, the same keys: renewed, no test push
	if err := r.register(d, "/a", r.n.ID); err != nil {
		t.Fatal(err)
	}
	if s := r.subs(); r.ps.count() != 1 || len(s) != 1 || !s[0].LastRefresh.After(before) {
		t.Fatalf("renewal: %d pushes, %+v", r.ps.count(), s)
	}
	// new keys for the same endpoint: tested again, and replaced
	d2 := newDevice(t)
	if err := r.register(d2, "/a", r.n.ID); err != nil || r.ps.count() != 2 || len(r.subs()) != 1 {
		t.Fatalf("new keys: %v, %d pushes, %+v", err, r.ps.count(), r.subs())
	}
	// the limit (per network)
	if err := r.register(d, "/b", r.n.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.register(d, "/c", r.n.ID); !errors.Is(err, store.ErrPushLimit) {
		t.Fatalf("over the limit: %v", err)
	}
	if err := r.register(d, "/c", 0); err != nil {
		t.Fatalf("a bouncer connection's: %v", err)
	}
	// a test push that fails
	r.ps.setStatus(http.StatusBadRequest)
	if err := r.register(newDevice(t), "/d", 0); !errors.Is(err, ErrTestFailed) {
		t.Fatalf("failed test push: %v", err)
	}
	if err := r.svc.Unregister(context.Background(), r.u.ID, r.n.ID, r.ps.URL+"/a"); err != nil || len(r.subs()) != 2 {
		t.Fatalf("unregister: %v %+v", err, r.subs())
	}
}

func TestNotifyDelaysAndReadMarkerSuppresses(t *testing.T) {
	r := newRig(t, Options{Delay: 150 * time.Millisecond})
	d := newDevice(t)
	r.register(d, "/a", r.n.ID)
	ctx := context.Background()

	// read on another client within the delay: no push
	at := time.Now()
	r.svc.Notify(r.n, "alice", dm("bob", "hello?", at))
	time.Sleep(50 * time.Millisecond)
	r.st.SetReadMarker(ctx, r.n.ID, "bob", at.Truncate(time.Millisecond)) // as MARKREAD sends it
	r.svc.Wait()
	if r.ps.count() != 1 {
		t.Fatalf("pushed a line read meanwhile: %d", r.ps.count())
	}

	// not read: pushed, after the delay
	at = time.Now()
	r.svc.Notify(r.n, "alice", dm("bob", "still there?", at))
	time.Sleep(50 * time.Millisecond)
	if r.ps.count() != 1 {
		t.Fatal("pushed before the delay")
	}
	r.svc.Wait()
	if r.ps.count() != 2 {
		t.Fatalf("pushes %d", r.ps.count())
	}
	_, body := r.ps.last()
	line, err := d.decrypt(body)
	want := "@time=" + at.UTC().Format(timeFormat) + ";msgid=" + dm("bob", "", at).ID().String() + " :bob!bob@Example-Net PRIVMSG alice :still there?"
	if err != nil || string(line) != want {
		t.Fatalf("push\n got %q %v\nwant %q", line, err, want)
	}
	if s := r.subs(); s[0].LastSuccess.Before(at) {
		t.Fatalf("last success %v", s[0].LastSuccess)
	}
}

func TestNotifyOnlyWithoutAttachedClients(t *testing.T) {
	r := newRig(t, Options{Delay: 100 * time.Millisecond})
	r.register(newDevice(t), "/a", r.n.ID)
	r.setAttached(true)
	r.svc.Notify(r.n, "alice", dm("bob", "hi", time.Now()))
	r.svc.Wait()
	if r.ps.count() != 1 {
		t.Fatal("pushed with a client attached")
	}
	// one attaching during the delay gets it in playback
	r.setAttached(false)
	r.svc.Notify(r.n, "alice", dm("bob", "hi", time.Now()))
	time.Sleep(20 * time.Millisecond)
	r.setAttached(true)
	r.svc.Wait()
	if r.ps.count() != 1 {
		t.Fatal("pushed after a client attached")
	}
}

func TestGoneSubscriptionsAreDropped(t *testing.T) {
	for _, status := range []int{http.StatusGone, http.StatusNotFound} {
		r := newRig(t, Options{})
		r.register(newDevice(t), "/a", r.n.ID)
		r.register(newDevice(t), "/b", 0)
		r.ps.setStatus(status)
		r.svc.Notify(r.n, "alice", dm("bob", "hi", time.Now()))
		r.svc.Wait()
		if r.ps.count() != 4 || len(r.subs()) != 0 {
			t.Fatalf("%d: %d pushes, %+v left", status, r.ps.count(), r.subs())
		}
	}
	// other failures keep it
	r := newRig(t, Options{})
	r.register(newDevice(t), "/a", r.n.ID)
	r.ps.setStatus(http.StatusInternalServerError)
	r.svc.Notify(r.n, "alice", dm("bob", "hi", time.Now()))
	r.svc.Wait()
	if len(r.subs()) != 1 {
		t.Fatal("dropped on a 500")
	}
}

func TestPushesGoToTheNetworksSubscriptions(t *testing.T) {
	r := newRig(t, Options{})
	ctx := context.Background()
	other, _ := r.st.CreateNetwork(ctx, store.Network{UserID: r.u.ID, Name: "two", Nick: "alice", Host: "h"})
	d := newDevice(t)
	r.register(d, "/one", r.n.ID)
	r.register(d, "/both", r.n.ID)
	r.register(d, "/both", 0) // the same endpoint twice: once
	r.register(d, "/two", other.ID)
	base := r.ps.count()
	r.svc.Notify(r.n, "alice", history.Message{Target: "#chan", Sender: "bob", Kind: "action", Text: "waves at alice", Time: time.Now(), Tags: "+draft/reply=abc"})
	r.svc.Wait()
	if r.ps.count()-base != 2 {
		t.Fatalf("pushes %d", r.ps.count()-base)
	}
	paths := map[string]bool{}
	r.ps.mu.Lock()
	for _, q := range r.ps.got[base:] {
		paths[q.URL.Path] = true
	}
	r.ps.mu.Unlock()
	if !paths["/one"] || !paths["/both"] {
		t.Fatalf("went to %v", paths)
	}
	_, body := r.ps.last()
	line, _ := d.decrypt(body)
	if !strings.Contains(string(line), ";+draft/reply=abc :bob!bob@Example-Net PRIVMSG #chan :\x01ACTION waves at alice\x01") {
		t.Fatalf("line %q", line)
	}
}

func TestMarkReadIsPushed(t *testing.T) {
	r := newRig(t, Options{})
	d := newDevice(t)
	r.register(d, "/phone", r.n.ID)
	r.register(d, "/laptop", r.n.ID)
	at := time.Now()
	// nothing was pushed for it: nothing to clear
	r.svc.MarkRead(r.u.ID, r.n.ID, "bob", at, "", "irc.example.org")
	r.svc.Wait()
	base := r.ps.count()
	if base != 2 {
		t.Fatal(base)
	}
	r.svc.Notify(r.n, "alice", dm("bob", "hi", at))
	r.svc.Wait()
	// read before it: still there
	r.svc.MarkRead(r.u.ID, r.n.ID, "bob", at.Add(-time.Second), "", "irc.example.org")
	r.svc.Wait()
	if r.ps.count() != base+2 {
		t.Fatalf("pushes %d", r.ps.count())
	}
	// read on the laptop: the phone hears
	read := at.Truncate(time.Millisecond)
	r.svc.MarkRead(r.u.ID, r.n.ID, "Bob", read, r.ps.URL+"/laptop", "irc.example.org")
	r.svc.Wait()
	if r.ps.count() != base+3 {
		t.Fatalf("pushes %d", r.ps.count())
	}
	req, body := r.ps.last()
	line, _ := d.decrypt(body)
	if req.URL.Path != "/phone" || req.Header.Get("Urgency") != "normal" ||
		!strings.HasSuffix(string(line), " :irc.example.org MARKREAD Bob timestamp="+read.UTC().Format(timeFormat)) {
		t.Fatalf("%s %s: %q", req.URL.Path, req.Header.Get("Urgency"), line)
	}
	// once
	r.svc.MarkRead(r.u.ID, r.n.ID, "bob", read, "", "irc.example.org")
	r.svc.Wait()
	if r.ps.count() != base+3 {
		t.Fatal("cleared twice")
	}
}

func TestMaintainExpiresAndPings(t *testing.T) {
	r := newRig(t, Options{Expiration: 10 * 24 * time.Hour})
	ctx := context.Background()
	now := time.Now()
	d := newDevice(t)
	p, a := d.keys(t).Strings()
	add := func(ep string, refresh, success time.Time) {
		r.st.SavePushSubscription(ctx, store.PushSubscription{UserID: r.u.ID, NetworkID: r.n.ID, Endpoint: r.ps.URL + ep, P256DH: p, Auth: a, LastRefresh: refresh, LastSuccess: success}, 0)
	}
	add("/fresh", now, now)
	add("/stale-refresh", now.Add(-11*24*time.Hour), now)
	add("/stale-success", now, now.Add(-11*24*time.Hour))
	add("/quiet", now, now.Add(-6*24*time.Hour))
	r.svc.Maintain(ctx)
	var eps []string
	for _, s := range r.subs() {
		eps = append(eps, strings.TrimPrefix(s.Endpoint, r.ps.URL))
	}
	if strings.Join(eps, " ") != "/fresh /quiet" {
		t.Fatalf("left %v", eps)
	}
	if r.ps.count() != 1 {
		t.Fatalf("pings %d", r.ps.count())
	}
	req, body := r.ps.last()
	if got, _ := d.decrypt(body); req.URL.Path != "/quiet" || string(got) != "PING webpush" {
		t.Fatalf("%s %q", req.URL.Path, got)
	}
	if s := r.subs(); s[1].LastSuccess.Before(now) {
		t.Fatal("the ping's success isn't recorded")
	}
	// Run does it as the leader
	r.st.SavePushSubscription(ctx, store.PushSubscription{UserID: r.u.ID, NetworkID: r.n.ID, Endpoint: r.ps.URL + "/old", P256DH: p, Auth: a, LastRefresh: now.Add(-20 * 24 * time.Hour), LastSuccess: now}, 0)
	rctx, cancel := context.WithCancel(ctx)
	go r.svc.Run(rctx, 10*time.Millisecond, func() bool { return true })
	deadline := time.Now().Add(3 * time.Second)
	for len(r.subs()) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if len(r.subs()) != 2 {
		t.Fatalf("Run: %+v", r.subs())
	}
}

func TestNotifyBounds(t *testing.T) {
	r := newRig(t, Options{Delay: time.Hour})
	for range maxPendingUser + 5 {
		r.svc.Notify(r.n, "alice", dm("bob", "hi", time.Now()))
	}
	r.svc.mu.Lock()
	n := r.svc.pending[r.u.ID]
	r.svc.mu.Unlock()
	if n != maxPendingUser {
		t.Fatalf("pending %d", n)
	}
}

func TestOffService(t *testing.T) {
	var s *Service
	if s.Enabled() || s.PublicKey() != "" {
		t.Fatal("nil is off")
	}
	s.Notify(store.Network{}, "", history.Message{})
	s.MarkRead(1, 1, "x", time.Now(), "", "evrd")
	s.Maintain(context.Background())
	s.Run(context.Background(), time.Second, nil)
	off := New(nil, nil, Options{}, slog.Default())
	if off.Enabled() || off.opts.MaxSubscriptions != 8 || off.opts.Expiration != 30*24*time.Hour || off.opts.Subscriber != "https://evrd.sh" {
		t.Fatalf("defaults %+v", off.opts)
	}
	// the default client is the external-only one
	if off.client() == nil || off.client() != off.HTTP {
		t.Fatal("client")
	}
}

func TestMessageLine(t *testing.T) {
	at := time.Date(2026, 11, 27, 10, 0, 0, 123456789, time.UTC)
	m := history.Message{Target: "#chan", Sender: "bob", Kind: "message", Text: "alice: hi", Time: at, Seq: 1}
	if got := string(MessageLine("net", "alice", m)); got != "@time=2026-11-27T10:00:00.123Z;msgid="+m.ID().String()+" :bob!bob@net PRIVMSG #chan :alice: hi" {
		t.Fatal(got)
	}
	m.Kind = "notice"
	if got := string(MessageLine("", "", m)); !strings.HasSuffix(got, " :bob!bob@irc NOTICE #chan :alice: hi") {
		t.Fatal(got)
	}
	// too long: the client tags go, then the text is cut (not mid-character)
	m.Kind, m.Tags, m.Text = "message", "+draft/reply="+strings.Repeat("x", 3000), strings.Repeat("é", 1000)
	got := MessageLine("net", "alice", m)
	if len(got) > MaxPayload || strings.Contains(string(got), "+draft/reply") || !strings.HasSuffix(string(got), "é") {
		t.Fatalf("%d bytes: %q", len(got), got[:80])
	}
	m.Text = "short"
	if got := string(MessageLine("net", "alice", m)); !strings.Contains(got, "+draft/reply") || !strings.HasSuffix(got, ":short") {
		t.Fatal(got[:80])
	}
	if cut("abc", 0) != "" || cut("abc", 5) != "abc" {
		t.Fatal("cut")
	}
	if l := string(markReadLine("evrd", "#chan", at, at)); l != "@time=2026-11-27T10:00:00.123Z :evrd MARKREAD #chan timestamp=2026-11-27T10:00:00.123Z" {
		t.Fatal(l)
	}
}

func x509MarshalEC(k *ecdsa.PrivateKey) ([]byte, error) { return x509.MarshalECPrivateKey(k) }

func pemBlock(typ string, der []byte) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der})
}

func ed25519PKCS8(t *testing.T) []byte {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return der
}
