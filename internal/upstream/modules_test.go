package upstream

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func hasLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

func TestModulesRunOnTheConnection(t *testing.T) {
	srv, host, port := startAuthServer(t, false)
	n := store.Network{ID: 41, Nick: "dora", Host: host, Port: port, Password: "pw", SASLUser: "dora",
		Modules: map[string]store.ModuleConfig{
			"perform": {Enabled: true, Settings: map[string]string{"commands": "/mode %nick% +i"}},
			"q":       {Enabled: true, Settings: map[string]string{"hidehost": "no"}},
			"alias":   {Enabled: true, Settings: map[string]string{"aliases": "hi = /msg %1 hello"}},
		}}
	var rec logRec
	c := New(n, quiet, Options{OnLog: rec.add})
	run(t, c)
	eventually(t, "perform ran", func() bool { return hasLine(srv.got(), "MODE dora +i") })
	eventually(t, "q logged in", func() bool { return hasLine(srv.got(), "PRIVMSG Q@CServe.quakenet.org :AUTH dora pw") })
	for _, l := range srv.got() {
		if strings.Contains(l, "NickServ") {
			t.Fatalf("identified to NickServ although q logs in: %q", srv.got())
		}
	}
	if !rec.has("evrd", "perform: sent 1 command", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	// an alias, from an IRC client
	if !c.ClientLine("HI #home") {
		t.Fatal("the alias wasn't taken")
	}
	eventually(t, "the alias sent", func() bool { return hasLine(srv.got(), "PRIVMSG #home :hello") })
	if c.ClientLine("PRIVMSG #home :plain") {
		t.Fatal("a plain line was taken")
	}
}

func TestModulesChangeWithoutReconnecting(t *testing.T) {
	srv, host, port := startAuthServer(t, false)
	n := store.Network{ID: 42, Nick: "dora", Host: host, Port: port}
	c := New(n, quiet, Options{})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	if c.ClientLine("HI #home") {
		t.Fatal("an alias before any module")
	}
	n.Modules = map[string]store.ModuleConfig{"alias": {Enabled: true, Settings: map[string]string{"aliases": "hi = /msg %1 hello"}}}
	c.Reconfigure(n, "", nil)
	if !c.ClientLine("HI #home") {
		t.Fatal("the alias switched on live wasn't taken")
	}
	eventually(t, "sent", func() bool { return hasLine(srv.got(), "PRIVMSG #home :hello") })
	users := 0
	for _, l := range srv.got() {
		if strings.HasPrefix(l, "USER ") {
			users++
		}
	}
	if users != 1 {
		t.Fatalf("reconnected to change modules (%d sign-ins)", users)
	}
	// switched off server-wide: not started
	n.Modules["perform"] = store.ModuleConfig{Enabled: true}
	c2 := New(store.Network{ID: 43, Nick: "x", Modules: n.Modules}, quiet, Options{ModulesOff: func() map[string]bool { return map[string]bool{"alias": true} }})
	c2.startModules(t.Context())
	if c2.ClientLine("HI #home") {
		t.Fatal("a module switched off server-wide ran")
	}
}

func TestCTCPFloodIsDropped(t *testing.T) {
	c := New(store.Network{ID: 44, Nick: "dora", Modules: map[string]store.ModuleConfig{
		"ctcpflood": {Enabled: true, Settings: map[string]string{"limit": "1", "seconds": "60"}}}}, quiet, Options{})
	c.startModules(t.Context())
	c.ctcpReplies = map[string]string{}
	first := c.ctcp("a!a@h", "dora", "\x01VERSION\x01")
	second := c.ctcp("b!b@h", "dora", "\x01VERSION\x01")
	if first || !second {
		t.Fatalf("first handled=%v (should go to clients), second handled=%v (should be dropped)", first, second)
	}
}
