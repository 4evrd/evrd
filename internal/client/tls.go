package client

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"time"
)

// tlsConfig returns the TLS config for ircs:// listeners, computed once. It
// loads the configured cert/key, or generates an in-memory self-signed cert
// (fine for dev; production should supply a real cert).
func (s *Server) tlsConfig() (*tls.Config, error) {
	s.tlsOnce.Do(func() {
		var cert tls.Certificate
		if s.tlsCert != "" && s.tlsKey != "" {
			cert, s.tlsErr = tls.LoadX509KeyPair(s.tlsCert, s.tlsKey)
		} else {
			s.log.Warn("no TLS cert configured for ircs listener — using a self-signed cert")
			cert, s.tlsErr = selfSignedCert()
		}
		if s.tlsErr == nil {
			s.tlsConf = &tls.Config{Certificates: []tls.Certificate{cert}}
		}
	})
	return s.tlsConf, s.tlsErr
}

func selfSignedCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "evrd"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost", "evrd"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
