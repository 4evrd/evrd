package certs

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// logBuf collects log output, for checking what a Manager warned about.
type logBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *logBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func loggingManager(st store.Store) (*Manager, *logBuf) {
	lb := &logBuf{}
	m := New(slog.New(slog.NewTextHandler(lb, nil)))
	m.SetStore(st)
	return m, lb
}

// ecdsaHello is a ClientHello from a client that takes ECDSA certificates,
// as prefetch builds it.
func ecdsaHello(name string) *tls.ClientHelloInfo {
	return &tls.ClientHelloInfo{
		ServerName:       name,
		SignatureSchemes: []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
		SupportedCurves:  []tls.CurveID{tls.CurveP256},
		CipherSuites:     []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
	}
}

func writePair(t *testing.T, dir, host string) (certPath, keyPath string) {
	t.Helper()
	data, err := makeSelfSigned([]string{host})
	if err != nil {
		t.Fatal(err)
	}
	certPath, keyPath = filepath.Join(dir, "c.pem"), filepath.Join(dir, "k.pem")
	if err := os.WriteFile(certPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}

func TestFilesErrors(t *testing.T) {
	dir := t.TempDir()
	m := newManager(testStore(t))

	// No files configured: nothing to load, nothing to reload.
	if err := m.LoadFiles("", ""); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(); err != nil {
		t.Fatalf("reload with no files: %v", err)
	}

	// Garbage instead of PEM.
	bad := filepath.Join(dir, "bad.pem")
	os.WriteFile(bad, []byte("not a certificate"), 0o600)
	if err := m.LoadFiles(bad, bad); err == nil {
		t.Fatal("garbage loaded")
	}
	// Apply with files that can't be read: an error naming them, and the
	// report says why.
	c := conf(func(c *config.Config) { c.TLSCert, c.TLSKey = bad, bad })
	if err := m.Apply(c); err == nil || !strings.Contains(err.Error(), "tls_cert and tls_key") {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{}); err == nil || !strings.Contains(err.Error(), "couldn't be read") {
		t.Fatalf("GetCertificate: %v", err)
	}
	r := m.Report()
	if r.Source != "files" || len(r.Certs) != 1 || r.Certs[0].Host != bad || r.Certs[0].Error == "" || r.Certs[0].Subject != "" {
		t.Fatalf("report %+v", r)
	}

	// Mismatched key and certificate.
	certA, _ := writePair(t, filepath.Join(dir), "a.example.org")
	other := t.TempDir()
	_, keyB := writePair(t, other, "b.example.org")
	if err := m.LoadFiles(certA, keyB); err == nil {
		t.Fatal("mismatched key loaded")
	}

	// Fixed: Apply reads them again and the report describes the certificate.
	certP, keyP := writePair(t, t.TempDir(), "files.example.org")
	if err := m.Apply(conf(func(c *config.Config) { c.TLSCert, c.TLSKey = certP, keyP })); err != nil {
		t.Fatal(err)
	}
	r = m.Report()
	if s := r.Certs[0]; s.Host != certP || s.Error != "" || s.Subject != "files.example.org" || s.Issuer != "self-signed" ||
		len(s.Names) != 1 || s.Names[0] != "files.example.org" || s.NotAfter == 0 {
		t.Fatalf("report %+v", r)
	}
}

func TestReloadFilesWhenChanged(t *testing.T) {
	dir := t.TempDir()
	certP, keyP := writePair(t, dir, "old.example.org")
	m, logs := loggingManager(testStore(t))
	m.reloadFiles() // no files: nothing to do
	if err := m.Apply(conf(func(c *config.Config) { c.TLSCert, c.TLSKey = certP, keyP })); err != nil {
		t.Fatal(err)
	}

	// Rewritten, but with the same modification time: not re-read.
	before, _ := os.Stat(certP)
	writePair(t, dir, "new.example.org")
	os.Chtimes(certP, before.ModTime(), before.ModTime())
	m.reloadFiles()
	if got := serve(t, m, "").Leaf.DNSNames[0]; got != "old.example.org" {
		t.Fatalf("re-read an unchanged file: %s", got)
	}

	// Newer: re-read.
	later := before.ModTime().Add(time.Minute)
	os.Chtimes(certP, later, later)
	m.reloadFiles()
	if got := serve(t, m, "").Leaf.DNSNames[0]; got != "new.example.org" {
		t.Fatalf("after a change: %s", got)
	}
	if !strings.Contains(logs.String(), "reloaded tls_cert") {
		t.Errorf("no log of the reload: %s", logs)
	}

	// Newer but broken: the current one stays, with a warning.
	os.WriteFile(certP, []byte("half-written"), 0o600)
	later = later.Add(time.Minute)
	os.Chtimes(certP, later, later)
	m.reloadFiles()
	if got := serve(t, m, "").Leaf.DNSNames[0]; got != "new.example.org" {
		t.Fatalf("after a broken change: %s", got)
	}
	if !strings.Contains(logs.String(), "reload tls_cert") || !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("no warning: %s", logs)
	}

	// Gone (out of reach in the chroot): nothing happens.
	os.Remove(certP)
	m.reloadFiles()
	if got := serve(t, m, "").Leaf.DNSNames[0]; got != "new.example.org" {
		t.Fatalf("after removal: %s", got)
	}
}

