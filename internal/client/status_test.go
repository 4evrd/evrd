package client

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/directory"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// status sends a *status command and waits for a reply containing want.
func (ic *ircClient) status(cmd, want string) string {
	ic.t.Helper()
	ic.send("PRIVMSG *status :" + cmd)
	return ic.expect(want)
}

// tryExpect is expect that gives up quietly after d.
func (ic *ircClient) tryExpect(want string, d time.Duration) bool {
	ic.c.SetReadDeadline(time.Now().Add(d))
	for ic.sc.Scan() {
		if strings.Contains(ic.sc.Text(), want) {
			return true
		}
	}
	// a timed-out scanner stays failed: start a fresh one
	ic.sc = bufio.NewScanner(ic.c)
	return false
}

// attached attaches to network one and waits for its link and channel.
func (r *rig) attached(caps ...string) *ircClient {
	r.t.Helper()
	ic := r.attach("alice/one:alice-password", caps...)
	ic.expect(" 376 ")
	n := r.network("one")
	eventually(r.t, "one connected", func() bool { return r.b.Status(n.ID) == upstream.StatusConnected })
	return ic
}

func TestStatusConnectAndDisconnect(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	one, two := r.network("one"), r.network("two")

	ic.status("connect", "one is already connected.")
	ic.status("disconnect two", "two isn't connected.")
	ic.status("connect nope", `No network called "nope"`)
	ic.status("disconnect nope", `No network called "nope"`)

	ic.status("connect two", fmt.Sprintf("Connecting to two (%s:%d)", two.Host, two.Port))
	eventually(t, "two connected", func() bool { return r.b.Status(two.ID) == upstream.StatusConnected })
	if !r.network("two").Enabled {
		t.Fatal("connect didn't switch two on")
	}
	ic.status("disconnect two", "Disconnected from two.")
	if r.b.Status(two.ID) != upstream.StatusDisconnected || r.network("two").Enabled {
		t.Fatal("two still on after disconnect")
	}

	// this client's own network: it stays attached
	ic.status("disconnect", "Disconnected from one. You're still attached")
	if r.network("one").Enabled {
		t.Fatal("one still on after disconnect")
	}
	ic.status("connect", "Connecting to one")
	eventually(t, "one back", func() bool { return r.b.Status(one.ID) == upstream.StatusConnected })

	// The session follows the new connection: the server's lines reach
	// the client again (it checks every few seconds).
	for i := 0; ; i++ {
		ic.send(fmt.Sprintf("SIMULATE :bob!u@h PRIVMSG #one :ping %d", i))
		if ic.tryExpect("PRIVMSG #one :ping", 500*time.Millisecond) {
			break
		}
		if i == 12 {
			t.Fatal("the session never followed the new connection")
		}
	}
}

func TestStatusChannelsAndDetach(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	eventually(t, "joined #one", func() bool {
		c, ok := r.b.ConnFor(r.network("one").ID)
		return ok && c.Joined("#one")
	})
	ic.status("listchans", "one: #one")
	ic.status("detach", "Usage: detach <#channel>")
	ic.status("detach #nope", "You're not on #nope.")
	ic.status("detach #one", "PART #one :Detached")
	eventually(t, "detached saved", func() bool { return r.network("one").Channel("#one").Detached })
}

func TestZNCRawCommand(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	// /znc ListNetworks: one word, then with arguments split as a client sends them
	ic.send("ZNC ListNetworks")
	ic.expect("one")
	ic.send("ZNC addnetwork three irc.example.org")
	ic.expect("Added three (irc.example.org:+6697")
	ic.send("ZNC :help")
	ic.expect("evrd commands")
}

