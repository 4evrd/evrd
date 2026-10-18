// Package irctls checks an IRC server's certificate the way ZNC does: a
// listed fingerprint always wins, then "trust all", then normal PKI
// validation. It's shared by evrd's upstream connections and the connection
// holder (evrd-links), and kept free of the rest of evrd so the holder
// rarely changes.
package irctls

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
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
// the fingerprint of a refused certificate.
func Config(host string, t Trust, onUntrusted func(fingerprint string)) *tls.Config {
	return &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // verified below
		VerifyConnection: func(cs tls.ConnectionState) error {
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
				opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
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
		},
	}
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
