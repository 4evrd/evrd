package webpush

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
)

// SubKeys are a subscription's keys, as the client sent them: its P-256
// public key and 16-byte auth secret.
type SubKeys struct {
	P256DH *ecdh.PublicKey
	Auth   []byte
}

// ErrInvalidKeys means WEBPUSH REGISTER's keys aren't a P-256 key and a
// 16-byte auth secret.
var ErrInvalidKeys = errors.New("webpush: invalid subscription keys")

// ParseKeys reads WEBPUSH REGISTER's keys parameter, tag-encoded:
// "p256dh=<base64url>;auth=<base64url>" (either order; padded or not, and
// standard base64 is taken too).
func ParseKeys(param string) (SubKeys, error) {
	var p256, auth string
	for _, kv := range strings.Split(param, ";") {
		k, v, _ := strings.Cut(kv, "=")
		switch k {
		case "p256dh":
			p256 = v
		case "auth":
			auth = v
		}
	}
	return DecodeKeys(p256, auth)
}

// DecodeKeys is ParseKeys for the two keys apart, as they're stored.
func DecodeKeys(p256dh, auth string) (SubKeys, error) {
	a, err := decodeKey(auth)
	if err != nil || len(a) != 16 {
		return SubKeys{}, ErrInvalidKeys
	}
	raw, err := decodeKey(p256dh)
	if err != nil {
		return SubKeys{}, ErrInvalidKeys
	}
	pub, err := ecdh.P256().NewPublicKey(raw)
	if err != nil {
		return SubKeys{}, ErrInvalidKeys
	}
	return SubKeys{P256DH: pub, Auth: a}, nil
}

func decodeKey(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if s == "" {
		return nil, ErrInvalidKeys
	}
	if strings.ContainsAny(s, "+/") {
		return base64.RawStdEncoding.DecodeString(s)
	}
	return base64.RawURLEncoding.DecodeString(s)
}

// Strings are the keys in their stored form (base64url, unpadded).
func (k SubKeys) Strings() (p256dh, auth string) {
	return base64.RawURLEncoding.EncodeToString(k.P256DH.Bytes()), base64.RawURLEncoding.EncodeToString(k.Auth)
}

// recordSize is the aes128gcm record size evrd declares; a push is one
// record, so this bounds a push's body.
const recordSize = 4096

// headerLen is the aes128gcm header: salt (16), record size (4), key id
// length (1) and the key id, evrd's ephemeral public key (65).
const headerLen = 16 + 4 + 1 + 65

// MaxPayload is the most a push may carry: one record of recordSize, less
// the header, the padding delimiter and the AEAD tag.
const MaxPayload = recordSize - headerLen - 1 - 16

// ErrTooLong means a payload doesn't fit one record.
var ErrTooLong = errors.New("webpush: payload too long")

// encrypt encrypts msg to the subscription's keys (RFC 8291 with the
// aes128gcm content coding, RFC 8188), as the body of a push. The record
// isn't padded: the body is as long as the line needs.
func encrypt(msg []byte, keys SubKeys) ([]byte, error) {
	if len(msg) > MaxPayload {
		return nil, ErrTooLong
	}
	eph, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	return seal(msg, keys, eph, salt)
}

func seal(msg []byte, keys SubKeys, eph *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	shared, err := eph.ECDH(keys.P256DH)
	if err != nil {
		return nil, err
	}
	ephPub := eph.PublicKey().Bytes()
	// IKM = HKDF(auth, ECDH, "WebPush: info\0" || ua_public || as_public, 32)
	info := append(append([]byte("WebPush: info\x00"), keys.P256DH.Bytes()...), ephPub...)
	ikm, err := hkdf.Key(sha256.New, shared, keys.Auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(salt)
	binary.Write(&out, binary.BigEndian, uint32(recordSize))
	out.WriteByte(byte(len(ephPub)))
	out.Write(ephPub)
	// the last (only) record: the content, then the delimiter 0x02
	plain := append(append(make([]byte, 0, len(msg)+1), msg...), 0x02)
	out.Write(gcm.Seal(nil, nonce, plain, nil))
	return out.Bytes(), nil
}
