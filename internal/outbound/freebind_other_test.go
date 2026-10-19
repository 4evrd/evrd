//go:build !linux

package outbound

import (
	"context"
	"net/netip"
	"strings"
	"testing"
)

func TestPrefixNeedsLinux(t *testing.T) {
	_, err := dialFrom(context.Background(), netip.MustParseAddrPort("[2001:db8:42::1]:0"), netip.MustParseAddrPort("[::1]:6667"), true)
	if err == nil || !strings.Contains(err.Error(), "needs Linux") {
		t.Fatalf("got %v", err)
	}
}
