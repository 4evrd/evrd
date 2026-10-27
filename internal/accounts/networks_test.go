package accounts

import (
	"errors"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/store"
)

func TestNormalizeNetworkServersAndOptions(t *testing.T) {
	n := store.Network{
		Name: "libera", Nick: "bob",
		Servers:         []store.Server{{Host: " irc.libera.chat ", TLS: true}, {Host: "irc2.libera.chat", Port: 6667}, {Host: ""}},
		Fingerprints:    []string{"AB:" + strings.Repeat("cd", 31), "ab" + strings.Repeat("CD", 31)},
		Channels:        []string{"#a", "#A", "#b"},
		ChannelSettings: map[string]store.ChannelSettings{"#a": {Key: " k "}, "#zzz": {Key: "gone"}},
		Encoding:        "^iso-8859-1",
		FloodRate:       2, FloodBurst: 9,
	}
	if err := NormalizeNetwork(&n); err != nil {
		t.Fatal(err)
	}
	if len(n.Servers) != 2 || n.Servers[0].Host != "irc.libera.chat" || n.Servers[0].Port != 6697 || n.Servers[1].Port != 6667 {
		t.Fatalf("servers: %+v", n.Servers)
	}
	if n.Host != "irc.libera.chat" || n.Port != 6697 || !n.TLS {
		t.Fatalf("Host/Port/TLS mirror the first server: %s %d %v", n.Host, n.Port, n.TLS)
	}
	if len(n.Fingerprints) != 1 || n.Fingerprints[0] != "ab"+strings.Repeat("cd", 31) {
		t.Fatalf("fingerprints are normalized and deduplicated: %q", n.Fingerprints)
	}
	if strings.Join(n.Channels, " ") != "#a #b" {
		t.Fatalf("channels: %q", n.Channels)
	}
	if n.ChannelSettings["#a"].Key != "k" || len(n.ChannelSettings) != 1 {
		t.Fatalf("channel settings follow the channel list: %+v", n.ChannelSettings)
	}

	// The single-server shorthand still works (older callers, *status).
	m := store.Network{Name: "x", Nick: "y", Host: "irc.example.org", Port: 6667}
	if err := NormalizeNetwork(&m); err != nil || len(m.Servers) != 1 || m.Servers[0].Port != 6667 {
		t.Fatalf("shorthand: %v %+v", err, m.Servers)
	}

	for _, bad := range []store.Network{
		{Name: "x", Nick: "y"}, // no server
		{Name: "x", Nick: "y", Host: "h", Fingerprints: []string{"nothex"}},
		{Name: "x", Nick: "y", Host: "h", Encoding: "klingon"},
		{Name: "x", Nick: "y", Host: "h", FloodRate: -1},
		{Name: "x", Nick: "y", Host: "h", FloodRate: 1, FloodBurst: 0},
		{Name: "x", Nick: "y", Host: "h", JoinDelay: 9999},
		{Name: "x", Nick: "y", Host: "h", AltNick: "bad nick"},
		{Name: "x", Nick: "y", Host: "h", Channels: []string{"#a"}, ChannelSettings: map[string]store.ChannelSettings{"#a": {Key: "a b"}}},
	} {
		if err := NormalizeNetwork(&bad); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestNeedsReconnect(t *testing.T) {
	a := store.Network{Nick: "a", Servers: []store.Server{{Host: "h", Port: 1}}, FloodRate: 2}
	b := a
	b.FloodRate, b.QuitMsg, b.JoinDelay = 5, "bye", 3
	if NeedsReconnect(a, b) {
		t.Fatal("flood, quit message and join delay apply live")
	}
	b.Servers = []store.Server{{Host: "h", Port: 1}, {Host: "h2", Port: 1}}
	if NeedsReconnect(a, b) {
		t.Fatal("a server to fall back to applies when the link drops")
	}
	if NeedsReconnect(b, a) {
		t.Fatal("removing a server it doesn't connect to first applies live")
	}
	b.Servers = []store.Server{{Host: "h", Port: 2}}
	if !NeedsReconnect(a, b) {
		t.Fatal("a change to the first server reconnects")
	}
	b = a
	b.Fingerprints = []string{"x"}
	if NeedsReconnect(a, b) {
		t.Fatal("a newly trusted certificate applies live")
	}
	if !NeedsReconnect(b, a) {
		t.Fatal("a certificate no longer trusted reconnects")
	}
	// a link that's down tries again at once with what might bring it up
	if ReconnectFor(a, b, "connected") || !ReconnectFor(a, b, "error") || !ReconnectFor(a, b, "reconnecting") || ReconnectFor(a, b, "disconnected") {
		t.Fatal("a newly trusted certificate: reconnect a link that's down, not a live or switched-off one")
	}
}

func TestHostAllowed(t *testing.T) {
	u := store.User{}
	if !HostAllowed(u, "203.0.113.7") {
		t.Fatal("an empty list allows everyone")
	}
	u.AllowedHosts = []string{"10.0.0.0/8", "192.168.*", "2001:db8::1"}
	for ip, want := range map[string]bool{
		"10.1.2.3": true, "192.168.1.9": true, "2001:db8::1": true, "::ffff:10.0.0.1": true,
		"203.0.113.7": false, "192.169.1.1": false, "2001:db8::2": false,
	} {
		if got := HostAllowed(u, ip); got != want {
			t.Errorf("%s: got %v, want %v", ip, got, want)
		}
	}
	if _, err := NormalizeAllowedHosts([]string{"evil.example.com"}); err == nil {
		t.Error("host names aren't allowed entries")
	}
}

func TestCheckBindHost(t *testing.T) {
	s := &Service{}
	user := store.User{Role: store.RoleUser}
	if err := s.CheckBindHost(user, "192.0.2.1"); err == nil {
		t.Fatal("without a list, users can't pick a bind host")
	}
	s.SetBindHosts([]string{"192.0.2.1", "vhost.example.org"})
	if err := s.CheckBindHost(user, "VHOST.example.org"); err != nil {
		t.Fatalf("a listed bind host: %v", err)
	}
	if err := s.CheckBindHost(user, "192.0.2.9"); err == nil {
		t.Fatal("an unlisted bind host is refused")
	}
	user.DenySetBindHost = true
	if err := s.CheckBindHost(user, "192.0.2.1"); err == nil {
		t.Fatal("a locked user can't change it")
	}
	if err := s.CheckBindHost(store.User{Role: store.RoleAdmin}, "198.51.100.1"); err != nil {
		t.Fatal("admins may use any address")
	}
}

func TestNormalizeNetworkRejects(t *testing.T) {
	ok := func() store.Network { return store.Network{Name: "x", Nick: "y", Host: "irc.example.org"} }
	for _, tc := range []struct {
		name string
		edit func(*store.Network)
		want string // substring of the message
	}{
		{"no name", func(n *store.Network) { n.Name = " " }, "needs a name"},
		{"name with a colon", func(n *store.Network) { n.Name = "a:b" }, "needs a name"},
		{"long name", func(n *store.Network) { n.Name = strings.Repeat("n", 33) }, "32 characters"},
		{"no nick", func(n *store.Network) { n.Nick = "" }, "needs a nick"},
		{"bad nick", func(n *store.Network) { n.Nick = "a@b" }, "nicks can't"},
		{"bad ident", func(n *store.Network) { n.Username = "me@host" }, "ident"},
		{"flood rate too high", func(n *store.Network) { n.FloodRate, n.FloodBurst = 101, 1 }, "flood rate"},
		{"flood burst too high", func(n *store.Network) { n.FloodRate, n.FloodBurst = 1, 1001 }, "flood burst"},
		{"negative join delay", func(n *store.Network) { n.JoinDelay = -1 }, "join delay"},
		{"multi-line quit", func(n *store.Network) { n.QuitMsg = "bye\r\nPRIVMSG x :hi" }, "one line"},
		{"bad hostname", func(n *store.Network) { n.Servers = []store.Server{{Host: "irc.example.org/path"}} }, "hostname"},
		{"bad port", func(n *store.Network) { n.Servers = []store.Server{{Host: "h", Port: 70000}} }, "ports"},
		{"multi-line server password", func(n *store.Network) { n.Servers = []store.Server{{Host: "h", Password: "a\nb"}} }, "one line"},
		{"too many servers", func(n *store.Network) {
			for range 21 {
				n.Servers = append(n.Servers, store.Server{Host: "h"})
			}
		}, "20 servers"},
		{"short fingerprint", func(n *store.Network) { n.Fingerprints = []string{"abcd"} }, "64 hex"},
		{"channel without a prefix", func(n *store.Network) { n.Channels = []string{"general"} }, "channel name"},
		{"channel with a comma", func(n *store.Network) { n.Channels = []string{"#a,#b"} }, "channel name"},
		{"channel key with a comma", func(n *store.Network) {
			n.Channels = []string{"#a"}
			n.ChannelSettings = map[string]store.ChannelSettings{"#a": {Key: "a,b"}}
		}, "channel keys"},
	} {
		n := ok()
		tc.edit(&n)
		err := NormalizeNetwork(&n)
		var v *ValidationError
		if !errors.As(err, &v) || !strings.Contains(v.Message, tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}

	// Empty lists come back non-nil (they're JSON arrays), blank entries go.
	n := ok()
	n.Channels, n.Fingerprints = []string{" ", ""}, []string{" "}
	if err := NormalizeNetwork(&n); err != nil || n.Channels == nil || len(n.Channels) != 0 || n.Fingerprints == nil || len(n.Fingerprints) != 0 {
		t.Fatalf("blank entries: %v %#v %#v", err, n.Channels, n.Fingerprints)
	}
}

func TestCharset(t *testing.T) {
	for _, enc := range []string{"ISO-8859-1", "^windows-1252", "koi8-r", "utf-8"} {
		if e, err := Charset(enc); err != nil || e == nil {
			t.Errorf("%s: %v", enc, err)
		}
	}
	if _, err := Charset("^klingon"); err == nil || !strings.Contains(err.Error(), `"klingon"`) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestCheckNetworkName(t *testing.T) {
	s, st := newService(t, config.Limits{})
	alice, _ := s.Create(CreateInput{Username: "alice"})
	bob, _ := s.Create(CreateInput{Username: "bob"})
	libera, err := st.CreateNetwork(t.Context(), store.Network{UserID: alice.ID, Name: "Libera", Nick: "alice", Host: "irc.libera.chat"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CheckNetworkName(alice.ID, "libera", 0); err == nil || !strings.Contains(err.Error(), "Libera") {
		t.Fatalf("a duplicate name, any case: %v", err)
	}
	if err := s.CheckNetworkName(alice.ID, "LIBERA", libera.ID); err != nil {
		t.Fatalf("renaming the network itself: %v", err)
	}
	if err := s.CheckNetworkName(alice.ID, "oftc", 0); err != nil {
		t.Fatalf("a new name: %v", err)
	}
	if err := s.CheckNetworkName(bob.ID, "libera", 0); err != nil {
		t.Fatalf("names are per user: %v", err)
	}
}
