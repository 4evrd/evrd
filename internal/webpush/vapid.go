// Package webpush is IRCv3 draft/webpush on evrd's side: the VAPID key it
// signs with, the subscriptions clients register (WEBPUSH REGISTER), and the
// pushes for DMs and highlights that arrive while none of the account's
// clients is attached.
//
// A push is a standard Web Push request: the IRC line, encrypted to the
// subscription's key (RFC 8291, aes128gcm), signed with evrd's VAPID key
// (RFC 8292), POSTed to the endpoint (RFC 8030). It works like Ergo's, so
// clients (the Boson app, Goguma) work against both.
//
// https://github.com/ircv3/ircv3-specifications/pull/471
package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Keys is evrd's VAPID key pair (P-256): pushes are signed with it, and
// clients get the public half in ISUPPORT (VAPID=).
type Keys struct {
	priv *ecdsa.PrivateKey
	pub  string // the uncompressed point, base64url without padding
}

// GenerateKeys makes a new VAPID key pair.
func GenerateKeys() (*Keys, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return keysOf(priv)
}

func keysOf(priv *ecdsa.PrivateKey) (*Keys, error) {
	if priv.Curve != elliptic.P256() {
		return nil, errors.New("webpush: a VAPID key is on P-256")
	}
	pub, err := priv.PublicKey.ECDH()
	if err != nil {
		return nil, err
	}
	return &Keys{priv: priv, pub: base64.RawURLEncoding.EncodeToString(pub.Bytes())}, nil
}

// PublicKey is the public key as ISUPPORT VAPID= and browsers'
// applicationServerKey take it: the uncompressed point, base64url.
func (k *Keys) PublicKey() string { return k.pub }

// LoadOrCreateKeys reads the VAPID key from path (a PEM PKCS #8 private
// key), making it the first time (created). The key stays the same from then
// on: subscriptions are bound to it.
func LoadOrCreateKeys(path string) (k *Keys, created bool, err error) {
	b, err := os.ReadFile(path)
	if err == nil {
		k, err = parseKeys(b)
		if err != nil {
			return nil, false, fmt.Errorf("webpush: %s: %w", path, err)
		}
		return k, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	k, err = GenerateKeys()
	if err != nil {
		return nil, false, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(k.priv)
	if err != nil {
		return nil, false, err
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, false, err
		}
	}
	data := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	// O_EXCL: two processes starting at once keep the first one's
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return LoadOrCreateKeys(path)
	}
	if err != nil {
		return nil, false, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return nil, false, err
	}
	if err := f.Close(); err != nil {
		return nil, false, err
	}
	return k, true, nil
}

func parseKeys(b []byte) (*Keys, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("not a PEM private key")
	}
	key, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		if ec, err2 := x509.ParseECPrivateKey(blk.Bytes); err2 == nil {
			return keysOf(ec)
		}
		return nil, err
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("a %T, not an ECDSA key", key)
	}
	return keysOf(ec)
}

// jwtLife is how long a VAPID token is good for (push services take up to
// 24 hours).
const jwtLife = 12 * time.Hour

// authorization is the Authorization header for a push to endpoint
// (RFC 8292): "vapid t=<JWT>, k=<public key>". The JWT's audience is the
// endpoint's origin, its subject who to contact about evrd's pushes (an
// https: or mailto: URL).
func (k *Keys) authorization(endpoint, subscriber string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(subscriber, "https:") && !strings.HasPrefix(subscriber, "mailto:") {
		subscriber = "mailto:" + subscriber
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(jwtLife).Unix(),
		"sub": subscriber,
	})
	if err != nil {
		return "", err
	}
	signing := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64) // ES256: r and s, 32 bytes each (RFC 7518)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + base64.RawURLEncoding.EncodeToString(sig) + ", k=" + k.pub, nil
}