func TestStatusNetworkCommandErrors(t *testing.T) {
	r := newRig(t)
	ic := r.attached()

	ic.status("jumpnetwork", "Usage: jumpnetwork <network>")
	ic.status("jump one", "You're already on one.")
	ic.status("jumpnetwork nope", `No network called "nope"`)
	ic.status("jump nope", `"nope" is neither one of your networks nor a server of one`)

	ic.status("addnetwork", "Usage: addnetwork")
	ic.status("addnetwork lonely", `No network "lonely" in the directory`)
	ic.status("addnetwork x irc.example.org port", `"port" isn't a port`)
	ic.status("addnetwork x irc.example.org 70000", `"70000" isn't a port`)
	ic.status("addnet one irc.example.org", "You already have a network called one.")
	// default: TLS on 6697, the username as nick
	ic.status("addnetwork three irc.example.org", "Added three (irc.example.org:+6697, nick alice)")
	ic.status("addnetwork four irc.example.org 6667 bob", "limit of 3 networks")
	ic.status("delnetwork", "Usage: delnetwork <network>")
	ic.status("delnetwork nope", `No network called "nope"`)
	ic.status("delnet three", "Removed three and its history.")
	ic.status("addnetwork four irc.example.org 6667 bob", "Added four (irc.example.org:6667, nick bob)")
	n := r.network("four")
	if n.TLS || n.Port != 6667 || n.Nick != "bob" || n.Enabled {
		t.Fatalf("added network: %+v", n)
	}

	ic.status("show one two", "Usage: show [network]")
	ic.status("show nope", `No network called "nope"`)
	ic.status("show four", "four — disconnected")
	ic.expect("server 1: irc.example.org:6667")
	ic.expect("nick bob · altnick (default) · ident (nick) · realname (nick)")
	ic.expect("sasl password not set")
	ic.expect("quitmsg (default) · encoding UTF-8")
	ic.expect("trustallcerts off")
	ic.expect("channels none")
}

func TestStatusSetEverySetting(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	two := r.network("two")

	ic.status("set", "Usage: set [network]")
	ic.status("set nick", "Usage: set [network]")
	ic.status("set two bogus x", "Usage: set [network]")
	ic.status("set nope nick bob", `No network called "nope"`)

	// settings that apply live on a stopped network
	for _, c := range []struct{ cmd, want string }{
		{"set two altnick bob_", "Saved altnick = bob_ on two."},
		{"set two sasluser bobby", "Saved sasluser = bobby on two."},
		{"set two encoding latin1", "Saved encoding = latin1 on two."},
		{"set two encoding klingon", `"klingon" isn't an encoding`},
		{"set two trustallcerts yes", "Saved trustallcerts = yes on two."},
		{"set two trustpki off", "Saved trustpki = off on two."},
		{"set two trustpki maybe", "trustpki is on or off."},
		{"set two tls on", "Saved tls = on on two."},
		{"set two tls sometimes", "tls is on or off."},
		{"set two port nope", `"nope" isn't a port`},
		{"set two floodrate x", "floodrate is lines per second"},
		{"set two floodburst many", "floodburst is a whole number."},
		{"set two floodburst 20", "Saved floodburst = 20 on two."},
		{"set two floodrate 5", "Saved floodrate = 5 on two."},
		{"set two floodrate 500", "The flood rate is lines per second, 0–100"},
		{"set two floodburst 0", "The flood burst is 1–1000 lines."},
		{"set two joindelay 3", "Saved joindelay = 3 on two."},
		{"set two ident bobident", "Saved ident = bobident on two."},
		{"set two realname Bob B", "Saved realname = Bob B on two."},
		{"set two host irc.example.net", "Saved host = irc.example.net on two."},
		{"set two nick bobby", "Saved nick = bobby on two."},
		{"set two name second", "Saved name = second on second."},
	} {
		ic.status(c.cmd, c.want)
	}
	n, _ := r.st.NetworkByID(two.ID)
	if n.AltNick != "bob_" || n.SASLUser != "bobby" || n.Encoding != "latin1" || !n.TrustAllCerts || n.TrustPKI ||
		!n.TLS || n.FloodRate != 5 || n.FloodBurst != 20 || n.JoinDelay != 3 || n.Username != "bobident" ||
		n.Realname != "Bob B" || n.Host != "irc.example.net" || n.Nick != "bobby" || n.Name != "second" {
		t.Fatalf("saved: %+v", n)
	}
	ic.status("show second", "second — disconnected")
	ic.expect("server 1: irc.example.net:")
	ic.expect("nick bobby · altnick bob_ · ident bobident · realname Bob B")
	ic.expect("sasl password not set · sasl user bobby")
	ic.expect("encoding latin1")
	ic.expect("trustallcerts on · trustpki off")
	ic.expect("floodrate 5 · floodburst 20 · joindelay 3s")

	// "-" clears
	for _, c := range []string{"altnick", "sasluser", "encoding", "ident", "realname"} {
		ic.status("set second "+c+" -", "Saved "+c+" = (cleared) on second.")
	}
	n, _ = r.st.NetworkByID(two.ID)
	if n.AltNick != "" || n.SASLUser != "" || n.Encoding != "" || n.Username != "" || n.Realname != "" {
		t.Fatalf("not cleared: %+v", n)
	}
}

