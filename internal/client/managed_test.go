package client

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/managed"
	"github.com/4evrd/evrd/internal/managed/managedtest"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

// managedRig is evrd in managed mode in front of a fake IRC server, with a
// fake token issuer, and every line any of its clients was sent.
type managedRig struct {
	t      *testing.T
	st     store.Store
	acc    *accounts.Service
	b      *bouncer.Bouncer
	srv    *Server
	addr   string
	irc    *testirc.Log
	issuer *managedtest.Issuer
	prov   *managed.Provisioner
	mode   *managed.Mode

	mu   sync.Mutex
	seen []string // every line sent to any client
}

func newManagedRig(t *testing.T, before ...func(*bouncer.Bouncer)) *managedRig {
	t.Helper()
	data := scratchDir(t)
	st, err := store.Open("sqlite:" + filepath.Join(data, "evrd.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hist, err := history.Open(filepath.Join(data, "history"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hist.Close() })
	log := slog.New(slog.NewTextHandler(&syncBuffer{}, nil))
	acc := accounts.New(st, config.Limits{MaxClients: 3})

	ircLog := &testirc.Log{}
	host, port, _ := net.SplitHostPort(testirc.StartLogged(t, ircLog))
	issuer := managedtest.New(t)
	cfg := config.Default()
	cfg.Managed.On = true
	cfg.Managed.Network = "ExampleNet"
	cfg.Managed.Host = host
	fmt.Sscan(port, &cfg.Managed.Port)
	cfg.Managed.ServerName = "irc.example.org"
	cfg.Managed.Token = "api-token"
	cfg.Managed.TokenIssuer, cfg.Managed.TokenAudience = issuer.URL, managedtest.Audience
	cfg.Managed.WebIRCPassword, cfg.Managed.WebIRCGateway, cfg.Managed.WebIRCResolve = "webirc-secret", "boson", false
	cfg.Managed.MOTD = []string{"Be excellent to each other."}
	cfg.Managed.VersionReply = "Boson"
	if err := cfg.CheckManaged(); err != nil {
		t.Fatal(err)
	}
	mode := managed.New(cfg)

	b := bouncer.New(st, hist, log)
	mc := mode.Config()
	b.SetManaged(bouncer.Managed{AwayMessage: mc.AwayMessage, VersionReply: mc.VersionReply, ReplyFrom: mode.ServerName(), WebIRC: mode.WebIRC})
	for _, f := range before {
		f(b)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	b.Start(ctx)

	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	srv := NewServer(b, acc, log, "", "")
	srv.followEvery = 100 * time.Millisecond
	srv.lsWait = 200 * time.Millisecond
	srv.pingQuiet = 400 * time.Millisecond
	srv.SetManaged(mode)
	srv.SetSignIns(&fakeSignIns{fails: auth.NewFailureLimiter(4, time.Minute)})
	go srv.ListenAndServe(ctx, []string{"irc+insecure://" + addr})
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	r := &managedRig{t: t, st: st, acc: acc, b: b, srv: srv, addr: addr, irc: ircLog, issuer: issuer, mode: mode,
		prov: &managed.Provisioner{Mode: mode, Accounts: acc, Bouncer: b}}
	return r
}

// put provisions an account through the managed API's logic.
func (r *managedRig) put(username string, in managed.UserInput) managed.User {
	r.t.Helper()
	u, err := r.prov.Put(context.Background(), username, in)
	if err != nil {
		r.t.Fatal(err)
	}
	return u
}

// recClient is a client whose every line is kept (in the rig's seen too).
type recClient struct {
	r     *managedRig
	c     net.Conn
	lines chan string
}

// dial connects a client and starts recording what it's sent.
func (r *managedRig) dial() *recClient {
	r.t.Helper()
	c, err := net.Dial("tcp", r.addr)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	rc := &recClient{r: r, c: c, lines: make(chan string, 1000)}
	go func() {
		sc := bufio.NewScanner(c)
		for sc.Scan() {
			r.mu.Lock()
			r.seen = append(r.seen, sc.Text())
			r.mu.Unlock()
			if p, ok := strings.CutPrefix(sc.Text(), "PING "); ok {
				rc.send("PONG " + p) // as clients do
			}
			select {
			case rc.lines <- sc.Text():
			default: // nobody's reading this one any more
			}
		}
		close(rc.lines)
	}()
	return rc
}

func (rc *recClient) send(line string) { fmt.Fprintf(rc.c, "%s\r\n", line) }

// expect waits for a line containing want, returning it.
func (rc *recClient) expect(want string) string {
	rc.r.t.Helper()
	var saw []string
	deadline := time.After(10 * time.Second)
	for {
		select {
		case l, ok := <-rc.lines:
			if !ok {
				rc.r.t.Fatalf("closed before %q; saw:\n%s", want, strings.Join(saw, "\n"))
			}
			if strings.Contains(l, want) {
				return l
			}
			saw = append(saw, l)
		case <-deadline:
			rc.r.t.Fatalf("never got %q; saw:\n%s", want, strings.Join(saw, "\n"))
		}
	}
}

// expectClosed waits for the connection to end.
func (rc *recClient) expectClosed() {
	rc.r.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-rc.lines:
			if !ok {
				return
			}
		case <-deadline:
			rc.r.t.Fatal("still connected")
		}
	}
}

// bearer signs in with SASL mech and token.
func (r *managedRig) bearer(mech, token string, caps ...string) *recClient {
	rc := r.dial()
	rc.send("CAP LS 302")
	rc.send("NICK alice")
	rc.send("USER alice 0 * :Alice")
	rc.send("CAP REQ :" + strings.Join(append([]string{"sasl"}, caps...), " "))
	rc.send("AUTHENTICATE " + mech)
	rc.expect("AUTHENTICATE +")
	var payload string
	switch mech {
	case "OAUTHBEARER":
		payload = "n,,\x01auth=Bearer " + token + "\x01\x01"
	case "IRCV3BEARER":
		payload = "\x00jwt\x00" + token
	case "PLAIN":
		payload = "\x00alice\x00" + token
	}
	enc := base64.StdEncoding.EncodeToString([]byte(payload))
	for len(enc) >= 400 {
		rc.send("AUTHENTICATE " + enc[:400])
		enc = enc[400:]
	}
	if enc == "" {
		enc = "+"
	}
	rc.send("AUTHENTICATE " + enc)
	return rc
}

// The white-label rule: nothing any client is sent — connecting, the
// network dropping and coming back, refused sign-ins, the lockout, the
// client limit, the bouncer's commands — names evrd, and *status is
// nowhere.
func TestManagedClientsNeverSeeEvrd(t *testing.T) {
	r := newManagedRig(t)
	alice := r.put("alice", managed.UserInput{Subject: new("usr_alice"), Channels: &[]string{"#chat"}})
	tok := r.issuer.Token("usr_alice", "alice")

	// sign in with OAUTHBEARER: the welcome is the network's, through the
	// server name, with the managed MOTD
	c := r.bearer("OAUTHBEARER", tok, "message-tags", "server-time", "batch", "echo-message")
	c.expect("903")
	c.send("CAP END")
	c.expect(":irc.example.org 001 alice :Welcome to the ExampleNet IRC Network alice")
	if l := c.expect(" 005 "); !strings.Contains(l, "NETWORK=ExampleNet") {
		t.Errorf("ISUPPORT: %q", l)
	}
	c.expect("Be excellent to each other.")
	c.expect("JOIN #chat")
	eventually(t, "WEBIRC with the client's address", func() bool { return r.irc.Has("WEBIRC webirc-secret boson 127.0.0.1 127.0.0.1") })
	if first := r.irc.Lines()[0]; !strings.HasPrefix(first, "WEBIRC ") {
		t.Errorf("the connection opened with %q", first)
	}

	// the bouncer's commands and nicks aren't there
	c.send("PRIVMSG *status :help")
	c.expect("401 alice *status :No such nick/channel")
	c.send("PRIVMSG *controlpanel :help")
	c.expect("401 alice *controlpanel :No such nick/channel")
	for _, cmd := range []string{"ZNC help", "BOUNCER LISTNETWORKS", "DETACH #chat"} {
		c.send(cmd)
		c.expect("421 alice " + strings.Fields(cmd)[0] + " :Unknown command")
	}
	c.send("WHOIS *status")
	c.expect("401 alice *status :No such nick/channel")
	c.send("WHO *status")
	c.expect("315 alice *status")
	c.send("USERHOST *status")
	c.expect("302 alice :")
	c.send("CAP LS 302")
	if l := c.expect("CAP alice LS"); strings.Contains(l, "bouncer-networks") || !strings.Contains(l, "sasl=PLAIN,OAUTHBEARER,IRCV3BEARER") {
		t.Errorf("caps: %q", l)
	}
	c.send("PING :hello")
	c.expect("PONG irc.example.org :hello")
	c.expect("PING :irc.example.org") // the liveness check, once quiet (answered)

	// a CTCP VERSION while nobody's attached is answered as configured
	bob := r.put("bob", managed.UserInput{Subject: new("usr_bob"), Channels: &[]string{"#chat"}})

	// the network drops: the channel stays open with a neutral notice, and
	// a neutral one when it's back
	r.irc.Drop()
	c.expect(":irc.example.org NOTICE #chat :Connection to ExampleNet lost, reconnecting")
	c.expect(":irc.example.org NOTICE #chat :Reconnected to ExampleNet")

	// without quiet rejoin, the drop is a KICK with the neutral reason
	u, _ := r.acc.UserByID(context.Background(), bob.ID)
	u.QuietRejoin = false
	r.acc.UpdateUser(context.Background(), u)
	cb := r.bearer("IRCV3BEARER", r.issuer.Token("usr_bob", "bob"))
	cb.expect("903")
	cb.send("CAP END")
	cb.expect("JOIN #chat")
	r.irc.Drop()
	cb.expect(":irc.example.org KICK #chat bob :Connection to ExampleNet lost")
	c.expect("Reconnected to ExampleNet")

	// refusals: a bad token, a wrong password, a token for another
	// account, a disabled account, the issuer down, the lockout, the
	// client limit
	bad := r.bearer("OAUTHBEARER", r.issuer.Sign(map[string]any{"sub": "usr_alice", "aud": "quicvoice"}))
	bad.expect("904")
	bad.send("AUTHENTICATE FOO")
	bad.expect("908")
	bad.send("CAP END")
	bad.expect("464")

	other := r.dial()
	other.send("PASS bob:" + tok) // alice's token, naming bob
	other.send("NICK x")
	other.send("USER x 0 * :x")
	other.expect("464")

	// a token as the server password, alone: whoever it's for
	pc := r.dial()
	pc.send("PASS " + tok)
	pc.send("NICK alice")
	pc.send("USER alice 0 * :x")
	pc.expect(" 001 alice ")

	for i := 0; i < 5; i++ {
		w := r.dial()
		w.send("PASS alice:not-the-password")
		w.send("NICK alice")
		w.send("USER alice 0 * :x")
		w.expect("464")
	}
	locked := r.bearer("PLAIN", tok)
	locked.expect("Too many failed sign-ins")

	full := r.bearer("IRCV3BEARER", tok) // alice has 2 attached, plus this one: 3
	full.expect("903")
	full.send("CAP END")
	full.expect(" 001 ")
	over := r.bearer("IRCV3BEARER", tok)
	over.expect("903")
	over.send("CAP END")
	over.expect("Too many clients attached")

	r.put("bob", managed.UserInput{Disabled: new(true)})
	cb.expectClosed()
	off := r.bearer("OAUTHBEARER", r.issuer.Token("usr_bob", "bob"))
	off.expect("suspended")

	none := r.dial() // no password at all
	none.send("NICK x")
	none.send("USER x 0 * :x")
	none.expect("464 x :Password required")

	r.issuer.Close()
	r.prov.Mode.SetTokens(managed.NewVerifier(r.issuer.URL, managedtest.Audience, ""))
	down := r.bearer("OAUTHBEARER", tok)
	down.expect("Service unavailable, try again later")

	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.seen) < 50 {
		t.Fatalf("only %d lines seen", len(r.seen))
	}
	if testing.Verbose() {
		t.Log(strings.Join(r.seen, "\n"))
	}
	for _, l := range r.seen {
		if strings.Contains(strings.ToLower(l), "evrd") || strings.Contains(l, "*status!") {
			t.Errorf("a client was sent %q", l)
		}
	}
	_ = alice
}

