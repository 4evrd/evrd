// Package certs hands out the TLS certificate for evrd's own TLS listeners:
// the IRC ports, and the web app when evrd serves HTTPS itself. It comes
// from one of three sources, switchable while evrd runs:
//
//   - files: tls_cert and tls_key, read at startup (before evrd chroots) and
//     again when they change, where evrd can still see them;
//   - acme: Let's Encrypt (or another ACME CA), got on first use and renewed
//     before it expires, answering the CA on the HTTPS port (tls-alpn-01) or
//     on http_listen (http-01);
//   - selfsigned: made once and kept.
//
// ACME's account and certificates and the self-signed certificate live in
// the database, encrypted like other secrets, so every node of a cluster
// serves the same ones and nothing needs writing inside the chroot.
package certs

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

// acmeHTTPClient talks to the CA (nil: the default). Tests set it to trust
// a test CA's own roots.
var acmeHTTPClient *http.Client

// Manager serves certificates from the configured source.
type Manager struct {
	log *slog.Logger

	mu     sync.RWMutex
	st     store.Store
	source string
	hosts  []string
	email  string

	acmeNames []string // the hosts a public CA will sign
	ca        string
	acme      *autocert.Manager

	certPath, keyPath string // as configured
	pathMap           func(string) string
	files             *tls.Certificate
	filesMod          time.Time
	filesErr          error

	self    *tls.Certificate
	selfFor string // the hosts it was made for

	errs map[string]string // host → the last failure getting its certificate
}

// New makes a Manager; LoadFiles and Apply configure it.
func New(log *slog.Logger) *Manager {
	return &Manager{log: log, errs: map[string]string{}}
}

// LoadFiles reads tls_cert and tls_key. Call it before evrd chroots, while
// the files are in reach; Apply keeps what it read if they're out of reach
// later.
func (m *Manager) LoadFiles(certPath, keyPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadFilesLocked(certPath, keyPath)
}

// SetPathMap maps tls_cert/tls_key to where they can be read from now —
// inside the chroot, once evrd has entered it (nil: as configured).
func (m *Manager) SetPathMap(f func(string) string) {
	m.mu.Lock()
	m.pathMap = f
	m.mu.Unlock()
}

func (m *Manager) mapped(p string) string {
	if m.pathMap == nil || p == "" {
		return p
	}
	return m.pathMap(p)
}

// Reload re-reads tls_cert and tls_key now (SIGHUP, after a renewal), keeping
// the current certificate if they can't be read.
func (m *Manager) Reload() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.certPath == "" {
		return nil
	}
	if err := m.loadFilesLocked(m.certPath, m.keyPath); err != nil {
		return err
	}
	m.log.Info("reloaded tls_cert", "path", m.mapped(m.certPath))
	return nil
}

func (m *Manager) loadFilesLocked(certPath, keyPath string) error {
	m.certPath, m.keyPath = certPath, keyPath
	if certPath == "" || keyPath == "" {
		m.files, m.filesErr = nil, nil
		return nil
	}
	cert, err := tls.LoadX509KeyPair(m.mapped(certPath), m.mapped(keyPath))
	if err != nil {
		m.filesErr = err
		return err
	}
	m.files, m.filesErr = &cert, nil
	if st, err := os.Stat(m.mapped(certPath)); err == nil {
		m.filesMod = st.ModTime()
	}
	return nil
}

// SetStore gives the Manager the database to keep certificates in. Call it
// before Apply.
func (m *Manager) SetStore(st store.Store) {
	m.mu.Lock()
	m.st = st
	m.mu.Unlock()
}

