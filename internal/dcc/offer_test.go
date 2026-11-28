package dcc

import (
	"net/netip"
	"testing"
)

func TestParse(t *testing.T) {
	ip := netip.MustParseAddr
	for _, c := range []struct {
		in   string
		want Offer
	}{
		{"\x01DCC CHAT chat 3232235777 5000\x01", Offer{Type: "CHAT", Name: "chat", Addr: ip("192.168.1.1"), Port: 5000, Size: -1}},
		{"\x01DCC SEND file.txt 2130706433 6000 1234\x01", Offer{Type: "SEND", Name: "file.txt", Addr: ip("127.0.0.1"), Port: 6000, Size: 1234}},
		{"\x01dcc send \"my file.txt\" 2130706433 6000 1234\x01", Offer{Type: "SEND", Name: "my file.txt", Addr: ip("127.0.0.1"), Port: 6000, Size: 1234}},
		{"\x01DCC SEND my holiday 2024.jpg 2130706433 6000 99\x01", Offer{Type: "SEND", Name: "my holiday 2024.jpg", Addr: ip("127.0.0.1"), Port: 6000, Size: 99}},
		{"\x01DCC SEND f 2001:db8::1 6000 10\x01", Offer{Type: "SEND", Name: "f", Addr: ip("2001:db8::1"), Port: 6000, Size: 10}},
		{"\x01DCC SEND f 10.0.0.1 6000\x01", Offer{Type: "SEND", Name: "f", Addr: ip("10.0.0.1"), Port: 6000, Size: -1}},
		{"\x01DCC SEND f 3232235777 0 10 77\x01", Offer{Type: "SEND", Name: "f", Addr: ip("192.168.1.1"), Port: 0, Size: 10, Token: "77"}},
		{"\x01DCC CHAT chat 3232235777 0 12\x01", Offer{Type: "CHAT", Name: "chat", Addr: ip("192.168.1.1"), Port: 0, Size: -1, Token: "12"}},
		{"\x01DCC RESUME f 6000 512\x01", Offer{Type: "RESUME", Name: "f", Port: 6000, Size: 512}},
		{"\x01DCC ACCEPT \"a b\" 0 512 77\x01", Offer{Type: "ACCEPT", Name: "a b", Port: 0, Size: 512, Token: "77"}},
	} {
		got, ok := Parse(c.in)
		if !ok || got != c.want {
			t.Errorf("%q: %+v %v, want %+v", c.in, got, ok, c.want)
			continue
		}
		// and back: what it writes reads the same
		if again, ok := Parse(got.String()); !ok || again != got {
			t.Errorf("%q: written %q reads as %+v", c.in, got.String(), again)
		}
	}
	for _, bad := range []string{
		"\x01VERSION\x01", "\x01DCC\x01", "\x01DCC SEND f\x01", "\x01DCC SEND f 1.2.3.4 99999\x01",
		"\x01DCC SEND f nowhere 5000\x01", "\x01DCC SEND f 1.2.3.4 5000 -1\x01", "\x01DCC XMIT f 1 2\x01",
		"\x01DCC SEND \"unclosed 1 2\x01", "\x01DCC CHAT chat 1.2.3.4 5000 tok extra\x01", "\x01DCC SEND f fe80::1%eth0 5000\x01",
	} {
		if o, ok := Parse(bad); ok {
			t.Errorf("%q parsed: %+v", bad, o)
		}
	}
}

func TestString(t *testing.T) {
	o := Offer{Type: "SEND", Name: "a b.txt", Addr: netip.MustParseAddr("1.2.3.4"), Port: 5000, Size: 7}
	if got := o.String(); got != "\x01DCC SEND \"a b.txt\" 16909060 5000 7\x01" {
		t.Errorf("%q", got)
	}
	o = Offer{Type: "CHAT", Name: "chat", Addr: netip.MustParseAddr("2001:db8::2"), Port: 5001, Size: -1}
	if got := o.String(); got != "\x01DCC CHAT chat 2001:db8::2 5001\x01" {
		t.Errorf("%q", got)
	}
	o = Offer{Type: "SEND", Name: "f", Addr: netip.MustParseAddr("1.2.3.4"), Port: 0, Size: -1, Token: "9"}
	if got := o.String(); got != "\x01DCC SEND f 16909060 0 0 9\x01" {
		t.Errorf("a token needs a size before it: %q", got)
	}
	if got := FormatAddr(netip.MustParseAddr("::ffff:127.0.0.1")); got != "2130706433" {
		t.Errorf("a mapped IPv4: %q", got)
	}
}
