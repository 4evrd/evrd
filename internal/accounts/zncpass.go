package accounts

// Passwords brought over from ZNC: an account made from a ZNC config keeps
// its owner's ZNC password. ZNC's hash is stored as it was, tagged (zncHash),
// checked at the first sign-in (web or IRC client) and then replaced with
// evrd's own bcrypt hash, so it's only ever checked once.

import (
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// zncPrefix marks a PasswordHash that's ZNC's: "znc$md5$salt$hex",
// "znc$sha256$salt$hex" or "znc$argon2id$<the PHC string>".
const zncPrefix = "znc$"

// ZNCPasswordHash is what to store for a ZNC user's password, from its
// config: the hash method (md5, sha256, argon2id, or plain for a password
// ZNC kept as it is), the hash, and the salt. A plain one is hashed here.
func ZNCPasswordHash(method, hash, salt string) (string, error) {
	switch m := strings.ToLower(method); m {
	case "plain":
		if hash == "" {
			return "", fmt.Errorf("accounts: an empty ZNC password")
		}
		h, err := bcrypt.GenerateFromPassword([]byte(hash), bcrypt.DefaultCost)
		return string(h), err
	case "md5", "sha256":
		hash = strings.ToLower(hash)
		if b, err := hex.DecodeString(hash); err != nil || (m == "md5" && len(b) != md5.Size) || (m == "sha256" && len(b) != sha256.Size) {
			return "", fmt.Errorf("accounts: not a ZNC %s hash", m)
		}
		if strings.Contains(salt, "$") {
			return "", fmt.Errorf("accounts: a ZNC salt with $ in it")
		}
		return zncPrefix + m + "$" + salt + "$" + hash, nil
	case "argon2id":
		if _, _, _, err := parseArgon2(hash); err != nil {
			return "", err
		}
		return zncPrefix + m + "$" + hash, nil
	}
	return "", fmt.Errorf("accounts: ZNC hash method %q isn't one evrd knows", method)
}

// IsZNCHash reports whether a stored hash is one brought over from ZNC.
func IsZNCHash(stored string) bool { return strings.HasPrefix(stored, zncPrefix) }

// matchHash checks pw against a stored hash: bcrypt, or ZNC's. upgrade is
// true when it matched a ZNC hash, which should be replaced.
func matchHash(stored, pw string) (ok, upgrade bool) {
	if stored == "" {
		return false, false
	}
	rest, isZNC := strings.CutPrefix(stored, zncPrefix)
	if !isZNC {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(pw)) == nil, false
	}
	method, rest, _ := strings.Cut(rest, "$")
	var got []byte
	var want []byte
	switch method {
	case "md5", "sha256":
		salt, hx, _ := strings.Cut(rest, "$")
		var err error
		if want, err = hex.DecodeString(hx); err != nil {
			return false, false
		}
		if method == "md5" {
			s := md5.Sum([]byte(pw + salt))
			got = s[:]
		} else {
			s := sha256.Sum256([]byte(pw + salt))
			got = s[:]
		}
	case "argon2id":
		p, salt, key, err := parseArgon2(rest)
		if err != nil {
			return false, false
		}
		got, want = argon2.IDKey([]byte(pw), salt, p.t, p.m, p.p, uint32(len(key))), key
	default:
		return false, false
	}
	ok = len(got) == len(want) && subtle.ConstantTimeCompare(got, want) == 1
	return ok, ok
}

type argonParams struct {
	m, t uint32
	p    uint8
}

// parseArgon2 reads "$argon2id$v=19$m=65536,t=3,p=4$salt$hash".
func parseArgon2(s string) (p argonParams, salt, key []byte, err error) {
	f := strings.Split(s, "$")
	if len(f) != 6 || f[1] != "argon2id" || f[2] != "v=19" {
		return p, nil, nil, fmt.Errorf("accounts: not an argon2id hash")
	}
	var par uint32
	if _, err = fmt.Sscanf(f[3], "m=%d,t=%d,p=%d", &p.m, &p.t, &par); err != nil || par == 0 || par > 255 || p.t == 0 || p.m == 0 {
		return p, nil, nil, fmt.Errorf("accounts: argon2id parameters %q", f[3])
	}
	p.p = uint8(par)
	if salt, err = base64.RawStdEncoding.DecodeString(f[4]); err != nil {
		return p, nil, nil, err
	}
	if key, err = base64.RawStdEncoding.DecodeString(f[5]); err != nil || len(key) == 0 {
		return p, nil, nil, fmt.Errorf("accounts: argon2id hash")
	}
	return p, salt, key, nil
}