func TestApplyIsCheapWhenNothingChanged(t *testing.T) {
	m := newManager(testStore(t))
	c := conf(nil)
	if err := m.Apply(c); err != nil {
		t.Fatal(err)
	}
	first := serve(t, m, "")
	m.mu.Lock()
	m.errs["x"] = "kept"
	m.mu.Unlock()
	if err := m.Apply(conf(nil)); err != nil {
		t.Fatal(err)
	}
	if serve(t, m, "") != first || m.errs["x"] != "kept" {
		t.Fatal("an unchanged Apply redid the setup")
	}
}

func TestSelfSigned(t *testing.T) {
	st := testStore(t)
	m, logs := loggingManager(st)
	c := conf(func(c *config.Config) { c.PublicURL, c.IRCHost = "https://localhost:8443", "192.0.2.7" })
	if err := m.Apply(c); err != nil {
		t.Fatal(err)
	}
	leaf := serve(t, m, "").Leaf
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "localhost" {
		t.Errorf("DNS names %v (localhost only once)", leaf.DNSNames)
	}
	if len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.ParseIP("192.0.2.7")) {
		t.Errorf("IPs %v", leaf.IPAddresses)
	}
	if leaf.Subject.CommonName != "localhost" || leaf.Subject.Organization[0] != "evrd" {
		t.Errorf("subject %v", leaf.Subject)
	}
	if time.Until(leaf.NotAfter) < 4*365*24*time.Hour {
		t.Errorf("valid only until %v", leaf.NotAfter)
	}
	if !strings.Contains(logs.String(), "made a self-signed certificate") {
		t.Errorf("logs: %s", logs)
	}

	// A handshake with it works, and the client sees the same certificate.
	ln, err := tls.Listen("tcp", "127.0.0.1:0", m.IRCConfig())
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	tc, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: "localhost", RootCAs: pool})
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	tc.Close()
}

func TestSelfSignedNearExpiryIsReplaced(t *testing.T) {
	st := testStore(t)
	m := newManager(st)
	if err := m.Apply(conf(nil)); err != nil {
		t.Fatal(err)
	}
	key := m.selfFor
	// What's stored expires in a week: too soon, a new one is made and saved.
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "bnc.example.org"},
		DNSNames: []string{"bnc.example.org"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(7 * 24 * time.Hour)}
	st.PutCertCache(key, string(pemOf(t, k, tmpl, "PKCS8")))
	m2 := newManager(st)
	if err := m2.Apply(conf(nil)); err != nil {
		t.Fatal(err)
	}
	got := serve(t, m2, "").Leaf
	if got.SerialNumber.Cmp(big.NewInt(99)) == 0 {
		t.Fatal("served a certificate about to expire")
	}
	data, _ := st.CertCache(key)
	if saved, err := parsePEM([]byte(data)); err != nil || saved.Leaf.SerialNumber.Cmp(got.SerialNumber) != 0 {
		t.Fatal("the new certificate wasn't saved")
	}

	// Without a database it's made anew each time, and still served.
	m3 := newManager(nil)
	if err := m3.Apply(conf(nil)); err != nil {
		t.Fatal(err)
	}
	if serve(t, m3, "") == nil {
		t.Fatal("no certificate without a database")
	}
}

