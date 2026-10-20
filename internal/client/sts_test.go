package client

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

// testCA makes a CA and a certificate it signed for host.
func testCA(t *testing.T, host string) (*x509.CertPool, tls.Certificate) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ = x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: host}, DNSNames: []string{host},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pool, tls.Certificate{Certificate: [][]byte{der, caDER}, PrivateKey: key}
}

// capLS is the CAP LS 302 reply a fresh connection gets.
func capLS(t *testing.T, c net.Conn) string {
	t.Helper()
	ic := &ircClient{t: t, c: c, sc: bufio.NewScanner(c)}
	ic.send("CAP LS 302")
	return ic.expect(" LS ")
}

func TestSTS(t *testing.T) {
	r := newRig(t)
	roots, cert := testCA(t, "irc.example.org")
	r.srv.SetTLSConfig(&tls.Config{Certificates: []tls.Certificate{cert}})
	r.srv.sts.roots = roots
	r.srv.SetSTS(24*time.Hour, "irc.example.org")
	plainLS := func() string {
		return capLS(t, dialUntil(t, func() (net.Conn, error) { return net.Dial("tcp", r.addr) }))
	}

	// no TLS listener: nowhere to send clients
	if l := plainLS(); strings.Contains(l, "sts") {
		t.Fatalf("without a TLS listener: %q", l)
	}
	tlsAddr := freeAddr(t)
	go r.srv.listen(r.ctx, "ircs://"+tlsAddr)
	tc := dialUntil(t, func() (net.Conn, error) { return tls.Dial("tcp", tlsAddr, &tls.Config{InsecureSkipVerify: true}) })
	_, port, _ := net.SplitHostPort(tlsAddr)
	if l := plainLS(); !strings.Contains(l, " sts=port="+port) {
		t.Fatalf("plain: %q", l)
	}
	if l := capLS(t, tc); !strings.Contains(l, " sts=duration=86400") {
		t.Fatalf("TLS: %q", l)
	}
	// it can't be asked for
	ic := &ircClient{t: t, c: tc, sc: bufio.NewScanner(tc)}
	ic.send("CAP REQ :sts")
	ic.expect("NAK :sts")

	// a certificate not for irc_host, or not trusted, or sts off: none
	r.srv.SetSTS(24*time.Hour, "other.example.org")
	if l := plainLS(); strings.Contains(l, "sts") {
		t.Fatalf("wrong host: %q", l)
	}
	r.srv.sts.mu.Lock()
	r.srv.sts.roots, r.srv.sts.host, r.srv.sts.checked = x509.NewCertPool(), "irc.example.org", time.Time{}
	r.srv.sts.mu.Unlock()
	if l := plainLS(); strings.Contains(l, "sts") {
		t.Fatalf("untrusted: %q", l)
	}
	r.srv.SetSTS(0, "irc.example.org")
	if l := plainLS(); strings.Contains(l, "sts") {
		t.Fatalf("off: %q", l)
	}
}
