package modules

import (
	"encoding/hex"
	"math/big"
	"strings"
	"testing"
)

// The known answers here come from weechat's fish.py
// (github.com/weechat/scripts, python/fish.py: blowcrypt_pack,
// blowcrypt_unpack, dh1080_pack, dh1080_secret, dh1080_b64encode), run
// with pycryptodome 4.6 on the inputs below; fish.py interoperates with
// FiSH, mircryption, ZNC's crypt and HexChat's fishlim.

func TestFishECBKnownAnswers(t *testing.T) {
	for _, v := range []struct{ key, text, want string }{
		{"testkey", "Hello, world!", "+OK 9vjCO08wIY41GHMAo.pCIjl/"},
		{"s3cr3t-k3y", "The quick brown fox jumps over the lazy dog", "+OK ewCe/.LKmoE1GGpgf1z8KmB0/4Ybl02aLQP0PLdOJ.TxBnx/jj07H1XPbil1Xm8HZ0Z0hqI."},
	} {
		got, err := fishEncrypt(v.key, false, v.text)
		if err != nil || got != v.want {
			t.Errorf("encrypt %q: %q, %v; want %q", v.text, got, err, v.want)
		}
		plain, cbc, ok := fishDecrypt(v.key, v.want)
		if !ok || cbc || plain != v.text {
			t.Errorf("decrypt %q: %q %v %v", v.want, plain, cbc, ok)
		}
		// mircryption's prefix, and a stray part block at the end
		if plain, _, ok := fishDecrypt(v.key, "mcps "+strings.TrimPrefix(v.want, "+OK ")+"xyz"); !ok || plain != v.text {
			t.Errorf("mcps: %q %v", plain, ok)
		}
	}
}

func TestFishCBCKnownAnswers(t *testing.T) {
	defer func(f func([]byte) error) { fishIV = f }(fishIV)
	for _, v := range []struct{ key, text, iv, want string }{
		{"testkey", "Hello, world!", "0001020304050607", "+OK *AAECAwQFBgcMwrYaMR5JLUL1S0XmY6y+"},
		{"s3cr3t-k3y", "The quick brown fox jumps over the lazy dog", "ffeeddccbbaa9988", "+OK */+7dzLuqmYgYN5G8utrqXxvzx3WBy9rsVvm/tOr+llwAYcSZ94tTmZpx1+R0DCfkRuAga8K43+Y="},
	} {
		iv, _ := hex.DecodeString(v.iv)
		fishIV = func(b []byte) error { copy(b, iv); return nil }
		got, err := fishEncrypt(v.key, true, v.text)
		if err != nil || got != v.want {
			t.Errorf("encrypt %q: %q, %v; want %q", v.text, got, err, v.want)
		}
		for _, in := range []string{v.want, strings.TrimRight(v.want, "=")} { // padding or not
			plain, cbc, ok := fishDecrypt(v.key, in)
			if !ok || !cbc || plain != v.text {
				t.Errorf("decrypt %q: %q %v %v", in, plain, cbc, ok)
			}
		}
	}
}

func TestFishBadInput(t *testing.T) {
	for _, in := range []string{"hello", "+OK short", "+OK !!!!!!!!!!!!", "+OK *", "+OK *QUJD", "+OK *not base64!"} {
		if _, _, ok := fishDecrypt("testkey", in); ok {
			t.Errorf("%q decrypted", in)
		}
	}
	if _, err := fishEncrypt(strings.Repeat("k", maxKeyLen+1), false, "x"); err == nil {
		t.Error("a 57-byte key was taken")
	}
	// round trips, CBC with a random IV, and UTF-8
	for _, cbc := range []bool{false, true} {
		enc, _ := fishEncrypt("k", cbc, "héllo wörld ✓")
		if plain, _, ok := fishDecrypt("k", enc); !ok || plain != "héllo wörld ✓" {
			t.Errorf("cbc %v: %q", cbc, plain)
		}
	}
}

func TestFishMaxPlainFits(t *testing.T) {
	for _, cbc := range []bool{false, true} {
		for room := 20; room < 450; room += 7 {
			n := fishMaxPlain(room, cbc)
			enc, _ := fishEncrypt("k", cbc, strings.Repeat("x", n))
			if len(enc) > room || n%8 != 0 {
				t.Fatalf("cbc %v room %d: %d bytes of text make %d", cbc, room, n, len(enc))
			}
			if more, _ := fishEncrypt("k", cbc, strings.Repeat("x", n+8)); len(more) <= room {
				t.Fatalf("cbc %v room %d: %d would fit too", cbc, room, n+8)
			}
		}
	}
}