func TestNoCertificateBeforeApply(t *testing.T) {
	m := newManager(nil)
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("a certificate before Apply")
	}
	if r := m.Report(); r.Source != "" || len(r.Certs) != 0 || r.Hosts == nil {
		t.Fatalf("report %+v", r)
	}
}

func TestTLSConfigs(t *testing.T) {
	m := newManager(nil)
	c := m.TLSConfig()
	if c.GetCertificate == nil || c.MinVersion != tls.VersionTLS12 ||
		strings.Join(c.NextProtos, ",") != "h2,http/1.1,"+acme.ALPNProto {
		t.Fatalf("TLSConfig %+v", c)
	}
	ic := m.IRCConfig()
	if ic.GetCertificate == nil || len(ic.NextProtos) != 1 || ic.NextProtos[0] != acme.ALPNProto {
		t.Fatalf("IRCConfig %+v", ic)
	}
}

func TestRunStopsWithItsContext(t *testing.T) {
	m := newManager(nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return")
	}
}

// pemOf writes key and a certificate from tmpl (self-signed) as one PEM
// blob, the key in the given format.
func pemOf(t *testing.T, key any, tmpl *x509.Certificate, format string) []byte {
	t.Helper()
	signer := key.(crypto.Signer)
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, signer.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	var kb *pem.Block
	switch format {
	case "PKCS8":
		b, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			t.Fatal(err)
		}
		kb = &pem.Block{Type: "PRIVATE KEY", Bytes: b}
	case "EC":
		b, err := x509.MarshalECPrivateKey(key.(*ecdsa.PrivateKey))
		if err != nil {
			t.Fatal(err)
		}
		kb = &pem.Block{Type: "EC PRIVATE KEY", Bytes: b}
	case "PKCS1":
		kb = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key.(*rsa.PrivateKey))}
	}
	return append(pem.EncodeToMemory(kb), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
}

func leafTmpl(host string) *x509.Certificate {
	return &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
}

func TestParsePEM(t *testing.T) {
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	for _, c := range []struct {
		name string
		key  any
		fmt  string
	}{
		{"PKCS8 ECDSA", ec, "PKCS8"},
		{"SEC1 ECDSA (autocert's cache)", ec, "EC"},
		{"PKCS1 RSA", rk, "PKCS1"},
		{"PKCS8 RSA", rk, "PKCS8"},
	} {
		t.Run(c.name, func(t *testing.T) {
			cert, err := parsePEM(pemOf(t, c.key, leafTmpl("p.example.org"), c.fmt))
			if err != nil {
				t.Fatal(err)
			}
			if cert.Leaf.DNSNames[0] != "p.example.org" || len(cert.Certificate) != 1 || cert.PrivateKey == nil {
				t.Fatalf("got %+v", cert)
			}
		})
	}

	good := pemOf(t, ec, leafTmpl("p.example.org"), "EC")
	keyOnly, rest := pem.Decode(good)
	for name, data := range map[string][]byte{
		"empty":          nil,
		"key only":       pem.EncodeToMemory(keyOnly),
		"cert only":      rest,
		"bad key":        append(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("nope")}), rest...),
		"bad cert":       append(pem.EncodeToMemory(keyOnly), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("nope")})...),
		"not PEM at all": []byte("hello"),
	} {
		if _, err := parsePEM(data); err == nil {
			t.Errorf("%s: no error", name)
		}
	}

	// a chain: leaf first, the rest kept; other blocks ignored
	two := append(append(pem.EncodeToMemory(&pem.Block{Type: "COMMENT", Bytes: []byte("x")}), good...), rest...)
	cert, err := parsePEM(two)
	if err != nil || len(cert.Certificate) != 2 {
		t.Fatalf("chain: %v, %d certificates", err, len(cert.Certificate))
	}
}