func TestStatusSetServicesLogin(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	two := r.network("two")
	for _, c := range []struct{ cmd, want string }{
		{"set two auth command", "The command login needs a command"},
		{"set two authcommand /msg X@channels.undernet.org LOGIN %account% %password%", "Saved authcommand = (hidden) on two."},
		{"set two auth command", "Saved auth = command on two."},
		{"set two auth carrier-pigeon", "The login method is auto, sasl"},
		{"set two servicesnick NickServ@services.dal.net", "Saved servicesnick = NickServ@services.dal.net on two."},
		{"set two identifyargs password", "Saved identifyargs = password on two."},
		{"set two identifyon prompt", "Saved identifyon = prompt on two."},
		{"set two ghost regain", "Saved ghost = regain on two."},
		{"set two joinafterauth maybe", "joinafterauth is on or off."},
		{"set two joinafterauth on", "Saved joinafterauth = on on two."},
		{"set two authtimeout soon", "authtimeout is a whole number."},
		{"set two authtimeout 30", "Saved authtimeout = 30 on two."},
	} {
		ic.status(c.cmd, c.want)
	}
	n, _ := r.st.NetworkByID(two.ID)
	if n.AuthMethod != store.AuthCommand || n.ServicesNick != "NickServ@services.dal.net" || n.IdentifyArgs != "password" ||
		n.IdentifyOn != "prompt" || n.GhostCmd != "regain" || !n.JoinAfterAuth || n.AuthTimeout != 30 ||
		!strings.HasPrefix(n.AuthCommand, "/msg X@") {
		t.Fatalf("saved: %+v", n)
	}
	ic.status("show two", "two — disconnected")
	ic.expect("auth command · servicesnick NickServ@services.dal.net · identifyargs password · identifyon prompt · ghost regain · joinafterauth on · authtimeout 30s")
	ic.expect("authcommand /msg X@channels.undernet.org LOGIN %account% %password%")
	ic.status("set two ghost off", "Saved ghost = off on two.")
	ic.status("set two identifyargs auto", "Saved identifyargs = auto on two.")
	n, _ = r.st.NetworkByID(two.ID)
	if n.GhostCmd != "" || n.IdentifyArgs != "" {
		t.Fatalf("not cleared: %+v", n)
	}
}

func TestStatusPlayback(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	n := r.network("one")
	now := time.Now()
	for i := 1; i <= 3; i++ {
		r.hist.Add(history.Message{NetworkID: n.ID, Target: "carol", Sender: "carol", Kind: "message", Text: fmt.Sprintf("line %d", i), Time: now})
	}
	ic.status("playback", "Usage: playback")
	ic.status("playback a b c", "Usage: playback")
	ic.status("playback carol many", `"many" isn't a number of lines.`)
	ic.status("playback carol 0", `"0" isn't a number of lines.`)
	ic.status("playback nobody", "Nothing saved for nobody.")
	ic.send("PRIVMSG *status :playback carol 2")
	seen := ic.until("Replayed 2 line(s) of carol.")
	if anyHas(seen, "line 1") || !anyHas(seen, ":carol!carol@one PRIVMSG carol :") || !anyHas(seen, "line 3") {
		t.Fatalf("playback of the last two: %q", seen)
	}
}

