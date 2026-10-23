package links

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileHash(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.WriteFile(a, []byte("one build"), 0o644)
	os.WriteFile(b, []byte("another build"), 0o644)
	if h := FileHash(a); len(h) != 12 || h != FileHash(a) || h == FileHash(b) {
		t.Errorf("hashes: %q %q", h, FileHash(b))
	}
	if h := FileHash(filepath.Join(dir, "none")); h != "" {
		t.Errorf("no file: %q", h)
	}
	// there's no evrd-links beside the test binary
	if h := ShippedHash(); h != "" {
		t.Errorf("shipped: %q", h)
	}
	if exe, _ := os.Executable(); SelfHash() != FileHash(exe) || SelfHash() == "" {
		t.Errorf("self: %q", SelfHash())
	}
}
