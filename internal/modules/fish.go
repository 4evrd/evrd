package modules

// FiSH's message encryption, as FiSH, mircryption, ZNC's crypt, HexChat's
// fishlim and weechat's fish.py do it: Blowfish with the key's bytes as
// the key, the text padded with zero bytes to whole 8-byte blocks, and
//
//	ECB: "+OK " and each block in FiSH's own base64, 12 characters a block
//	CBC: "+OK *" and standard base64 of a random 8-byte IV and the blocks
//
// Older clients also send "mcps " (mircryption) instead of "+OK ".

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"

	"golang.org/x/crypto/blowfish"
)

// fishB64 is FiSH's base64 alphabet for ECB.
const fishB64 = "./0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// maxKeyLen is the longest Blowfish key: 56 bytes (448 bits).
const maxKeyLen = 56

var errFishText = errors.New("not FiSH text")

// fishIV picks a CBC message's IV (tests pick their own).
var fishIV = func(iv []byte) error {
	_, err := rand.Read(iv)
	return err
}

func fishCipher(key string) (*blowfish.Cipher, error) {
	return blowfish.NewCipher([]byte(key)) // 1 to 56 bytes
}

// pad8 pads b with zero bytes to whole blocks.
func pad8(b []byte) []byte {
	if n := len(b) % 8; n != 0 {
		b = append(b, make([]byte, 8-n)...)
	}
	return b
}

// fishEncrypt encrypts text, giving "+OK …" (ECB) or "+OK *…" (CBC).
func fishEncrypt(key string, cbc bool, text string) (string, error) {
	c, err := fishCipher(key)
	if err != nil {
		return "", err
	}
	plain := pad8([]byte(text))
	if cbc {
		out := make([]byte, 8+len(plain))
		if err := fishIV(out[:8]); err != nil {
			return "", err
		}
		cipher.NewCBCEncrypter(c, out[:8]).CryptBlocks(out[8:], plain)
		return "+OK *" + base64.StdEncoding.EncodeToString(out), nil
	}
	var b strings.Builder
	b.WriteString("+OK ")
	block := make([]byte, 8)
	for i := 0; i < len(plain); i += 8 {
		c.Encrypt(block, plain[i:i+8])
		left, right := binary.BigEndian.Uint32(block[:4]), binary.BigEndian.Uint32(block[4:])
		for _, half := range []uint32{right, left} { // right half first
			for range 6 {
				b.WriteByte(fishB64[half&0x3f])
				half >>= 6
			}
		}
	}
	return b.String(), nil
}

// fishDecrypt decrypts "+OK …" or "mcps …" text (ok false when it isn't
// that, or doesn't decode). The text ends at the first zero byte.
func fishDecrypt(key, text string) (plain string, cbc, ok bool) {
	rest, found := strings.CutPrefix(text, "+OK ")
	if !found {
		if rest, found = strings.CutPrefix(text, "mcps "); !found {
			return "", false, false
		}
	}
	c, err := fishCipher(key)
	if err != nil {
		return "", false, false
	}
	var out []byte
	if rest, cbc = strings.CutPrefix(rest, "*"); cbc {
		// some clients leave the padding off (or a character over)
		rest = strings.TrimRight(rest, "=")
		if len(rest)%4 == 1 {
			rest = rest[:len(rest)-1]
		}
		raw, err := base64.RawStdEncoding.DecodeString(rest)
		if err != nil || len(raw) < 16 {
			return "", false, false
		}
		raw = raw[:len(raw)-len(raw)%8]
		out = make([]byte, len(raw)-8)
		cipher.NewCBCDecrypter(c, raw[:8]).CryptBlocks(out, raw[8:])
	} else {
		if out, err = fishECBDecode(c, rest); err != nil {
			return "", false, false
		}
	}
	if i := strings.IndexByte(string(out), 0); i >= 0 {
		out = out[:i]
	}
	return string(out), cbc, true
}

// fishECBDecode decodes and decrypts ECB text, 12 characters a block (a
// part block at the end is dropped, as FiSH does).
func fishECBDecode(c *blowfish.Cipher, s string) ([]byte, error) {
	s = s[:len(s)-len(s)%12]
	if s == "" {
		return nil, errFishText
	}
	out := make([]byte, 0, len(s)/12*8)
	block := make([]byte, 8)
	for ; s != ""; s = s[12:] {
		var halves [2]uint32 // right, then left
		for i := range 12 {
			v := strings.IndexByte(fishB64, s[i])
			if v < 0 {
				return nil, errFishText
			}
			halves[i/6] |= uint32(v) << (6 * (i % 6))
		}
		binary.BigEndian.PutUint32(block[:4], halves[1])
		binary.BigEndian.PutUint32(block[4:], halves[0])
		c.Decrypt(block, block)
		out = append(out, block...)
	}
	return out, nil
}

// fishMaxPlain is how many bytes of text fit in an encrypted payload of at
// most room bytes.
func fishMaxPlain(room int, cbc bool) int {
	if !cbc {
		return (room - len("+OK ")) / 12 * 8
	}
	n := 0
	for base64.StdEncoding.EncodedLen(8+n+8)+len("+OK *") <= room {
		n += 8
	}
	return n
}