func TestStatusServersAndFingerprintErrors(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("addserver", "Usage: addserver")
	ic.status("addserver h 1 2 3", "Usage: addserver")
	ic.status("addserver h x", `"x" isn't a port`)
	ic.status("addserver bad/host", `"bad/host" isn't a server hostname.`)
	ic.status("delserver", "Usage: delserver")
	ic.status("delserver nope", "one has no server nope.")

	// changing servers of a running network reconnects it
	ic.status("addserver irc.example.org 6667", "Added irc.example.org:6667 to one. It's used when the link drops. Reconnecting to apply it…")
	ic.status("delserver irc.example.org 6668", "one has no server irc.example.org.")
	ic.status("delserver irc.example.org +6667", "Removed irc.example.org from one.")

	ic.status("listfingerprints", "No trusted fingerprints on one.")
	ic.status("addfingerprint", "Usage: addfingerprint")
	ic.status("addfingerprint xyz", "Certificate fingerprints are SHA-256")
	ic.status("delfingerprint", "Usage: delfingerprint")
	ic.status("delfingerprint "+strings.Repeat("cd", 32), "That fingerprint isn't trusted on one.")
}

func TestStatusInfoAndVersion(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("version", "evrd ")
	ic.status("info", "Account alice (")
	ic.expect("Networks: 2 of 3. Clients attached: 1 of 5. Backlog: unlimited lines per buffer.")
	ic.status("", "evrd commands")
}

func TestWebStatus(t *testing.T) {
	r := newRig(t)
	run := func(n store.Network, cmd string) []string {
		var out []string
		r.srv.WebStatus(r.user.ID, n, cmd, func(s string) { out = append(out, s) })
		return out
	}
	has := func(lines []string, s string) bool { return anyHas(lines, s) }
	one := r.network("one")

	if out := run(one, "networks"); !has(out, "* one") || !has(out, "2 networks.") {
		t.Fatalf("networks: %q", out)
	}
	if out := run(one, "jump two"); !has(out, "pick a network from the Networks page") {
		t.Fatalf("jump: %q", out)
	}
	if out := run(one, "playback #one"); !has(out, "already shows each buffer's history") {
		t.Fatalf("playback: %q", out)
	}
	if out := run(one, "detach #one"); !has(out, "the web chat shows everything") {
		t.Fatalf("detach: %q", out)
	}
	// not running: the saved channels
	if out := run(one, "channels"); !has(out, "one: #one") {
		t.Fatalf("channels: %q", out)
	}
	run(one, "addnetwork three irc.example.org")
	if out := run(r.network("three"), "channels"); !has(out, "No channels on three yet.") {
		t.Fatalf("channels of a new network: %q", out)
	}
	// running: the connection's nick and channels
	if out := run(one, "connect"); !has(out, "Connecting to one") {
		t.Fatalf("connect: %q", out)
	}
	eventually(t, "one connected", func() bool { return r.b.Status(one.ID) == upstream.StatusConnected })
	if out := run(one, "info"); !has(out, "On one as alice_one.") {
		t.Fatalf("info: %q", out)
	}
	// a deleted network
	gone := store.Network{ID: 999, Name: "gone"}
	if out := run(gone, "show"); !has(out, "This network no longer exists.") {
		t.Fatalf("show gone: %q", out)
	}
}

