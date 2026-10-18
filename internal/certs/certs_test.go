package certs

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func testStore(t *testing.T) store.Store {
	t.Helper()
	st, err := store.Open("sqlite:" + filepath.Join(t.TempDir(), "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newManager(st store.Store) *Manager {
	m := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	m.SetStore(st)
	return m
}

func conf(mut func(*config.Config)) *config.Config {
	c := config.Default()
	c.PublicURL = "https://bnc.example.org"
	c.IRCHost = "irc.example.org"
	if mut != nil {
		mut(c)
	}
	return c
}

func serve(t *testing.T, m *Manager, name string) *tls.Certificate {
	t.Helper()
	c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: name})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSelfSignedIsKept(t *testing.T) {
	st := testStore(t)
	m := newManager(st)
	if err := m.Apply(conf(nil)); err != nil {
		t.Fatal(err)
	}
	c := serve(t, m, "")
	if got := c.Leaf.DNSNames; len(got) != 3 || got[0] != "bnc.example.org" || got[1] != "irc.example.org" || got[2] != "localhost" {
		t.Errorf("names %v", got)
	}

	// Another node (or a restart) serves the same one, from the database.
	m2 := newManager(st)
	if err := m2.Apply(conf(nil)); err != nil {
		t.Fatal(err)
	}
	if serve(t, m2, "x").Leaf.SerialNumber.Cmp(c.Leaf.SerialNumber) != 0 {
		t.Error("a second manager made a new certificate")
	}

	// New host names: a new certificate.
	if err := m2.Apply(conf(func(c *config.Config) { c.IRCHost = "chat.example.org" })); err != nil {
		t.Fatal(err)
	}
	if got := serve(t, m2, "").Leaf.DNSNames; got[1] != "chat.example.org" {
		t.Errorf("names %v after a host change", got)
	}

	r := m2.Report()
	if r.Source != "selfsigned" || len(r.Certs) != 1 || r.Certs[0].Issuer != "self-signed" || r.Certs[0].NotAfter == 0 {
		t.Errorf("report %+v", r)
	}
}

func TestFilesAndSwitching(t *testing.T) {
	dir := t.TempDir()
	data, err := makeSelfSigned([]string{"files.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath := filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	os.WriteFile(certPath, data, 0o600)
	os.WriteFile(keyPath, data, 0o600)

	m := newManager(testStore(t))
	if err := m.LoadFiles(certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	files := conf(func(c *config.Config) { c.TLSCert, c.TLSKey = certPath, keyPath })
	if files.TLSSource() != "files" {
		t.Fatalf("source %s with tls_cert set", files.TLSSource())
	}
	if err := m.Apply(files); err != nil {
		t.Fatal(err)
	}
	if serve(t, m, "").Leaf.DNSNames[0] != "files.example.org" {
		t.Error("not the file's certificate")
	}

	// Switched to self-signed while running (Admin → Settings).
	files.TLS.Source = "selfsigned"
	if err := m.Apply(files); err != nil {
		t.Fatal(err)
	}
	if serve(t, m, "").Leaf.DNSNames[0] != "bnc.example.org" {
		t.Error("still the file's certificate")
	}

	// Files out of reach (inside a chroot) keep what was read at startup.
	m2 := newManager(testStore(t))
	m2.LoadFiles(certPath, keyPath)
	os.Remove(certPath)
	files.TLS.Source = "files"
	if err := m2.Apply(files); err != nil || serve(t, m2, "").Leaf.DNSNames[0] != "files.example.org" {
		t.Errorf("lost the certificate read at startup: %v", err)
	}
}

func TestACMESetup(t *testing.T) {
	m := newManager(testStore(t))
	acmeConf := conf(func(c *config.Config) { c.TLS.Source = "acme"; c.TLS.ACMEEmail = "ops@example.org" })
	if err := m.Apply(acmeConf); err != nil {
		t.Fatal(err)
	}
	r := m.Report()
	if r.Source != "acme" || len(r.Certs) != 2 || !r.Certs[0].Pending {
		t.Errorf("report %+v", r)
	}
	// Not one of ours: refused without asking the CA.
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "evil.example.com"}); err == nil {
		t.Error("served a host outside the list")
	}

	// Nothing a CA would sign.
	if err := m.Apply(conf(func(c *config.Config) {
		c.TLS.Source, c.PublicURL, c.IRCHost = "acme", "http://localhost:8098", "10.0.0.1"
	})); err == nil {
		t.Error("acme with no public host names: no error")
	}
}

func TestHTTPHandlerRedirects(t *testing.T) {
	m := newManager(testStore(t))
	m.Apply(conf(nil))
	rec := httptest.NewRecorder()
	m.HTTPHandler(":443").ServeHTTP(rec, httptest.NewRequest("GET", "http://bnc.example.org:80/networks/2?x=1", nil))
	if rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "https://bnc.example.org/networks/2?x=1" {
		t.Errorf("%d %s", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	m.HTTPHandler("127.0.0.1:8443").ServeHTTP(rec, httptest.NewRequest("GET", "http://bnc.example.org/", nil))
	if rec.Header().Get("Location") != "https://bnc.example.org:8443/" {
		t.Errorf("other port: %s", rec.Header().Get("Location"))
	}
}

// TestACMEWithPebble gets a real certificate from Pebble, Let's Encrypt's
// test CA, answering its tls-alpn-01 challenge on :5001 (and http-01 on
// :5002). Run Pebble and its DNS (pointing evrd.test at this machine), then:
//
//	EVRD_TEST_PEBBLE=https://localhost:14000/dir EVRD_TEST_PEBBLE_CA=pebble.minica.pem go test -run Pebble ./internal/certs
func TestACMEWithPebble(t *testing.T) {
	dir, caFile := os.Getenv("EVRD_TEST_PEBBLE"), os.Getenv("EVRD_TEST_PEBBLE_CA")
	if dir == "" {
		t.Skip("EVRD_TEST_PEBBLE isn't set")
	}
	pemCA, err := os.ReadFile(caFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(pemCA)
	acmeHTTPClient = &http.Client{Transport: pebbleShim{&http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}}}}
	defer func() { acmeHTTPClient = nil }()

	st := testStore(t)
	m := newManager(st)
	c := conf(func(c *config.Config) {
		c.TLS.Source, c.TLS.ACMECA, c.TLS.ACMEHosts = "acme", dir, []string{"evrd.test"}
	})
	if err := m.Apply(c); err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", ":5001", m.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go http.Serve(ln, http.NotFoundHandler())
	hs := &http.Server{Addr: ":5002", Handler: m.HTTPHandler(":5001")}
	go hs.ListenAndServe()
	defer hs.Close()

	got := serve(t, m, "evrd.test")
	if got.Leaf.DNSNames[0] != "evrd.test" || issuerName(got.Leaf) == "self-signed" {
		t.Fatalf("got %v from %s", got.Leaf.DNSNames, issuerName(got.Leaf))
	}
	t.Logf("issued by %s, until %s", issuerName(got.Leaf), got.Leaf.NotAfter)
	if r := m.Report(); r.Certs[0].Pending || r.Certs[0].NotAfter == 0 {
		t.Errorf("report %+v", r)
	}
	// Without SNI (an IRC client connecting by address): the same one.
	if serve(t, m, "").Leaf.SerialNumber.Cmp(got.Leaf.SerialNumber) != 0 {
		t.Error("no-SNI handshake got another certificate")
	}
	// Another node: from the database, not the CA.
	m2 := newManager(st)
	m2.Apply(c)
	if serve(t, m2, "evrd.test").Leaf.SerialNumber.Cmp(got.Leaf.SerialNumber) != 0 {
		t.Error("a second node got its own certificate")
	}
}

// pebbleShim adds the order's Location to Pebble's finalize responses, as
// Let's Encrypt's own server sends it and x/crypto/acme expects it.
type pebbleShim struct{ rt http.RoundTripper }

func (p pebbleShim) RoundTrip(r *http.Request) (*http.Response, error) {
	res, err := p.rt.RoundTrip(r)
	if err == nil && strings.Contains(r.URL.Path, "/finalize-order/") && res.Header.Get("Location") == "" {
		u := *r.URL
		u.Path = strings.Replace(u.Path, "/finalize-order/", "/my-order/", 1)
		res.Header.Set("Location", u.String())
	}
	return res, err
}