func TestIssuerName(t *testing.T) {
	for _, c := range []struct {
		subject, issuer pkix.Name
		want            string
	}{
		{pkix.Name{CommonName: "a"}, pkix.Name{CommonName: "a"}, "self-signed"},
		{pkix.Name{CommonName: "a", Organization: []string{"evrd"}}, pkix.Name{CommonName: "a", Organization: []string{"evrd"}}, "self-signed"},
		{pkix.Name{CommonName: "a", Organization: []string{"evrd"}}, pkix.Name{CommonName: "a", Organization: []string{"other"}}, "other a"},
		{pkix.Name{CommonName: "bnc.example.org"}, pkix.Name{CommonName: "R11", Organization: []string{"Let's Encrypt"}}, "Let's Encrypt R11"},
		{pkix.Name{CommonName: "bnc.example.org"}, pkix.Name{CommonName: "Some CA"}, "Some CA"},
	} {
		if got := issuerName(&x509.Certificate{Subject: c.subject, Issuer: c.issuer}); got != c.want {
			t.Errorf("%v by %v: %q, want %q", c.subject, c.issuer, got, c.want)
		}
	}
}

func TestDBCache(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	c := dbCache{newManager(st)}
	if _, err := c.Get(ctx, "k"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("Get of a missing key: %v", err)
	}
	if err := c.Put(ctx, "k", []byte("v1")); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(ctx, "k", []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if got, err := c.Get(ctx, "k"); err != nil || string(got) != "v2" {
		t.Fatalf("Get: %q %v", got, err)
	}
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(ctx, "k"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("Get after Delete: %v", err)
	}

	// No database: a miss, a refused write, a harmless delete.
	none := dbCache{newManager(nil)}
	if _, err := none.Get(ctx, "k"); !errors.Is(err, autocert.ErrCacheMiss) {
		t.Fatalf("Get without a database: %v", err)
	}
	if err := none.Put(ctx, "k", []byte("v")); err == nil {
		t.Fatal("Put without a database: no error")
	}
	if err := none.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
}

// acmeConf uses a CA that isn't there: anything not in the cache fails at
// once, without a network round trip to a real CA.
func acmeConf(t *testing.T) *config.Config {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dir := "http://" + ln.Addr().String() + "/directory"
	ln.Close()
	return conf(func(c *config.Config) {
		c.TLS.Source, c.TLS.ACMEEmail, c.TLS.ACMECA = "acme", "ops@example.org", dir
	})
}

func TestACMEFromTheCache(t *testing.T) {
	st := testStore(t)
	m, logs := loggingManager(st)
	if err := m.Apply(acmeConf(t)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "certificates from ACME") {
		t.Errorf("logs: %s", logs)
	}

	// irc.example.org's ECDSA certificate is already in the database (got
	// by another node); bnc.example.org's isn't, and the CA can't be reached.
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ircPEM := pemOf(t, ec, leafTmpl("irc.example.org"), "EC")
	st.PutCertCache("irc.example.org", string(ircPEM))

	got, err := m.GetCertificate(ecdsaHello("irc.example.org"))
	if err != nil || got.Leaf.DNSNames[0] != "irc.example.org" {
		t.Fatalf("cached certificate: %v", err)
	}
	if _, err := m.GetCertificate(ecdsaHello("bnc.example.org")); err == nil {
		t.Fatal("got a certificate from a CA that isn't there")
	}

	r := m.Report()
	if r.Source != "acme" || len(r.Hosts) != 2 || len(r.Certs) != 2 {
		t.Fatalf("report %+v", r)
	}
	bnc, irc := r.Certs[0], r.Certs[1]
	if bnc.Host != "bnc.example.org" || !bnc.Pending || bnc.Error == "" {
		t.Errorf("bnc: %+v", bnc)
	}
	if irc.Host != "irc.example.org" || irc.Pending || irc.Error != "" || irc.Subject != "irc.example.org" || irc.NotAfter == 0 {
		t.Errorf("irc: %+v", irc)
	}

	// Without SNI: the first host's certificate — which is bnc's, failing.
	if _, err := m.GetCertificate(ecdsaHello("")); err == nil {
		t.Fatal("no-SNI hello got a certificate")
	}

	// Once bnc's RSA certificate turns up (got by a node a non-ECDSA client
	// reached), the report finds it under "+rsa", and a success clears the
	// error.
	rk, _ := rsa.GenerateKey(rand.Reader, 2048)
	st.PutCertCache("bnc.example.org+rsa", string(pemOf(t, rk, leafTmpl("bnc.example.org"), "PKCS1")))
	if c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "bnc.example.org"}); err != nil || c.Leaf.DNSNames[0] != "bnc.example.org" {
		t.Fatalf("RSA from the cache: %v", err)
	}
	r = m.Report()
	if bnc := r.Certs[0]; bnc.Pending || bnc.Error != "" || bnc.Subject != "bnc.example.org" {
		t.Errorf("bnc after: %+v", bnc)
	}

	// Hosts outside the list aren't recorded as failures.
	m.GetCertificate(ecdsaHello("evil.example.com"))
	if _, ok := m.errs["evil.example.com"]; ok {
		t.Error("recorded an error for a host that isn't ours")
	}
}

