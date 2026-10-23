package links

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
)

// evrd-links is built without a version or commit stamped in, so it stays
// byte-identical between releases unless its own code changes (and upgrades
// leave it running). It names itself by a hash of its binary instead: the
// same hash as the evrd-links shipped with evrd means it's that release's.

// BinaryName is the holder's executable, installed beside evrd.
const BinaryName = "evrd-links"

// FileHash is the short hash a holder built from path reports, "" if path
// can't be read.
func FileHash(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// ShippedHash is the hash of the evrd-links beside the running executable
// (the one shipped with this evrd), "" if there's none. Call it before
// confining the process.
func ShippedHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return FileHash(filepath.Join(filepath.Dir(exe), BinaryName))
}

// SelfHash is the running executable's hash, "" if it can't be read. A
// holder reads it as it starts (Holder.Hash): an upgrade can replace the
// file while it keeps running, and once confined it can't read files.
func SelfHash() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return FileHash(exe)
}
