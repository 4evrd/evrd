package upstream

import (
	"net"
	"net/netip"
	"testing"
)

func TestAddrPort(t *testing.T) {
	for _, c := range []struct {
		in   net.Addr
		want string
	}{
		{&net.TCPAddr{IP: net.ParseIP("192.0.2.1"), Port: 6667}, "192.0.2.1:6667"}, // 16-byte IPv4, unmapped
		{&net.TCPAddr{IP: net.ParseIP("2001:db8::1"), Port: 6697}, "[2001:db8::1]:6697"},
		{&net.UDPAddr{IP: net.ParseIP("192.0.2.2"), Port: 113}, "192.0.2.2:113"},
		{&net.UnixAddr{Name: "/run/links.sock", Net: "unix"}, "invalid AddrPort"},
		{nil, "invalid AddrPort"},
	} {
		if got := addrPort(c.in).String(); got != c.want {
			t.Errorf("addrPort(%v) = %s, want %s", c.in, got, c.want)
		}
	}
	var none netip.AddrPort
	if addrPort(nil) != none {
		t.Error("addrPort(nil) isn't the zero AddrPort")
	}
}