func TestStatusDirectory(t *testing.T) {
	r := newRig(t)
	run := func(cmd string) []string {
		var out []string
		r.srv.WebStatus(r.user.ID, r.network("one"), cmd, func(s string) { out = append(out, s) })
		return out
	}
	if out := run("directory"); !anyHas(out, "The network directory is empty here") {
		t.Fatalf("no directory: %q", out)
	}
	path := filepath.Join(t.TempDir(), "networks.json")
	os.WriteFile(path, []byte(`{"version": 1, "networks": [
		{"id": "libera", "name": "Libera.Chat", "description": "FOSS", "servers": [{"host": "irc.libera.chat", "port": 6697, "tls": true}, {"host": "irc.libera.chat", "port": 6667}], "channels": ["#libera"]},
		{"id": "efnet", "name": "EFnet", "servers": [{"host": "irc.efnet.org", "port": 6667}]}
	]}`), 0o644)
	d := directory.New(path, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := d.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.srv.SetDirectory(d)
	if out := run("directory"); !anyHas(out, "libera") || !anyHas(out, "irc.libera.chat:+6697") || !anyHas(out, "irc.efnet.org:6667") || !anyHas(out, "2 networks.") {
		t.Fatalf("directory: %q", out)
	}
	if out := run("directory foss"); !anyHas(out, "1 network.") {
		t.Fatalf("search: %q", out)
	}
	if out := run("directory nothing here"); !anyHas(out, `No network in the directory matches "nothing here"`) {
		t.Fatalf("no match: %q", out)
	}
	// by id: the TLS servers and the suggested channels
	if out := run("addnetwork Libera"); !anyHas(out, "Added libera (irc.libera.chat:+6697, nick alice)") {
		t.Fatalf("addnetwork: %q", out)
	}
	n := r.network("libera")
	if len(n.Servers) != 1 || !n.Servers[0].TLS || n.Port != 6697 || len(n.Channels) != 1 || n.Channels[0] != "#libera" {
		t.Fatalf("added: %+v", n)
	}
	if out := run("addnetwork nope"); !anyHas(out, `No network "nope" in the directory`) {
		t.Fatalf("unknown id: %q", out)
	}
}

func TestStatusZNCOddsAndEnds(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	// bind hosts: this evrd offers none
	ic.status("showbindhost", "This network's bind host: (not set) · your account's: (not set)")
	ic.status("setbindhost", "Usage: setbindhost <host>")
	ic.status("setbindhost 192.0.2.1", "doesn't offer bind hosts")
	ic.status("clearbindhost", "Saved bindhost = (cleared)")
	ic.status("setuserbindhost", "Usage: setuserbindhost <host>")
	ic.status("setuserbindhost 192.0.2.1", "doesn't offer bind hosts")
	ic.status("clearuserbindhost", "Cleared your account's bind host.")

	// admins only
	ic.status("listusers", "Admins only.")
	ic.status("listallusernetworks", "Admins only.")
	u, _ := r.st.UserByUsername("alice")
	u.Role = store.RoleAdmin
	r.st.UpdateUser(u)
	other := r.attached()
	other.status("listusers", "alice")
	other.status("listusers", "1 user.")
	other.status("listallusernetworks", "2 networks.")

	ic.status("traffic", "on each network's health page")
	r.srv.SetPublicURL("https://evrd.example/")
	ic.status("traffic", fmt.Sprintf("Traffic for one: https://evrd.example/networks/%d/health", r.network("one").ID))
	for _, alias := range []string{"listmods", "listavailmods", "listnetmods"} {
		ic.status(alias, "loadmod <module>")
	}
	ic.status("listbuffers", "#one")
	for cmd, why := range notHere {
		ic.status(cmd, why)
	}
	ic.status("listnicks", "Usage: listnicks <#channel>")
	ic.status("showchan", "Usage: showchan <#channel>")
	ic.status("showchan #nope", "#nope on one — not saved")
	ic.status("attach", "Usage: attach")
	ic.status("attach #nope", "You're not on #nope.")
	ic.status("disablechan", "Usage: disablechan")
	ic.status("enablechan", "Usage: enablechan")
	ic.status("jump a b", "Usage: jump [server|network]")
}

func TestJumpToANamedServer(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	n := r.network("one")
	ic.status(fmt.Sprintf("addserver localhost %d", n.Port), "Added localhost")
	ic.status("jump localhost", fmt.Sprintf("Reconnecting one to localhost:%d", n.Port))
	eventually(t, "on localhost", func() bool {
		c, ok := r.b.ConnFor(n.ID)
		return ok && r.b.Status(n.ID) == upstream.StatusConnected && strings.HasPrefix(c.Health().Server, "localhost:")
	})
	ic.status("disconnect", "Disconnected from one")
	ic.status("jump", "one isn't connected")
}

func TestStatusHelpInCaps(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("help", "| CLEARBUFFER            | <channel\\|nick>[,…]")
	ic.status("help", "SET's settings: name, host, port")
	ic.status("HeLp", "Commands are case-insensitive.")
	// a *status command sent as its own goes to the network
	ic.send("CLEARBUFFER #one")
	eventually(t, "sent on", func() bool { return r.irc.Has("CLEARBUFFER #one") })
}

func TestMarkdownTable(t *testing.T) {
	got := strings.Join(markdownTable([][]string{{"Command", "Arguments", "Description"}, {"JUMP", "[server|network]", "Reconnect."}, {"UPTIME", "", "How long."}}), "\n")
	want := strings.Join([]string{
		"| Command | Arguments         | Description",
		"| ------- | ----------------- | ---",
		"| JUMP    | [server\\|network] | Reconnect.",
		"| UPTIME  |                   | How long.",
	}, "\n")
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}
