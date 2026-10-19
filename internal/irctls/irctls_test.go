package irctls

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// testPKI is a CA plus a leaf it signed, generated in-test.
type testPKI struct {
	ca, leaf *x509.Certificate
	leafKey  *ecdsa.PrivateKey
	pool     *x509.CertPool
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// newPKI makes a CA and a leaf for dnsName valid from notBefore to notAfter.
func newPKI(t *testing.T, dnsName string, notBefore, notAfter time.Time) testPKI {
	t.Helper()
	caKey := newKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(caDER)
	leafKey := newKey(t)
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsName},
		DNSNames:     []string{dnsName},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(leafDER)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return testPKI{ca: ca, leaf: leaf, leafKey: leafKey, pool: pool}
}

func validPKI(t *testing.T, dnsName string) testPKI {
	return newPKI(t, dnsName, time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
}

// serveKey runs a TLS server presenting chain (signed for key) on a
// loopback listener.
func serveKey(t *testing.T, key *ecdsa.PrivateKey, chain ...*x509.Certificate) string {
	t.Helper()
	cert := tls.Certificate{PrivateKey: key}
	for _, c := range chain {
		cert.Certificate = append(cert.Certificate, c.Raw)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
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
				c.(*tls.Conn).Handshake()
				c.Write([]byte("hello\r\n"))
			}()
		}
	}()
	return ln.Addr().String()
}

// dial connects to addr and handshakes as host with trust t, returning what
// onUntrusted was told.
func dial(t *testing.T, addr, host string, tr Trust, pool *x509.CertPool) (*tls.Conn, string, error) {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { raw.Close() })
	var told string
	cfg := Config(host, tr, func(fp string) { told = fp })
	cfg.RootCAs = pool
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tc, err := Handshake(ctx, raw, cfg)
	return tc, told, err
}

func TestConfigServerName(t *testing.T) {
	cfg := Config("irc.example.org", Trust{}, nil)
	if cfg.ServerName != "irc.example.org" || !cfg.InsecureSkipVerify || cfg.VerifyConnection == nil {
		t.Fatalf("config %+v", cfg)
	}
}

func TestFingerprintAndColonHex(t *testing.T) {
	p := validPKI(t, "irc.example.org")
	fp := Fingerprint(p.leaf)
	if len(fp) != 64 || strings.ToLower(fp) != fp {
		t.Fatalf("fingerprint %q isn't 64 lowercase hex digits", fp)
	}
	if Fingerprint(p.ca) == fp {
		t.Fatal("CA and leaf share a fingerprint")
	}
	for in, want := range map[string]string{
		"":         "",
		"ab":       "AB",
		"abcdef":   "AB:CD:EF",
		"abc":      "AB:C",
		"0a1B2c3D": "0A:1B:2C:3D",
	} {
		if got := ColonHex(in); got != want {
			t.Errorf("ColonHex(%q) = %q, want %q", in, got, want)
		}
	}
	c := ColonHex(fp)
	if len(c) != 64+31 || strings.ReplaceAll(strings.ToLower(c), ":", "") != fp {
		t.Fatalf("ColonHex(%s) = %s", fp, c)
	}
}

func TestTrustedByFingerprint(t *testing.T) {
	p := validPKI(t, "other.example.org") // wrong name, unknown CA: only the fingerprint can save it
	addr := serveKey(t, p.leafKey, p.leaf)
	fp := Fingerprint(p.leaf)

	tc, told, err := dial(t, addr, "irc.example.org", Trust{Fingerprints: []string{"00", fp}}, nil)
	if err != nil {
		t.Fatalf("listed fingerprint refused: %v", err)
	}
	if told != "" {
		t.Fatalf("onUntrusted called with %q for a trusted cert", told)
	}
	buf := make([]byte, 7)
	if _, err := tc.Read(buf); err != nil || string(buf) != "hello\r\n" {
		t.Fatalf("read after handshake: %q %v", buf, err)
	}

	// Fingerprints are stored lowercase hex without colons (the accounts
	// layer normalises them); other spellings don't match here.
	for _, other := range []string{strings.ToUpper(fp), ColonHex(fp), strings.ToLower(ColonHex(fp))} {
		_, told, err := dial(t, addr, "irc.example.org", Trust{Fingerprints: []string{other}}, nil)
		var ue *UntrustedCertError
		if !errors.As(err, &ue) {
			t.Fatalf("%s: want UntrustedCertError, got %v", other, err)
		}
		if ue.Fingerprint != fp || told != fp {
			t.Fatalf("%s: error fingerprint %q, told %q, want %q", other, ue.Fingerprint, told, fp)
		}
	}
}

