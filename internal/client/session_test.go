package client

import (
	"bufio"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

// closed reports whether the server closes the connection within a moment.
func (ic *ircClient) closed() bool {
	ic.c.SetReadDeadline(time.Now().Add(3 * time.Second))
	for ic.sc.Scan() {
	}
	return !isTimeout(ic.sc.Err())
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func TestAttachRefusals(t *testing.T) {
	r := newRig(t)

	for _, c := range []struct{ pass, want string }{
		{"alice", "464 alice :Password required"},
		{"alice:", "464 alice :Password required"},
		{"alice:wrong-password", "464 alice :Invalid credentials"},
		{"nobody:alice-password", "464 alice :Invalid credentials"},
		{"alice/nope:alice-password", "464 alice :Unknown network nope"},
	} {
		ic := r.attach(c.pass)
		ic.expect(c.want)
		if !ic.closed() {
			t.Fatalf("%s: connection left open", c.pass)
		}
	}

	// an account without networks
	if _, err := r.acc.Create(accountsInput("bob")); err != nil {
		t.Fatal(err)
	}
	ic := r.attach("bob:bob-password")
	ic.expect("No networks configured")

	// a suspended account
	u, _ := r.st.UserByUsername("bob")
	u.Suspended = true
	r.st.UpdateUser(u)
	r.attach("bob:bob-password").expect("465 alice :This account is suspended")

	// signing in from a host the account doesn't allow
	a := r.user
	a.AllowedHosts = []string{"192.0.2.0/24"}
	r.st.UpdateUser(a)
	r.attach("alice:alice-password").expect("Signing in from 127.0.0.1 isn't allowed for this account")
}

func TestSignInFailuresAreLogged(t *testing.T) {
	r := newRig(t)
	sam, err := r.acc.Create(accounts.CreateInput{Username: "sam"}) // SSO: no password of its own
	if err != nil {
		t.Fatal(err)
	}
	logged := func(want ...string) {
		t.Helper()
		eventually(t, strings.Join(want, " "), func() bool {
			for _, l := range strings.Split(r.logs.String(), "\n") {
				if strings.Contains(l, "irc sign-in") && containsAll(l, want...) {
					return true
				}
			}
			return false
		})
	}

	sso := "464 alice :Invalid credentials — this account signs in with SSO, so use an IRC client password from "
	r.attach("sam/one:sso-secret-1").expect(sso + "the web app's IRC clients page")
	logged("user=sam", "network=one", "ip=127.0.0.1", "reason=sso_needs_client_password")
	r.srv.SetPublicURL("https://evrd.example.org/")
	r.attach("sam:sso-secret-2").expect(sso + "https://evrd.example.org/clients")

	// Unknown users and password accounts get the plain refusal.
	for _, pass := range []string{"nobody:secret-3", "alice/two:secret-4"} {
		line := r.attach(pass).expect(" 464 ")
		if !strings.HasSuffix(line, " :Invalid credentials") {
			t.Errorf("%s: %q", pass, line)
		}
	}
	logged("user=nobody", "reason=invalid")
	logged("user=alice", "network=two", "reason=invalid")

	// A client password still works for the SSO account.
	_, secret, _ := r.acc.NewClientPassword(sam.ID, "laptop")
	r.attach("sam:" + secret).expect("No networks configured")

	r.attach("secret-5").expect("Password required")
	logged("without a password", "pass_sent=true")

	sam.Suspended = true
	r.st.UpdateUser(sam)
	r.attach("sam:" + secret).expect("This account is suspended")
	logged("user=sam", "reason=suspended")

	a := r.user
	a.AllowedHosts = []string{"192.0.2.0/24"}
	r.st.UpdateUser(a)
	r.attach("alice:alice-password").expect("isn't allowed")
	logged("user=alice", "reason=host_denied")

	for _, pw := range []string{"sso-secret", "secret-3", "secret-4", "secret-5", "alice-password", secret} {
		if strings.Contains(r.logs.String(), pw) {
			t.Errorf("the log has a password (%q):\n%s", pw, r.logs.String())
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func TestClientLimit(t *testing.T) {
	r := newRig(t)
	one := 1
	u := r.user
	u.MaxClients = &one
	r.st.UpdateUser(u)
	first := r.attach("alice/one:alice-password")
	first.expect(" 376 ")
	r.attach("alice/one:alice-password").expect("Too many clients attached (limit 1)")

	// a client leaving frees its slot
	first.send("QUIT :bye")
	if !first.closed() {
		t.Fatal("QUIT didn't close the connection")
	}
	eventually(t, "slot freed", func() bool { return r.b.Clients(u.ID) == 0 })
	r.attach("alice/one:alice-password").expect(" 376 ")
}

func TestNotOwnerIsToldToReconnect(t *testing.T) {
	r := newRig(t)
	r.b.SetOwnership(func(int64) bool { return false })
	r.attach("alice:alice-password").expect(movingMsg)
}

func TestRegistrationEdges(t *testing.T) {
	r := newRig(t)
	// QUIT before registering
	c, _ := net.Dial("tcp", r.addr)
	defer c.Close()
	io.WriteString(c, "NICK x\r\nQUIT\r\n")
	c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadAll(c); err != nil {
		t.Fatalf("not closed after QUIT: %v", err)
	}

	// an endless line
	c2, _ := net.Dial("tcp", r.addr)
	defer c2.Close()
	io.WriteString(c2, "NICK "+strings.Repeat("x", 2*maxLine))
	c2.SetReadDeadline(time.Now().Add(3 * time.Second))
	// closed, possibly with our unread line reset
	if _, err := io.ReadAll(c2); isTimeout(err) {
		t.Fatalf("not closed after a too-long line: %v", err)
	}

	// CAP during registration holds it until END; a bare CAP and REQ are ignored
	c3, err := net.Dial("tcp", r.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c3.Close()
	ic := &ircClient{t: t, c: c3, sc: bufio.NewScanner(c3)}
	ic.send("CAP")
	ic.send("CAP REQ")
	ic.send("CAP LS 302")
	for _, l := range []string{"PASS alice/one:alice-password", "NICK alice", "USER alice 0 * :alice"} {
		ic.send(l)
	}
	ic.expect("CAP * LS :batch cap-notify draft/chathistory draft/event-playback echo-message message-tags server-time")
	ic.send("CAP REQ :server-time")
	ic.expect("ACK :server-time")
	ic.send("CAP REQ :-server-time")
	ic.expect("ACK :-server-time")
	ic.send("CAP END")
	ic.expect(" 001 alice_one ")
	ic.send("CAP LIST")
	if l := ic.expect(" LIST :"); !strings.HasSuffix(l, "LIST :cap-notify") { // CAP 302 implies cap-notify
		t.Fatalf("server-time is still on: %q", l)
	}
}

func TestClientCommands(t *testing.T) {
	r := newRig(t)
	ic := r.attached("echo-message")
	n := r.network("one")
	eventually(t, "joined #one", func() bool {
		c, ok := r.b.ConnFor(n.ID)
		return ok && c.Joined("#one")
	})

	ic.send("PING :tok")
	ic.expect(":evrd PONG evrd :tok")
	ic.send("USER again 0 * :x")
	ic.expect(" 462 alice_one :You're already registered")
	ic.send("PASS again")
	ic.expect(" 462 ")

	// echo-message: the client sees its own line
	ic.send("PRIVMSG #one :hello")
	ic.expect(":alice_one!alice_one@evrd PRIVMSG #one :hello")

	// anything else goes to the server as is
	ic.send("TOPIC #one")
	ic.expect(" 332 alice_one #one :the topic")

	// JOIN of a detached channel brings it back; the rest go upstream with
	// their keys
	ic.send("DETACH #one")
	ic.expect("PART #one :Detached")
	ic.send("JOIN #one,#keyed sekrit,key2")
	ic.expect("alice_one@evrd JOIN #one")
	ic.expect(":alice_one!u@h JOIN #keyed")
	eventually(t, "#one attached", func() bool { return !r.network("one").Channel("#one").Detached })

	// the CTCP VERSION reply names the client
	ic.send("NOTICE *status :\x01VERSION HexChat 2.16.1 [x86_64] / Linux\x01")
	eventually(t, "software noted", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].Software == "HexChat" && cs[0].Networks[0] == "one"
	})
	// a NOTICE to *status that isn't one is ignored; PRIVMSG without text too
	ic.send("NOTICE *status :hi")
	ic.send("PRIVMSG #one")
	ic.status("version", "evrd ")
}

func TestOtherSessionsStayInSync(t *testing.T) {
	r := newRig(t)
	first := r.attached()
	second := r.attach("alice/one:alice-password")
	second.expect(" 376 ")
	second.send("PRIVMSG #one :from the second client")
	first.expect(":alice_one!alice_one@one PRIVMSG #one :from the second client")
	// and the sender doesn't get it twice
	second.send("PRIVMSG *status :version")
	if seen := second.until("evrd "); anyHas(seen, "from the second client") {
		t.Fatalf("echoed to its sender: %q", seen)
	}
}

func TestRelayHidesBouncerTraffic(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	for _, l := range []string{
		"PING :hidden-ping",
		":fake 002 alice_one :hidden-002",
		":fake 005 alice_one MONITOR=100 :are supported", // ISUPPORT passes
		":fake CAP alice_one ACK :hidden-cap",
		":fake 001 renamed :welcome back", // a re-registration under another nick
		":bob!u@h PRIVMSG #one :visible",
	} {
		ic.send("SIMULATE " + l)
	}
	seen := ic.until("PRIVMSG #one :visible")
	if anyHas(seen, "hidden") || anyHas(seen, "welcome back") {
		t.Fatalf("bouncer traffic reached the client: %q", seen)
	}
	if !anyHas(seen, ":alice_one!alice_one@evrd NICK renamed") || !anyHas(seen, "005 alice_one MONITOR=100") {
		t.Fatalf("no NICK for the new registration: %q", seen)
	}
	ic.status("info", "PRIVMSG renamed :Account alice")
}

func TestAttachWithClientPassword(t *testing.T) {
	r := newRig(t)
	_, secret, err := r.acc.NewClientPassword(r.user.ID, "phone")
	if err != nil {
		t.Fatal(err)
	}
	r.attach("alice:" + secret).expect(" 376 ")
	eventually(t, "attached with the device", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].Device == "phone" && cs[0].IP == "127.0.0.1"
	})
}

func TestFormat(t *testing.T) {
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	se := &session{caps: map[string]bool{}, net: store.Network{Name: "net"}}
	msg := func(kind, sender, text string) history.Message {
		return history.Message{Target: "#c", Sender: sender, Kind: kind, Text: text, Time: at}
	}
	for _, c := range []struct {
		m    history.Message
		want string
	}{
		{msg("message", "bob", "hi"), ":bob!bob@net PRIVMSG #c :hi"},
		{msg("notice", "bob", "hey"), ":bob!bob@net NOTICE #c :hey"},
		{msg("action", "bob", "waves"), ":bob!bob@net PRIVMSG #c :\x01ACTION waves\x01"},
		{msg("", "", "server says"), ":*!*@net PRIVMSG #c :server says"},
		{msg("part", "bob", ""), ":*buffextras!buffextras@evrd PRIVMSG #c :bob left"},
	} {
		if got := se.format(c.m); got != c.want {
			t.Errorf("format %+v: got %q, want %q", c.m, got, c.want)
		}
	}
	se.caps["server-time"] = true
	if got := se.format(msg("message", "bob", "hi")); got != "@time=2026-01-02T03:04:05.000Z :bob!bob@net PRIVMSG #c :hi" {
		t.Errorf("server-time: %q", got)
	}
	// live lines (not playback) carry no timestamp text
	se.caps = map[string]bool{}
	se.acct.PrependTimestamp = true
	if got := se.formatAs(msg("message", "bob", "hi"), false); got != ":bob!bob@net PRIVMSG #c :hi" {
		t.Errorf("live line: %q", got)
	}
}

func TestDescribe(t *testing.T) {
	for _, c := range []struct {
		m    history.Message
		want string
	}{
		{history.Message{Kind: "part", Sender: "bob", Text: "later"}, "bob left (later)"},
		{history.Message{Kind: "part", Sender: "bob"}, "bob left"},
		{history.Message{Kind: "quit", Sender: "bob"}, "bob quit"},
		{history.Message{Kind: "kick", Sender: "op", Extra: "bob"}, "op kicked bob"},
		{history.Message{Kind: "invite", Sender: "bob", Text: "#c"}, "bob invite #c"},
	} {
		if got := describe(c.m); got != c.want {
			t.Errorf("describe %+v: got %q, want %q", c.m, got, c.want)
		}
	}
}

func TestSmallHelpers(t *testing.T) {
	for in, want := range map[string]string{
		"HexChat 2.16.1 [x86_64] / Linux": "HexChat",
		"irssi: v1.4.5":                   "irssi",
		"":                                "",
		strings.Repeat("y", 40):           strings.Repeat("y", 32),
	} {
		if got := clientName(in); got != want {
			t.Errorf("clientName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := lastParam(irc.Parse("PING")); got != "" {
		t.Errorf("lastParam of no params: %q", got)
	}
	for in, want := range map[string]string{
		":evrd 001 me #chan :x":         "#chan",
		":evrd 353 me = #chan :a b":     "#chan",
		":evrd 001 me a b #toofar":      "",
		":bob!u@h PRIVMSG &local :text": "&local",
	} {
		if got := lineChannel(irc.Parse(in)); got != want {
			t.Errorf("lineChannel(%q) = %q, want %q", in, got, want)
		}
	}
	if s, a := parseListen("127.0.0.1:6667"); s != "tcp" || a != "127.0.0.1:6667" {
		t.Errorf("parseListen without scheme: %q %q", s, a)
	}
	if capitalize("") != "" || plural(1, "line") != "1 line" || onOff(true) != "on" {
		t.Error("small helpers")
	}
}

func accountsInput(name string) accounts.CreateInput {
	return accounts.CreateInput{Username: name, Password: name + "-password"}
}
