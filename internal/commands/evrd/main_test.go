package evrd

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain keeps the VAPID key the daemons under test make (webpush_vapid_key,
// relative to the working directory by default) out of the source tree.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "evrd-vapid-")
	if err != nil {
		panic(err)
	}
	os.Setenv("EVRD_WEBPUSH_VAPID_KEY", filepath.Join(dir, "webpush-vapid.pem"))
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