func TestTrustAll(t *testing.T) {
	p := validPKI(t, "nothing.example")
	addr := serveKey(t, p.leafKey, p.leaf)
	if _, told, err := dial(t, addr, "irc.example.org", Trust{TrustAll: true}, nil); err != nil || told != "" {
		t.Fatalf("trust-all refused: %v (told %q)", err, told)
	}
}

func TestNoTrustAtAll(t *testing.T) {
	p := validPKI(t, "irc.example.org")
	addr := serveKey(t, p.leafKey, p.leaf)
	// even a cert that PKI would accept is refused when PKI is off
	_, told, err := dial(t, addr, "irc.example.org", Trust{}, p.pool)
	var ue *UntrustedCertError
	if !errors.As(err, &ue) || ue.Reason != "certificate validation is off for this network" {
		t.Fatalf("got %v", err)
	}
	fp := Fingerprint(p.leaf)
	if ue.Fingerprint != fp || told != fp {
		t.Fatalf("fingerprint %q told %q", ue.Fingerprint, told)
	}
	if !strings.Contains(err.Error(), ColonHex(fp)) || !strings.Contains(err.Error(), "validation is off") {
		t.Fatalf("message %q", err)
	}
}

func TestPKI(t *testing.T) {
	good := validPKI(t, "irc.example.org")
	pki := Trust{TrustPKI: true}

	t.Run("valid", func(t *testing.T) {
		addr := serveKey(t, good.leafKey, good.leaf)
		if _, told, err := dial(t, addr, "irc.example.org", pki, good.pool); err != nil || told != "" {
			t.Fatalf("valid cert refused: %v (told %q)", err, told)
		}
	})

	t.Run("intermediate sent by the server", func(t *testing.T) {
		// root signs intermediate signs leaf; the client only knows root
		rk := newKey(t)
		rootTmpl := &x509.Certificate{SerialNumber: big.NewInt(10), Subject: pkix.Name{CommonName: "Root"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		rDER, err := x509.CreateCertificate(rand.Reader, rootTmpl, rootTmpl, &rk.PublicKey, rk)
		if err != nil {
			t.Fatal(err)
		}
		rc, _ := x509.ParseCertificate(rDER)
		ik := newKey(t)
		intTmpl := &x509.Certificate{SerialNumber: big.NewInt(11), Subject: pkix.Name{CommonName: "Intermediate"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
		iDER, err := x509.CreateCertificate(rand.Reader, intTmpl, rc, &ik.PublicKey, rk)
		if err != nil {
			t.Fatal(err)
		}
		ic, _ := x509.ParseCertificate(iDER)
		lk := newKey(t)
		leafTmpl := &x509.Certificate{SerialNumber: big.NewInt(12), DNSNames: []string{"irc.example.org"},
			NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		lDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ic, &lk.PublicKey, ik)
		if err != nil {
			t.Fatal(err)
		}
		lc, _ := x509.ParseCertificate(lDER)
		pool := x509.NewCertPool()
		pool.AddCert(rc)

		addr := serveKey(t, lk, lc, ic)
		if _, _, err := dial(t, addr, "irc.example.org", pki, pool); err != nil {
			t.Fatalf("chain with intermediate refused: %v", err)
		}
		// without the intermediate the chain can't be built
		addr = serveKey(t, lk, lc)
		_, _, err = dial(t, addr, "irc.example.org", pki, pool)
		var ue *UntrustedCertError
		if !errors.As(err, &ue) || ue.Reason != "signed by an unknown authority" {
			t.Fatalf("leaf without intermediate: %v", err)
		}
	})

	for _, c := range []struct {
		name, host string
		p          testPKI
		pool       *x509.CertPool
		reason     string
	}{
		{"hostname mismatch", "irc.other.org", good, good.pool, "it's for a different host name"},
		{"unknown authority", "irc.example.org", good, x509.NewCertPool(), "signed by an unknown authority"},
		{"expired", "irc.example.org", newPKI(t, "irc.example.org", time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour)), nil, "expired or not yet valid"},
		{"not yet valid", "irc.example.org", newPKI(t, "irc.example.org", time.Now().Add(time.Hour), time.Now().Add(2*time.Hour)), nil, "expired or not yet valid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			pool := c.pool
			if pool == nil {
				pool = c.p.pool
			}
			addr := serveKey(t, c.p.leafKey, c.p.leaf)
			_, told, err := dial(t, addr, c.host, pki, pool)
			var ue *UntrustedCertError
			if !errors.As(err, &ue) {
				t.Fatalf("want UntrustedCertError, got %v", err)
			}
			fp := Fingerprint(c.p.leaf)
			if ue.Reason != c.reason || ue.Fingerprint != fp || told != fp {
				t.Fatalf("reason %q fp %q told %q; want %q %q", ue.Reason, ue.Fingerprint, told, c.reason, fp)
			}
			// adding the fingerprint it reported makes it trusted
			if _, _, err := dial(t, addr, c.host, Trust{TrustPKI: true, Fingerprints: []string{ue.Fingerprint}}, pool); err != nil {
				t.Fatalf("reported fingerprint not accepted: %v", err)
			}
		})
	}
}

func TestVerifyWithoutCertificate(t *testing.T) {
	err := Config("h", Trust{TrustAll: true}, func(string) { t.Fatal("onUntrusted called") }).VerifyConnection(tls.ConnectionState{})
	var ue *UntrustedCertError
	if !errors.As(err, &ue) || ue.Reason != "no certificate" || ue.Fingerprint != "" {
		t.Fatalf("got %v", err)
	}
}

func TestShortCertErr(t *testing.T) {
	p := validPKI(t, "a.example")
	for _, c := range []struct {
		err  error
		want string
	}{
		{x509.HostnameError{Certificate: p.leaf, Host: "b.example"}, "it's for a different host name"},
		{x509.UnknownAuthorityError{}, "signed by an unknown authority"},
		{x509.CertificateInvalidError{Reason: x509.Expired}, "expired or not yet valid"},
		{x509.CertificateInvalidError{Reason: x509.NotAuthorizedToSign}, "invalid"},
		{errors.New("something else"), "something else"},
	} {
		if got := shortCertErr(c.err); got != c.want {
			t.Errorf("%T: %q, want %q", c.err, got, c.want)
		}
	}
}

// slowPlainServer writes the parts with a pause between them.
func slowPlainServer(t *testing.T, parts ...string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		for i, p := range parts {
			if i > 0 {
				time.Sleep(20 * time.Millisecond)
			}
			c.Write([]byte(p))
		}
		time.Sleep(50 * time.Millisecond)
	}()
	return ln.Addr().String()
}

func TestHandshakePlainTextEdgeCases(t *testing.T) {
	long := strings.Repeat("x", 300)
	for _, c := range []struct {
		name  string
		parts []string
		check func(error) bool
	}{
		{"line arrives in two parts", []string{"ERROR :Closing", " link: too many connections\r\n"},
			func(err error) bool {
				return strings.Contains(err.Error(), "refused the connection: Closing link: too many connections")
			}},
		{"long line is cut", []string{"NOTICE " + long + "\r\n"},
			func(err error) bool {
				return strings.Contains(err.Error(), "NOTICE "+long[:193]+"…") && !strings.Contains(err.Error(), long[:194])
			}},
		{"binary garbage keeps crypto/tls's error", []string{"\x01\x02\x03\x04\x05\x06\x07\x08\r\n"},
			func(err error) bool {
				var rh tls.RecordHeaderError
				return errors.As(err, &rh)
			}},
		{"leading newlines are skipped", []string{"\r\n\r\nERROR :go away\r\n"},
			func(err error) bool { return strings.Contains(err.Error(), "refused the connection: go away") }},
	} {
		t.Run(c.name, func(t *testing.T) {
			raw, err := net.Dial("tcp", slowPlainServer(t, c.parts...))
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			_, err = Handshake(context.Background(), raw, Config("irc.example.org", Trust{TrustPKI: true}, nil))
			if err == nil || !c.check(err) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestHandshakeHangUp(t *testing.T) {
	// the server closes without a word: it hung up, said plainly
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			c.Close()
		}
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_, err = Handshake(context.Background(), raw, Config("irc.example.org", Trust{TrustAll: true}, nil))
	if !errors.Is(err, ErrHungUp) {
		t.Fatalf("got %v", err)
	}
}

func TestRecorderStopsAndCaps(t *testing.T) {
	r := &recorder{}
	r.add(make([]byte, 400))
	r.add(make([]byte, 400))
	if len(r.first()) != 512 {
		t.Fatalf("recorded %d bytes, want 512", len(r.first()))
	}
	r.stop()
	r.add([]byte("more"))
	if len(r.first()) != 0 {
		t.Fatalf("recorded %d bytes after stop", len(r.first()))
	}
}