// Apply switches to c's source and settings. It's cheap when nothing
// changed, so it can run on every settings change.
func (m *Manager) Apply(c *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	source, hosts, acmeNames := c.TLSSource(), c.CertHosts(), c.ACMEHosts()
	if c.TLSCert != m.certPath || c.TLSKey != m.keyPath {
		if err := m.loadFilesLocked(c.TLSCert, c.TLSKey); err != nil {
			m.log.Warn("tls_cert: can't read it (outside the chroot? restart evrd)", "err", err)
		}
	}
	changed := source != m.source || !slices.Equal(hosts, m.hosts) || c.TLS.ACMEEmail != m.email || c.TLS.ACMECA != m.ca
	m.source, m.hosts, m.email, m.ca = source, hosts, c.TLS.ACMEEmail, c.TLS.ACMECA
	m.acmeNames = acmeNames
	if !changed {
		return nil
	}
	m.errs = map[string]string{}
	m.acme = nil
	switch source {
	case "acme":
		if err := c.CheckTLS(); err != nil {
			return err
		}
		names := c.ACMEHosts()
		dir := c.TLS.ACMECA
		if dir == "" {
			dir = acme.LetsEncryptURL
		}
		m.acme = &autocert.Manager{
			Prompt:      autocert.AcceptTOS,
			Cache:       dbCache{m},
			HostPolicy:  autocert.HostWhitelist(names...),
			Email:       c.TLS.ACMEEmail,
			Client:      &acme.Client{DirectoryURL: dir, HTTPClient: acmeHTTPClient},
			RenewBefore: 30 * 24 * time.Hour,
		}
		m.log.Info("certificates from ACME", "ca", dir, "hosts", names)
	case "selfsigned":
		if err := m.selfSignedLocked(); err != nil {
			return err
		}
	case "files":
		if m.files == nil {
			return fmt.Errorf("tls files: tls_cert and tls_key: %v", m.filesErr)
		}
	}
	return nil
}

// GetCertificate picks the certificate for a TLS handshake.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	source, am, files, self, names := m.source, m.acme, m.files, m.self, m.acmeNames
	m.mu.RUnlock()
	switch source {
	case "files":
		if files == nil {
			return nil, errors.New("no certificate: tls_cert and tls_key couldn't be read")
		}
		return files, nil
	case "acme":
		if am == nil {
			return nil, errors.New("no certificate: ACME isn't set up")
		}
		if hello.ServerName == "" {
			// IRC clients often connect by address, without SNI: give them
			// the first host's certificate.
			if len(names) == 0 {
				return nil, errors.New("no certificate: no host names")
			}
			h := *hello
			h.ServerName = names[0]
			hello = &h
		}
		cert, err := am.GetCertificate(hello)
		m.mu.Lock()
		if err != nil {
			if !strings.Contains(err.Error(), "not configured in HostWhitelist") {
				m.errs[hello.ServerName] = err.Error()
			}
		} else {
			delete(m.errs, hello.ServerName)
		}
		m.mu.Unlock()
		return cert, err
	default:
		if self == nil {
			return nil, errors.New("no certificate")
		}
		return self, nil
	}
}

// TLSConfig is for evrd's HTTPS listener: HTTP/2, and Let's Encrypt's
// tls-alpn-01 challenge.
func (m *Manager) TLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: m.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1", acme.ALPNProto},
		MinVersion:     tls.VersionTLS12,
	}
}

// IRCConfig is for the ircs:// listeners.
func (m *Manager) IRCConfig() *tls.Config {
	return &tls.Config{GetCertificate: m.GetCertificate, NextProtos: []string{acme.ALPNProto}}
}

// HTTPHandler is for http_listen: it answers ACME's http-01 challenges and
// sends everything else to HTTPS on httpsAddr's port.
func (m *Manager) HTTPHandler(httpsAddr string) http.Handler {
	port := ""
	if _, p, err := net.SplitHostPort(httpsAddr); err == nil && p != "443" {
		port = ":" + p
	}
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		http.Redirect(w, r, "https://"+host+port+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		am := m.acme
		m.mu.RUnlock()
		if am != nil {
			am.HTTPHandler(redirect).ServeHTTP(w, r)
			return
		}
		redirect.ServeHTTP(w, r)
	})
}