func TestPrefetch(t *testing.T) {
	st := testStore(t)
	m, logs := loggingManager(st)
	m.prefetch() // not ACME: nothing
	if err := m.Apply(acmeConf(t)); err != nil {
		t.Fatal(err)
	}
	ec, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	st.PutCertCache("irc.example.org", string(pemOf(t, ec, leafTmpl("irc.example.org"), "EC")))
	m.prefetch()
	out := logs.String()
	if !strings.Contains(out, "get certificate") || !strings.Contains(out, "host=bnc.example.org") || strings.Contains(out, "host=irc.example.org") {
		t.Errorf("logs: %s", out)
	}
	if r := m.Report(); r.Certs[0].Error == "" || r.Certs[1].Error != "" || r.Certs[1].Pending {
		t.Errorf("report %+v", r)
	}
}

func TestACMEBadSetupKeepsServingNothing(t *testing.T) {
	m := newManager(testStore(t))
	err := m.Apply(conf(func(c *config.Config) {
		c.TLS.Source, c.PublicURL, c.IRCHost = "acme", "http://localhost:8098", "10.0.0.1"
	}))
	if err == nil {
		t.Fatal("no error")
	}
	if _, err := m.GetCertificate(ecdsaHello("localhost")); err == nil || !strings.Contains(err.Error(), "ACME isn't set up") {
		t.Fatalf("GetCertificate: %v", err)
	}
	if r := m.Report(); r.Source != "acme" || len(r.Certs) != 0 {
		t.Fatalf("report %+v", r)
	}
	if rec := get(m.HTTPHandler(":443"), "http://localhost/.well-known/acme-challenge/x"); rec.Code != http.StatusMovedPermanently {
		t.Fatalf("challenge without ACME: %d", rec.Code)
	}
}

func TestACMENoHostNames(t *testing.T) {
	m := newManager(testStore(t))
	if err := m.Apply(acmeConf(t)); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.acmeNames = nil
	m.mu.Unlock()
	if _, err := m.GetCertificate(&tls.ClientHelloInfo{}); err == nil || !strings.Contains(err.Error(), "no host names") {
		t.Fatalf("got %v", err)
	}
}

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec
}

func TestHTTPHandlerAnswersACMEChallenges(t *testing.T) {
	st := testStore(t)
	m := newManager(st)
	if err := m.Apply(acmeConf(t)); err != nil {
		t.Fatal(err)
	}
	h := m.HTTPHandler(":8443")
	// a token autocert left in the cache for the CA to fetch
	st.PutCertCache("tok123+http-01", "tok123.thumbprint")
	if rec := get(h, "http://bnc.example.org/.well-known/acme-challenge/tok123"); rec.Code != 200 || rec.Body.String() != "tok123.thumbprint" {
		t.Fatalf("challenge: %d %q", rec.Code, rec.Body)
	}
	if rec := get(h, "http://bnc.example.org/.well-known/acme-challenge/unknown"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown token: %d", rec.Code)
	}
	if rec := get(h, "http://evil.example.com/.well-known/acme-challenge/tok123"); rec.Code != http.StatusForbidden {
		t.Fatalf("host not ours: %d", rec.Code)
	}
	if rec := get(h, "http://bnc.example.org/login?next=/"); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "https://bnc.example.org:8443/login?next=/" {
		t.Fatalf("redirect: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	// an address that isn't host:port: no port in the redirect
	if rec := get(m.HTTPHandler("garbage"), "http://bnc.example.org/"); rec.Header().Get("Location") != "https://bnc.example.org/" {
		t.Fatalf("redirect: %s", rec.Header().Get("Location"))
	}
}
