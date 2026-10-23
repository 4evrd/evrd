package client

// sts (IRCv3 strict transport security): clients on a plain listener are
// told the TLS port, and clients on TLS how long to keep to it. A client
// that saw it won't connect without TLS until the duration runs out, so
// evrd offers it only with a certificate clients trust (not a self-signed
// one) for irc_host, and a TLS listener to go to.

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
)

type stsState struct {
	mu       sync.Mutex
	duration time.Duration
	host     string
	port     int            // the first ircs:// listener's
	roots    *x509.CertPool // nil: the system's (tests set their own)
	checked  time.Time      // when the certificate was last checked
	valid    bool
}

// stsRecheck is how often the certificate is checked again (it's renewed
// or replaced while evrd runs).
const stsRecheck = 10 * time.Minute

// SetSTS sets how long clients keep to TLS (0: sts off) and the host name
// the certificate must be good for.
func (s *Server) SetSTS(d time.Duration, host string) {
	s.sts.mu.Lock()
	defer s.sts.mu.Unlock()
	if host != s.sts.host {
		s.sts.checked = time.Time{}
	}
	s.sts.duration, s.sts.host = d, host
}

// tlsPort notes a TLS listener's port, for sts.
func (s *Server) tlsPort(addr string) {
	_, port, _ := net.SplitHostPort(addr)
	p, err := strconv.Atoi(port)
	if err != nil {
		return
	}
	s.sts.mu.Lock()
	if s.sts.port == 0 {
		s.sts.port = p
	}
	s.sts.mu.Unlock()
}

// stsValue is the sts capability's value for a client on a TLS connection
// or not, "" when it isn't offered.
func (s *Server) stsValue(secure bool) string {
	s.sts.mu.Lock()
	defer s.sts.mu.Unlock()
	if s.sts.duration <= 0 || s.sts.port == 0 {
		return ""
	}
	if time.Since(s.sts.checked) > stsRecheck {
		s.sts.valid, s.sts.checked = s.trusted(), time.Now()
	}
	switch {
	case !s.sts.valid:
		return ""
	case secure:
		return fmt.Sprintf("duration=%d", int(s.sts.duration.Seconds()))
	}
	return fmt.Sprintf("port=%d", s.sts.port)
}

// trusted reports whether the IRC certificate is one clients accept for
// the host: it chains to a trusted root. Holds s.sts.mu.
func (s *Server) trusted() bool {
	conf, err := s.tlsConfig()
	if err != nil || conf == nil {
		return false
	}
	var cert *tls.Certificate
	if conf.GetCertificate != nil {
		cert, err = conf.GetCertificate(&tls.ClientHelloInfo{ServerName: s.sts.host})
	} else if len(conf.Certificates) > 0 {
		cert = &conf.Certificates[0]
	}
	if err != nil || cert == nil || len(cert.Certificate) == 0 {
		return false
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return false
	}
	inter := x509.NewCertPool()
	for _, der := range cert.Certificate[1:] {
		if c, err := x509.ParseCertificate(der); err == nil {
			inter.AddCert(c)
		}
	}
	_, err = leaf.Verify(x509.VerifyOptions{DNSName: s.sts.host, Roots: s.sts.roots, Intermediates: inter})
	return err == nil
}

// secure reports whether the client came over TLS.
func (se *session) secure() bool {
	if sc, ok := se.conn.(interface{ Secure() bool }); ok {
		return sc.Secure()
	}
	_, ok := se.conn.(*tls.Conn)
	return ok
}