// Run keeps certificates fresh until ctx ends: it re-reads tls_cert when
// the file changes (acme-client renewing it, say) and gets ACME
// certificates ahead of the first visitor.
func (m *Manager) Run(ctx context.Context) {
	warm := time.NewTimer(5 * time.Second) // let the listeners come up first
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-warm.C:
			m.prefetch()
		case <-tick.C:
			m.reloadFiles()
			m.prefetch()
		}
	}
}

func (m *Manager) reloadFiles() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.certPath == "" {
		return
	}
	st, err := os.Stat(m.mapped(m.certPath))
	if err != nil || !st.ModTime().After(m.filesMod) {
		return // unchanged, or out of reach in the chroot
	}
	if err := m.loadFilesLocked(m.certPath, m.keyPath); err != nil {
		m.log.Warn("reload tls_cert", "err", err)
		return
	}
	m.log.Info("reloaded tls_cert", "path", m.mapped(m.certPath))
}

func (m *Manager) prefetch() {
	m.mu.RLock()
	source, hosts := m.source, m.acmeNames
	m.mu.RUnlock()
	if source != "acme" {
		return
	}
	for _, h := range hosts {
		// as a modern client asks: for an ECDSA certificate
		hello := &tls.ClientHelloInfo{
			ServerName:        h,
			SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
			SupportedCurves:   []tls.CurveID{tls.CurveP256},
			CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
			SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		}
		if _, err := m.GetCertificate(hello); err != nil {
			m.log.Warn("get certificate", "host", h, "err", err)
		}
	}
}

// selfSignedLocked loads the self-signed certificate for the current hosts
// from the database, or makes and saves one.
func (m *Manager) selfSignedLocked() error {
	hosts := append([]string{}, m.hosts...)
	if !slices.Contains(hosts, "localhost") {
		hosts = append(hosts, "localhost")
	}
	sum := sha256.Sum256([]byte(strings.Join(hosts, " ")))
	key := "selfsigned+" + hex.EncodeToString(sum[:8])
	if m.self != nil && m.selfFor == key {
		return nil
	}
	if m.st != nil {
		if data, err := m.st.CertCache(key); err == nil {
			if cert, err := parsePEM([]byte(data)); err == nil && time.Now().Before(cert.Leaf.NotAfter.Add(-30*24*time.Hour)) {
				m.self, m.selfFor = cert, key
				return nil
			}
		}
	}
	data, err := makeSelfSigned(hosts)
	if err != nil {
		return err
	}
	cert, err := parsePEM(data)
	if err != nil {
		return err
	}
	if m.st != nil {
		if err := m.st.PutCertCache(key, string(data)); err != nil {
			m.log.Warn("save the self-signed certificate", "err", err)
		}
	}
	m.self, m.selfFor = cert, key
	m.log.Info("made a self-signed certificate", "hosts", hosts)
	return nil
}

func makeSelfSigned(hosts []string) ([]byte, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: hosts[0], Organization: []string{"evrd"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	kder, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	out := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder})
	return append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...), nil
}

// parsePEM reads a key and certificate chain from one PEM blob (autocert's
// cache format too).
func parsePEM(data []byte) (*tls.Certificate, error) {
	var keyDER []byte
	var chain [][]byte
	for {
		var b *pem.Block
		b, data = pem.Decode(data)
		if b == nil {
			break
		}
		switch {
		case strings.Contains(b.Type, "PRIVATE KEY"):
			keyDER = b.Bytes
		case b.Type == "CERTIFICATE":
			chain = append(chain, b.Bytes)
		}
	}
	if keyDER == nil || len(chain) == 0 {
		return nil, errors.New("no key and certificate")
	}
	var key crypto.Signer
	if k, err := x509.ParsePKCS8PrivateKey(keyDER); err == nil {
		key, _ = k.(crypto.Signer)
	} else if k, err := x509.ParseECPrivateKey(keyDER); err == nil {
		key = k
	} else if k, err := x509.ParsePKCS1PrivateKey(keyDER); err == nil {
		key = k
	}
	if key == nil {
		return nil, errors.New("unreadable private key")
	}
	leaf, err := x509.ParseCertificate(chain[0])
	if err != nil {
		return nil, err
	}
	return &tls.Certificate{Certificate: chain, PrivateKey: key, Leaf: leaf}, nil
}