// Away while nobody's attached, and CTCP VERSION, say what the config
// says on the network.
func TestManagedNetworkSideTexts(t *testing.T) {
	r := newManagedRig(t)
	r.put("alice", managed.UserInput{Subject: new("usr_alice")})
	u, _ := r.acc.UserByUsername(context.Background(), "alice")
	u.AutoAway, u.AutoAwayDelay = true, new(0)
	r.acc.UpdateUser(context.Background(), u)
	c := r.bearer("OAUTHBEARER", r.issuer.Token("usr_alice", "alice"))
	c.expect("903")
	c.send("CAP END")
	c.expect(" 001 ")
	c.send("QUIT")
	eventually(t, "the managed away message", func() bool { return r.irc.Has("AWAY :Away") })
	r.irc.Send(":bob!b@h PRIVMSG alice :\x01VERSION\x01")
	eventually(t, "the managed VERSION", func() bool { return r.irc.Has("NOTICE bob :\x01VERSION Boson\x01") })
	for _, l := range r.irc.Lines() {
		if strings.Contains(strings.ToLower(l), "evrd") && !strings.Contains(l, "PING") {
			t.Errorf("the network was sent %q", l)
		}
	}
}

func TestSASLCredentials(t *testing.T) {
	for _, tc := range []struct {
		mech, raw, login, secret string
		ok                       bool
	}{
		{"PLAIN", "\x00alice\x00pw", "alice", "pw", true},
		{"PLAIN", "\x00\x00pw", "", "", false},
		{"OAUTHBEARER", "n,a=alice,\x01auth=Bearer tok\x01\x01", "alice", "tok", true},
		{"OAUTHBEARER", "y,,\x01host=x\x01auth=bearer tok\x01\x01", "", "tok", true},
		{"OAUTHBEARER", "n,,\x01auth=Basic tok\x01\x01", "", "", false},
		{"OAUTHBEARER", "x,,\x01auth=Bearer tok\x01\x01", "", "", false},
		{"OAUTHBEARER", "nonsense", "", "", false},
		{"IRCV3BEARER", "alice\x00jwt\x00tok", "alice", "tok", true},
		{"IRCV3BEARER", "\x00jwt\x00", "", "", false},
		{"SCRAM-SHA-256", "x", "", "", false},
	} {
		login, secret, ok := saslCredentials(tc.mech, tc.raw)
		if login != tc.login || secret != tc.secret || ok != tc.ok {
			t.Errorf("%s %q: %q %q %v", tc.mech, tc.raw, login, secret, ok)
		}
	}
}