func TestDH1080Base64(t *testing.T) {
	for _, v := range []struct{ hex, enc string }{
		{"4420", "RCA"},
		{"823cfd", "gjz9A"},
		{"e6f1c26b", "5vHCaw"},
		{"30f90ec7dd", "MPkOx90"},
		{"01e4887534a20f0b0d04c36ed80e71e0fd77b07670eb940bd5335f973daad8", "AeSIdTSiDwsNBMNu2A5x4P13sHZw65QL1TNflz2q2A"},
		{"619b91ffc911f57cced458bbbf2ce03753c9bdfa0ff0169dc9575674066676cf", "YZuR/8kR9XzO1Fi7vyzgN1PJvfoP8BadyVdWdAZmds8"},
		{"b0b4eb8902c44269da1cf6ba66d3f8b6d4b100a9ea0e755a5c2e8210242a08e707", "sLTriQLEQmnaHPa6ZtP4ttSxAKnqDnVaXC6CECQqCOcHA"},
	} {
		b, _ := hex.DecodeString(v.hex)
		if got := dhEncode(b); got != v.enc {
			t.Errorf("encode %s: %s, want %s", v.hex, got, v.enc)
		}
		if got, err := dhDecode(v.enc); err != nil || hex.EncodeToString(got) != v.hex {
			t.Errorf("decode %s: %x %v", v.enc, got, err)
		}
	}
	if _, err := dhDecode("A"); err == nil {
		t.Error("decoded one character")
	}
}

func TestDH1080KnownExchange(t *testing.T) {
	pair := func(h string) dhPair {
		priv, _ := new(big.Int).SetString(h, 16)
		return dhPair{private: priv, public: new(big.Int).Exp(big.NewInt(2), priv, dhPrime)}
	}
	alice := pair("8934c0843955653d24249a4df2a1b6ff390636f0b03a4fa934f4b3f78c6f7ebc9da256cbc882f725b8c2c39736870b0c6ea0d9975c6435caa4d1fe435d17a74632516e7ecbf6419bb4777e7f5e108fbb02a2d8d43964953c19b616d44d698f33fecc939575369e1b6a5c9acc9e17ff8b0394083b6dd38b54da2ae85a5d16f5af6896693b7fd2ff")
	bob := pair("3c63ef6ad210f1441bc06a64de3be9af3add4ec9fa3ae7f1f61bbfb1e63fc784bc88693f10e0f2e85c29423cd0f589fcadfc4c20952167da16a4703ef1d009e15c50207e45687f72c59c1051f4880123fc93bd12a647ecec7d613e4a4a8b1d7ff522d9978d0a77df4c77a2acc6900ddcd4d452941c50a3de04b715cc6a50723348ec51d7cd2f2a")
	const (
		alicePub = "XQFtlfE/DxTcRlgblm6YLX6yos047BodXJtt9llKZaGk4xHky7VyHyT6lS5UKkz2CPVj1ufZDXCEVi+99aTqkJMzTx4XuRbMjFhlQhq4z4FQ5GoO34ukxMb21vOvATqSa6cqK9XrHuvHL9GUZqvWuFNoHzT9co9LjgCK5ALPsG79cgTCQ9miA"
		bobPub   = "6x/bfGUizJTlvJRF7kyx+UsF9xvoy1FhyK49YXEw9/LAz98hmgYy0iczuCYtLwvYPbNTCuFR1SO+LIOZvhnRRhDAq2iIQOGO6sfyrvxUy7i4i5EgtSyd3RrknOmxT08ClMNDbMfmD6Zl9kVxM9SDTLsBQiFW70zGgWvX36tj7eWtB04d10xaA"
		key      = "sDCavY+Drk8hihfnwr7TTZR4ZBR4D2JUHs5+TnI9k1k"
	)
	if alice.Public() != alicePub || bob.Public() != bobPub {
		t.Fatalf("public keys:\n%s\n%s", alice.Public(), bob.Public())
	}
	if k, err := alice.Key(bobPub); err != nil || k != key {
		t.Errorf("alice's key %q %v", k, err)
	}
	if k, err := bob.Key(alicePub); err != nil || k != key {
		t.Errorf("bob's key %q %v", k, err)
	}
	for _, bad := range []string{"AA", dhEncode([]byte{1}), dhEncode(dhPrime.Bytes())} {
		if _, err := alice.Key(bad); err == nil {
			t.Errorf("took %q", bad)
		}
	}
}

func TestDH1080NewPairs(t *testing.T) {
	a, err := newDH()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newDH()
	ka, _ := a.Key(b.Public())
	kb, _ := b.Key(a.Public())
	if ka == "" || ka != kb || len(ka) != 43 {
		t.Fatalf("keys %q %q", ka, kb)
	}
}
