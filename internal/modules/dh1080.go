package modules

// DH1080, FiSH's key exchange: Diffie-Hellman over a fixed 1080-bit prime
// with generator 2. One side sends "DH1080_INIT <public key>", the other
// answers "DH1080_FINISH <its public key>" (both as NOTICEs), and the key
// both then use is the SHA-256 of the shared secret. Public keys and the
// key are in FiSH's base64. Newer clients add " CBC" to say they want CBC
// mode (or send "DH1080_INIT_CBC").

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"math/big"
)

// dhPrime is FiSH's prime (as in FiSH, ZNC's crypt and weechat's fish.py).
var dhPrime, _ = new(big.Int).SetString(
	"FBE1022E23D213E8ACFA9AE8B9DFADA3EA6B7AC7A7B7E95AB5EB2DF858921FEADE95E6AC7BE7DE6ADBAB8A783E"+
		"7AF7A7FA6A2B7BEB1E72EAE2B72F9FA2BFB2A2EFBEFAC868BADB3E828FA8BADFADA3E4CC1BE7E8AFE85E9698A7"+
		"83EB68FA07A77AB6AD7BEB618ACF9CA2897EB28A6189EFA07AB99A8A7FA9AE299EFA7BA66DEAFEFBEFBF0B7D8B", 16)

// dhQ is (p-1)/2: a public key of ours is in the subgroup it orders.
var dhQ = new(big.Int).Rsh(new(big.Int).Sub(dhPrime, big.NewInt(1)), 1)

var errDHKey = errors.New("not a DH1080 public key")

// dhPair is one side's keys for one exchange.
type dhPair struct{ private, public *big.Int }

// newDH makes a key pair, as fish.py does: a random 1080-bit private key
// whose public key is in the prime's large subgroup.
func newDH() (dhPair, error) {
	one, two := big.NewInt(1), big.NewInt(2)
	max := new(big.Int).Sub(dhPrime, one)
	for {
		b := make([]byte, 1080/8)
		if _, err := rand.Read(b); err != nil {
			return dhPair{}, err
		}
		priv := new(big.Int).SetBytes(b)
		pub := new(big.Int).Exp(two, priv, dhPrime)
		if pub.Cmp(two) >= 0 && pub.Cmp(max) <= 0 && new(big.Int).Exp(pub, dhQ, dhPrime).Cmp(one) == 0 {
			return dhPair{private: priv, public: pub}, nil
		}
	}
}

// Public is our public key as sent.
func (p dhPair) Public() string { return dhEncode(p.public.Bytes()) }

// Key is the key both sides end up with, given the other's public key.
func (p dhPair) Key(theirs string) (string, error) {
	b, err := dhDecode(theirs)
	if err != nil {
		return "", err
	}
	pub := new(big.Int).SetBytes(b)
	// (only the range is checked: FiSH's clients don't keep to the subgroup)
	if pub.Cmp(big.NewInt(1)) <= 0 || pub.Cmp(dhPrime) >= 0 {
		return "", errDHKey
	}
	secret := new(big.Int).Exp(pub, p.private, dhPrime)
	sum := sha256.Sum256(secret.Bytes())
	return dhEncode(sum[:]), nil
}

// dhEncode is FiSH's base64 for DH1080: standard base64 without padding,
// and an extra "A" when the bytes come out even (a length of 3n), as
// FiSH's C code has it.
func dhEncode(b []byte) string {
	s := base64.RawStdEncoding.EncodeToString(b)
	if len(b)%3 == 0 {
		s += "A"
	}
	return s
}

// dhDecode is FiSH's base64 decoding, ported as it is (fish.py's
// dh1080_b64decode): characters that are "A" (or not base64) at the end are
// dropped, then it's decoded 4 characters to 3 bytes.
func dhDecode(s string) ([]byte, error) {
	const b64 = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var val [256]byte
	for i := range 64 {
		val[b64[i]] = byte(i)
	}
	n := len(s)
	if n < 2 {
		return nil, errDHKey
	}
	for i := n - 2; i >= 0 && val[s[i]] == 0; i-- {
		n--
	}
	if n < 2 {
		return nil, errDHKey
	}
	d := make([]byte, n)
	i, k := 0, 0
	for {
		i++
		if k+1 >= n {
			break
		}
		d[i-1] = val[s[k]] << 2
		k++
		d[i-1] |= val[s[k]] >> 4 // (k < n here)
		i++
		if k+1 >= n {
			break
		}
		d[i-1] = val[s[k]] << 4
		k++
		d[i-1] |= val[s[k]] >> 2
		i++
		if k+1 >= n {
			break
		}
		d[i-1] = val[s[k]] << 6
		k++
		d[i-1] |= val[s[k]]
		k++
	}
	return d[:i-1], nil
}