// Outside managed mode, nothing changes: no bearer mechanisms, the stock
// names.
func TestNotManagedKeepsTheStockNames(t *testing.T) {
	r := newRig(t)
	ic := r.attach("alice/one:alice-password")
	ic.expect(":evrd 001 ")
	se := &session{srv: r.srv}
	if se.serverName() != "evrd" || se.pingToken() != "evrd" || se.managedRestartText() != "" || se.movingMsg() != movingMsg {
		t.Error("stock names changed")
	}
	if m := se.saslMechs(); len(m) != 1 || m[0] != "PLAIN" {
		t.Errorf("mechanisms %v", m)
	}
	if _, ok := se.managedText("kick.disconnected"); ok {
		t.Error("a managed text outside managed mode")
	}
	if _, _, ok, _ := se.tokenSignIn("alice", "eyJhbGciOiJFZERTQSJ9.e30.c2lnbmF0dXJlc2lnbmF0dXJl"); ok {
		t.Error("checked a token outside managed mode")
	}
}

// Texts set to "-" aren't sent; an account without the network can't
// attach; one network under another name is it.
func TestManagedEdges(t *testing.T) {
	r := newManagedRig(t)
	cfg := r.mode.Config()
	cfg.Texts = map[string]string{"disconnected": "-", "reconnected": "-"}
	c := config.Default()
	c.Managed = cfg
	m := managed.New(c)
	m.SetTokens(r.mode.Tokens())
	r.srv.SetManaged(m)

	se := &session{srv: r.srv, unick: "al"}
	if got := se.managedISupport([]string{"NETWORK=Other"}); len(got) != 1 {
		t.Errorf("NETWORK added twice: %v", got)
	}
	if got := se.managedISupport(nil); len(got) != 1 || got[0] != "NETWORK=ExampleNet" {
		t.Errorf("NETWORK: %v", got)
	}
	if se.managedRestartText() != "Server restarting, please reconnect" || se.movingMsg() != "Service unavailable, try again later" {
		t.Error("managed texts")
	}
	if _, ok := se.managedText("help.intro"); ok {
		t.Error("a *status text in managed mode")
	}

	// no network at all: refused, neutrally
	dave, _ := r.acc.Create(accounts.CreateInput{Username: "dave", Subject: "usr_dave"})
	nn := r.bearer("OAUTHBEARER", r.issuer.Token("usr_dave", "dave"))
	nn.expect("903")
	nn.send("CAP END")
	nn.expect("464 alice :Service unavailable")

	// a lone network under another name is the one
	n, _ := r.st.CreateNetwork(context.Background(), store.Network{UserID: dave.ID, Name: "Renamed", Host: cfg.Host, Port: cfg.Port, Nick: "dave", Channels: []string{"#chat"}})
	ok := r.bearer("OAUTHBEARER", r.issuer.Token("usr_dave", "dave"))
	ok.expect("903")
	ok.send("CAP END")
	ok.expect("JOIN #chat")
	before := r.irc.Connections()
	r.irc.Drop()
	eventually(t, "back", func() bool { return r.irc.Connections() > before && r.b.Status(n.ID) == "connected" })
	ok.send("PING :done")
	ok.expect("PONG irc.example.org :done") // and nothing said about it
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, l := range r.seen {
		if strings.Contains(l, "NOTICE #chat") {
			t.Errorf("a notice set to -: %q", l)
		}
	}
}

// A managed account another node runs (or none yet) waits a moment for
// this one to take it, then is told to try again, neutrally.
func TestManagedWaitsForTheAccount(t *testing.T) {
	owned := false
	var mu sync.Mutex
	r := newManagedRig(t, func(b *bouncer.Bouncer) {
		b.SetOwnership(func(int64) bool { mu.Lock(); defer mu.Unlock(); return owned })
	})
	r.put("alice", managed.UserInput{Subject: new("usr_alice")})
	defer func(d time.Duration) { ownedWait = d }(ownedWait)
	ownedWait = 300 * time.Millisecond
	c := r.bearer("OAUTHBEARER", r.issuer.Token("usr_alice", "alice"))
	c.expect("903")
	c.send("CAP END")
	c.expect("464 alice :Service unavailable, try again later")

	ownedWait = 5 * time.Second
	go func() { time.Sleep(200 * time.Millisecond); mu.Lock(); owned = true; mu.Unlock() }()
	c = r.bearer("OAUTHBEARER", r.issuer.Token("usr_alice", "alice"))
	c.expect("903")
	c.send("CAP END")
	c.expect(" 001 alice ")
}