// Status is a certificate as the admin page shows it.
type Status struct {
	Host     string   `json:"host"`
	Subject  string   `json:"subject,omitempty"`
	Issuer   string   `json:"issuer,omitempty"`
	Names    []string `json:"names,omitempty"`
	NotAfter int64    `json:"notAfter,omitempty"`
	Error    string   `json:"error,omitempty"`
	Pending  bool     `json:"pending,omitempty"` // ACME: not got yet
}

// Report says where certificates come from and what each host gets.
type Report struct {
	Source string   `json:"source"`
	Hosts  []string `json:"hosts"`
	Certs  []Status `json:"certs"`
}

// Report describes the certificates in use, without getting any.
func (m *Manager) Report() Report {
	m.mu.RLock()
	defer m.mu.RUnlock()
	r := Report{Source: m.source, Hosts: append([]string{}, m.hosts...)}
	describe := func(host string, c *tls.Certificate) Status {
		s := Status{Host: host}
		if c == nil || c.Leaf == nil {
			return s
		}
		s.Subject, s.Issuer = c.Leaf.Subject.CommonName, issuerName(c.Leaf)
		s.Names, s.NotAfter = c.Leaf.DNSNames, c.Leaf.NotAfter.Unix()
		return s
	}
	switch m.source {
	case "files":
		s := describe(m.certPath, m.files)
		if m.filesErr != nil {
			s.Error = m.filesErr.Error()
		}
		r.Certs = append(r.Certs, s)
	case "selfsigned":
		r.Certs = append(r.Certs, describe(strings.Join(m.hosts, " "), m.self))
	case "acme":
		for _, h := range m.acmeNames {
			s := Status{Host: h, Pending: true}
			if m.st != nil {
				// autocert keys ECDSA certificates by the bare name, RSA
				// ones (for clients without ECDSA) with "+rsa"
				for _, k := range []string{h, h + "+rsa"} {
					if data, err := m.st.CertCache(k); err == nil {
						if c, err := parsePEM([]byte(data)); err == nil {
							s = describe(h, c)
							break
						}
					}
				}
			}
			s.Error = m.errs[h]
			r.Certs = append(r.Certs, s)
		}
	}
	return r
}

func issuerName(c *x509.Certificate) string {
	if c.Issuer.CommonName == c.Subject.CommonName && slices.Equal(c.Issuer.Organization, c.Subject.Organization) {
		return "self-signed"
	}
	n := c.Issuer.CommonName
	if len(c.Issuer.Organization) > 0 {
		n = c.Issuer.Organization[0] + " " + n
	}
	return n
}

// dbCache is autocert's cache, in the database.
type dbCache struct{ m *Manager }

func (c dbCache) store() store.Store {
	c.m.mu.RLock()
	defer c.m.mu.RUnlock()
	return c.m.st
}

func (c dbCache) Get(_ context.Context, key string) ([]byte, error) {
	st := c.store()
	if st == nil {
		return nil, autocert.ErrCacheMiss
	}
	data, err := st.CertCache(key)
	if errors.Is(err, store.ErrNotFound) {
		return nil, autocert.ErrCacheMiss
	}
	return []byte(data), err
}

func (c dbCache) Put(_ context.Context, key string, data []byte) error {
	st := c.store()
	if st == nil {
		return errors.New("no database")
	}
	return st.PutCertCache(key, string(data))
}

func (c dbCache) Delete(_ context.Context, key string) error {
	st := c.store()
	if st == nil {
		return nil
	}
	return st.DeleteCertCache(key)
}
