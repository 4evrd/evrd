// Package irctls checks an IRC server's certificate the way ZNC does: a
// listed fingerprint always wins, then "trust all", then normal PKI
// validation. It's shared by evrd's upstream connections and the connection
// holder (evrd-links), and kept free of the rest of evrd so the holder
// rarely changes.
package irctls

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Trust is how a network's certificates are checked.
type Trust struct {
	Fingerprints []string `json:"fingerprints,omitempty"` // SHA-256, lowercase hex
	TrustAll     bool     `json:"trustAll,omitempty"`
	TrustPKI     bool     `json:"trustPKI,omitempty"`
}

// UntrustedCertError is returned when a server's certificate isn't trusted.
// Fingerprint is what to add to the network's trusted fingerprints to accept
// it anyway.
type UntrustedCertError struct {
	Fingerprint string // SHA-256 of the leaf certificate, lowercase hex
	Reason      string
}

func (e *UntrustedCertError) Error() string {
	return fmt.Sprintf("untrusted certificate (%s) — SHA-256 %s", e.Reason, ColonHex(e.Fingerprint))
}

// ColonHex formats a hex fingerprint as AB:CD:… the way ZNC shows them.
func ColonHex(h string) string {
	h = strings.ToUpper(h)
	var b strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i:min(i+2, len(h))])
	}
	return b.String()
}

// Fingerprint is the SHA-256 of a certificate, lowercase hex.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// Config is the TLS client config for host. onUntrusted, if set, is told
// the fingerprint of a refused certificate. PKI validation uses the
// config's RootCAs: nil, the system roots, unless the caller sets it.
func Config(host string, t Trust, onUntrusted func(fingerprint string)) *tls.Config {
	cfg := &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // verified below
	}
	cfg.VerifyConnection = func(cs tls.ConnectionState) error {
		if len(cs.PeerCertificates) == 0 {
			return &UntrustedCertError{Reason: "no certificate"}
		}
		leaf := cs.PeerCertificates[0]
		fp := Fingerprint(leaf)
		if slices.Contains(t.Fingerprints, fp) || t.TrustAll {
			return nil
		}
		reason := "certificate validation is off for this network"
		if t.TrustPKI {
			opts := x509.VerifyOptions{DNSName: host, Roots: cfg.RootCAs, Intermediates: x509.NewCertPool()}
			for _, ic := range cs.PeerCertificates[1:] {
				opts.Intermediates.AddCert(ic)
			}
			_, err := leaf.Verify(opts)
			if err == nil {
				return nil
			}
			reason = shortCertErr(err)
		}
		if onUntrusted != nil {
			onUntrusted(fp)
		}
		return &UntrustedCertError{Fingerprint: fp, Reason: reason}
	}
	return cfg
}

func shortCertErr(err error) string {
	var hn x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	switch {
	case errors.As(err, &hn):
		return "it's for a different host name"
	case errors.As(err, &ua):
		return "signed by an unknown authority"
	case errors.As(err, &ci):
		if ci.Reason == x509.Expired {
			return "expired or not yet valid"
		}
		return "invalid"
	}
	return err.Error()
}

// ErrHungUp is the server closing the connection before the TLS handshake
// finished: it may be refusing this address for now, or busy, or not speak
// TLS on that port.
var ErrHungUp = errors.New("the server hung up during the TLS handshake")

// Handshake runs the TLS handshake over raw. When the server answers in
// plain text instead (a plain-text port, or a refusal sent before any TLS:
// "ERROR :*** Banned: …"), the error says what it said rather than
// crypto/tls's "first record does not look like a TLS handshake".
func Handshake(ctx context.Context, raw net.Conn, cfg *tls.Config) (*tls.Conn, error) {
	rec := &recorder{Conn: raw}
	tc := tls.Client(rec, cfg)
	err := tc.HandshakeContext(ctx)
	if err == nil {
		rec.stop()
		return tc, nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, syscall.ECONNRESET) {
		return nil, ErrHungUp
	}
	var rh tls.RecordHeaderError
	if !errors.As(err, &rh) {
		return nil, err
	}
	// the rest of the server's line, if it hasn't all arrived yet
	raw.SetReadDeadline(time.Now().Add(time.Second))
	if !bytes.ContainsAny(rec.first(), "\r\n") {
		more := make([]byte, 256)
		n, _ := raw.Read(more)
		rec.add(more[:n])
	}
	line, _, _ := strings.Cut(strings.TrimLeft(string(rec.first()), "\r\n"), "\n")
	line = strings.TrimSpace(strings.ToValidUTF8(line, "?"))
	if line == "" || strings.IndexFunc(line, func(r rune) bool { return r < 0x20 && r != '\t' }) >= 0 {
		return nil, err
	}
	if len(line) > 200 {
		line = line[:200] + "…"
	}
	if why, ok := strings.CutPrefix(line, "ERROR :"); ok {
		return nil, fmt.Errorf("the server refused the connection: %s", why)
	}
	return nil, fmt.Errorf("the server didn't answer with TLS on this port — it said %q (is it a plain-text port?)", line)
}

// recorder keeps the first bytes read, for Handshake's error.
type recorder struct {
	net.Conn
	mu   sync.Mutex
	buf  []byte
	done bool
}

func (r *recorder) Read(p []byte) (int, error) {
	n, err := r.Conn.Read(p)
	r.add(p[:n])
	return n, err
}

func (r *recorder) add(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.done && len(r.buf) < 512 {
		r.buf = append(r.buf, b[:min(len(b), 512-len(r.buf))]...)
	}
}

func (r *recorder) first() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.buf...)
}

func (r *recorder) stop() {
	r.mu.Lock()
	r.done, r.buf = true, nil
	r.mu.Unlock()
}
