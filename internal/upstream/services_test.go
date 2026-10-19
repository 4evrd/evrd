package upstream

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/store"
)

// services is a scripted server with services on it: CAP offers ls
// (SASL answered with saslResult), and what's sent to a services nick goes
// to answer (nil: no answer). taken nicks get 433 while registering.
type services struct {
	ls         string
	saslResult string
	taken      map[string]bool
	answer     func(p *peer, to, text string)
}

func (sv services) reply(p *peer, l string) {
	m := irc.Parse(l)
	switch {
	case strings.HasPrefix(l, "CAP LS"):
		p.send(":srv CAP * LS :%s", sv.ls)
	case strings.HasPrefix(l, "CAP REQ"):
		p.send(":srv CAP * ACK :%s", strings.TrimPrefix(l, "CAP REQ :"))
	case l == "AUTHENTICATE PLAIN":
		p.send("AUTHENTICATE +")
	case strings.HasPrefix(l, "AUTHENTICATE "):
		p.send(":srv %s %s :SASL result", sv.saslResult, p.nick)
	case m.Command == "NICK" && sv.taken[strings.ToLower(m.Params[0])] && !p.registered():
		p.send(":srv 433 * %s :Nickname is already in use", m.Params[0])
		p.nick = "*" // still without one
	case m.Command == "NICK" && p.registered():
		p.send(":%s!u@h NICK %s", p.me(), m.Params[0])
		p.mu.Lock()
		p.welcomed = m.Params[0]
		p.mu.Unlock()
	case m.Command == "NICK" && p.ended, l == "CAP END" && p.nick != "*":
		p.send(":srv 001 %s :Welcome", p.nick)
		p.mu.Lock()
		p.welcomed = p.nick
		p.mu.Unlock()
	case l == "CAP END":
		p.ended = true // welcome once there's a free nick
	case m.Command == "PRIVMSG" && len(m.Params) == 2 && !isChannel(m.Params[0]) && sv.answer != nil:
		sv.answer(p, m.Params[0], m.Params[1])
	case m.Command == "NS" && sv.answer != nil:
		sv.answer(p, "NS", strings.Join(m.Params, " "))
	}
}

// nickserv answers IDENTIFY with ok or no, as Atheme does.
func nickserv(pw string) func(p *peer, to, text string) {
	return func(p *peer, to, text string) {
		f := strings.Fields(text)
		if len(f) == 0 || !strings.EqualFold(f[0], "IDENTIFY") {
			return
		}
		if len(f) == 2 { // IDENTIFY password: the nick in use
			f = []string{f[0], p.me(), f[1]}
		}
		if f[len(f)-1] == pw {
			p.send(":NickServ!NickServ@services. NOTICE %s :You are now identified for \x02%s\x02.", p.me(), f[1])
		} else {
			p.send(":NickServ!NickServ@services. NOTICE %s :Invalid password for \x02%s\x02.", p.me(), f[1])
		}
	}
}

func startServices(t *testing.T, sv services) *scripted {
	return startScripted(t, sv.reply)
}

// connect runs a network on s, with its log recorded.
func connect(t *testing.T, s *scripted, n store.Network) (*Conn, *logRec) {
	t.Helper()
	rec := &logRec{}
	if n.Nick == "" {
		n.Nick = "alice"
	}
	c := New(s.network(n), quiet, Options{OnLog: rec.add})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	return c, rec
}

func shorten(t *testing.T) {
	pw, gw, au := promptWait, ghostWait, authUnit
	promptWait, ghostWait, authUnit = 100*time.Millisecond, 50*time.Millisecond, 20*time.Millisecond
	t.Cleanup(func() { promptWait, ghostWait, authUnit = pw, gw, au })
}

func noPassword(t *testing.T, rec *logRec, pw string) {
	t.Helper()
	for _, e := range rec.all() {
		if strings.Contains(e.Text, pw) {
			t.Fatalf("the password is in the log: %q", e.Text)
		}
	}
}

func TestNickServMethodSkipsSASL(t *testing.T) {
	s := startServices(t, services{ls: "sasl", answer: nickserv("hunter22")})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, SASLUser: "acct", IdentifyArgs: store.IdentifyAccount,
		Password: "hunter22", Channels: []string{"#home"}})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
	got := s.last().got()
	if index(got, "CAP REQ") >= 0 || index(got, "PASS") >= 0 {
		t.Fatalf("SASL or PASS with the nickserv method: %q", got)
	}
	if i, j := index(got, "PRIVMSG NickServ :IDENTIFY acct hunter22"), index(got, "JOIN #home"); i < 0 || j < i {
		t.Fatalf("want IDENTIFY then JOIN: %q", got)
	}
	if !rec.has("evrd", "Identifying to NickServ as acct", false) || !rec.has("evrd", "Identified to services: You are now identified for acct.", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	noPassword(t, rec, "hunter22")
}

func TestIdentifyLine(t *testing.T) {
	for _, tc := range []struct {
		n       store.Network
		current string
		want    string
	}{
		{store.Network{Nick: "al", Password: "pw"}, "al", "PRIVMSG NickServ :IDENTIFY pw"},
		{store.Network{Nick: "al", Password: "pw"}, "al_", "PRIVMSG NickServ :IDENTIFY al pw"}, // on the alt nick
		{store.Network{Nick: "al", SASLUser: "acct", Password: "pw"}, "al", "PRIVMSG NickServ :IDENTIFY acct pw"},
		{store.Network{Nick: "al", SASLUser: "acct", Password: "pw", IdentifyArgs: store.IdentifyPassword}, "al", "PRIVMSG NickServ :IDENTIFY pw"},
		{store.Network{Nick: "al", Password: "pw", IdentifyArgs: store.IdentifyAccount}, "al", "PRIVMSG NickServ :IDENTIFY al pw"},
		{store.Network{Nick: "al", Password: "pw", ServicesNick: "NickServ@services.dal.net"}, "al", "PRIVMSG NickServ@services.dal.net :IDENTIFY pw"},
		{store.Network{Nick: "al", Password: "pw", ServicesNick: "/NS", IdentifyArgs: store.IdentifyAccount}, "al", "NS IDENTIFY al pw"},
		{store.Network{Nick: "al", Password: "pw", ServicesNick: "/nickserv"}, "al", "NICKSERV IDENTIFY pw"},
	} {
		if got := identifyLine(tc.n, tc.current); got != tc.want {
			t.Errorf("%+v on %s: %q, want %q", tc.n, tc.current, got, tc.want)
		}
	}
}

func TestDALnetStyleTarget(t *testing.T) {
	s := startServices(t, services{answer: func(p *peer, to, text string) {
		if to == "NickServ@services.dal.net" && text == "IDENTIFY s3cret-pw" {
			p.send(":NickServ!service@dal.net NOTICE %s :Password accepted for \x02%s\x02.", p.me(), p.me())
		}
	}})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, ServicesNick: "NickServ@services.dal.net",
		IdentifyArgs: store.IdentifyPassword, Password: "s3cret-pw"})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
	if !rec.has("evrd", "Identifying to NickServ@services.dal.net", false) {
		t.Fatalf("log: %+v", rec.all())
	}
}

func TestNSCommandTarget(t *testing.T) {
	s := startServices(t, services{answer: func(p *peer, to, text string) {
		if to == "NS" && text == "IDENTIFY pw-pw-pw" {
			p.send(":NickServ!s@services NOTICE %s :Password accepted - you are now recognized.", p.me())
		}
	}})
	c, _ := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, ServicesNick: "/NS", Password: "pw-pw-pw"})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
}

func TestUndernetXCommand(t *testing.T) {
	for _, tc := range []struct{ name, cmd string }{
		{"raw", "PRIVMSG x@channels.undernet.org :LOGIN %account% %password%"},
		{"/msg", "/msg x@channels.undernet.org LOGIN %account% %password%"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := startServices(t, services{answer: func(p *peer, to, text string) {
				if to != "x@channels.undernet.org" {
					return
				}
				if text == "LOGIN acct xpass-1" {
					p.send(":X!cservice@undernet.org NOTICE %s :AUTHENTICATION SUCCESSFUL as acct", p.me())
				} else {
					p.send(":X!cservice@undernet.org NOTICE %s :AUTHENTICATION FAILED as acct", p.me())
				}
			}})
			c, rec := connect(t, s, store.Network{AuthMethod: store.AuthCommand, AuthCommand: tc.cmd, SASLUser: "acct", Password: "xpass-1"})
			eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
			if !rec.has("evrd", "Sent the login command to x@channels.undernet.org", false) || !rec.has("evrd", "AUTHENTICATION SUCCESSFUL", false) {
				t.Fatalf("log: %+v", rec.all())
			}
			noPassword(t, rec, "xpass-1")
		})
	}
	s := startServices(t, services{answer: func(p *peer, to, text string) {
		p.send(":X!cservice@undernet.org NOTICE %s :AUTHENTICATION FAILED as acct", p.me())
	}})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthCommand, AuthCommand: "PRIVMSG X@channels.undernet.org :LOGIN %account% %password%", SASLUser: "acct", Password: "wrong-pw"})
	eventually(t, "refused", func() bool { return c.Health().Auth == AuthFailed })
	if !rec.has("evrd", "Services refused the login: AUTHENTICATION FAILED as acct", true) || !rec.has("evrd", "password was refused", false) {
		t.Fatalf("log: %+v", rec.all())
	}
}

func TestACommandWithoutATarget(t *testing.T) {
	s := startServices(t, services{})
	_, rec := connect(t, s, store.Network{AuthMethod: store.AuthCommand, AuthCommand: "AUTH %nick% %password%", Password: "pw-123"})
	eventually(t, "sent", func() bool { return s.has("AUTH alice pw-123") })
	if !rec.has("evrd", "Sent the login command", false) || rec.has("evrd", "Sent the login command to", false) {
		t.Fatalf("log: %+v", rec.all())
	}
}

func TestAWrongPasswordIsExplained(t *testing.T) {
	s := startServices(t, services{answer: nickserv("right-pw")})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "wrong-pw", Channels: []string{"#home"}, JoinAfterAuth: true})
	eventually(t, "refused", func() bool { return c.Health().Auth == AuthFailed })
	eventually(t, "joined anyway", func() bool { return s.has("JOIN #home") })
	if !rec.has("evrd", "Services refused the login: Invalid password for alice.", true) || !rec.has("evrd", "The password was refused", false) {
		t.Fatalf("log: %+v", rec.all())
	}
	noPassword(t, rec, "wrong-pw")
}

func TestServicesAnswersAreUnderstood(t *testing.T) {
	for text, ok := range map[string]bool{
		"You are now identified for \x02acct\x02.":            true, // Atheme
		"Password accepted - you are now recognized.":         true, // Anope
		"You're now logged in as acct":                        true, // ergo
		"AUTHENTICATION SUCCESSFUL as acct":                   true, // Undernet X
		"You are now logged in as acct.":                      true, // QuakeNet Q
		"You're already logged into an account":               true,
		"Invalid password for \x02acct\x02.":                  false,
		"Password incorrect.":                                 false,
		"Authentication failed: Invalid account credentials":  false,
		"\x02acct\x02 is not a registered nickname.":          false,
		"Nick \x02acct\x02 isn't registered.":                 false,
		"AUTHENTICATION FAILED as acct":                       false,
		"\x034This nickname has been suspended\x03":           false,
		"Account acct has too many failed login attempts":     false,
		"Authentication failed: Account does not exist":       false,
		"Password authentication failed for this nick, sorry": false,
	} {
		clean := formatting.ReplaceAllString(text, "")
		fail, good := servicesFail.MatchString(clean), servicesOK.MatchString(clean)
		if fail == ok || (ok && !good) {
			t.Errorf("%q: fail=%v ok=%v, want ok=%v", text, fail, good, ok)
		}
	}
	for _, p := range []string{
		"This nickname is registered. Please choose a different nickname, or identify via \x02/msg NickServ identify <password>\x02.",
		"This nickname is registered and protected. If it is your nick, type \x02/msg NickServ IDENTIFY \x1fpassword\x1f\x02.",
		"This nick is owned by someone else. Please choose another.",
	} {
		if !servicesPrompt.MatchString(formatting.ReplaceAllString(p, "")) {
			t.Errorf("not a prompt: %q", p)
		}
	}
}

func TestRPLLoggedInConfirms(t *testing.T) {
	s := startServices(t, services{answer: func(p *peer, to, text string) {
		p.send(":srv 900 %s %s!u@h acct :You are now logged in as acct", p.me(), p.me())
	}})
	c, _ := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw"})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
}

func TestJoinAfterAuthWaitsForServices(t *testing.T) {
	s := startServices(t, services{answer: func(*peer, string, string) {}}) // answers later
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", JoinAfterAuth: true,
		Channels: []string{"#regonly"}})
	eventually(t, "identify", func() bool { return s.has("PRIVMSG NickServ :IDENTIFY pw-pw") })
	time.Sleep(100 * time.Millisecond)
	if s.count("JOIN") != 0 {
		t.Fatalf("joined before services answered: %q", s.last().got())
	}
	if c.Health().Auth != AuthPending || !rec.has("evrd", "Joining channels once services confirm", false) {
		t.Fatalf("auth %q, log %+v", c.Health().Auth, rec.all())
	}
	s.send(":NickServ!NickServ@services. NOTICE alice :You are now identified for \x02alice\x02.")
	eventually(t, "joined", func() bool { return s.has("JOIN #regonly") })
}

func TestJoinAfterAuthGivesUp(t *testing.T) {
	shorten(t)
	s := startServices(t, services{})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", JoinAfterAuth: true,
		AuthTimeout: 5, Channels: []string{"#regonly"}})
	eventually(t, "joined after the timeout", func() bool { return s.has("JOIN #regonly") })
	if c.Health().Auth != AuthUnconfirmed || !rec.has("evrd", "Services didn't answer the login within", true) ||
		!rec.has("evrd", "NickServ@services.dal.net", false) {
		t.Fatalf("auth %q, log %+v", c.Health().Auth, rec.all())
	}
	// a late answer changes nothing
	s.send(":NickServ!NickServ@services. NOTICE alice :You are now identified for \x02alice\x02.")
	time.Sleep(50 * time.Millisecond)
	if c.Health().Auth != AuthUnconfirmed || s.count("JOIN") != 1 {
		t.Fatalf("auth %q, sent %q", c.Health().Auth, s.last().got())
	}
}

func TestJoinDelayStillApplies(t *testing.T) {
	s := startServices(t, services{answer: nickserv("pw-pw")})
	connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", JoinAfterAuth: true, JoinDelay: 1,
		Channels: []string{"#later"}})
	eventually(t, "identify", func() bool { return s.has("PRIVMSG NickServ :IDENTIFY pw-pw") })
	time.Sleep(300 * time.Millisecond)
	if s.count("JOIN") != 0 {
		t.Fatal("joined before the join delay")
	}
	eventually(t, "joined", func() bool { return s.has("JOIN #later") })
}

func TestIdentifyOnPrompt(t *testing.T) {
	s := startServices(t, services{answer: nickserv("pw-pw")})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", IdentifyOn: store.IdentifyOnPrompt})
	time.Sleep(100 * time.Millisecond)
	if s.count("PRIVMSG NickServ") != 0 || !rec.has("evrd", "Waiting for NickServ to ask", false) {
		t.Fatalf("identified before the prompt: %q", s.last().got())
	}
	s.send(":NickServ!NickServ@services. NOTICE alice :This nickname is registered. Please choose a different nickname, or identify via \x02/msg NickServ identify <password>\x02.")
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
	if s.count("PRIVMSG NickServ :IDENTIFY") != 1 {
		t.Fatalf("%q", s.last().got())
	}
}

func TestIdentifyOnPromptThatNeverComes(t *testing.T) {
	shorten(t)
	s := startServices(t, services{answer: nickserv("pw-pw")})
	c, _ := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", IdentifyOn: store.IdentifyOnPrompt})
	eventually(t, "identified anyway", func() bool { return c.Health().Auth == AuthIdentified })
}

func TestGhostFreesTheNick(t *testing.T) {
	for _, cmd := range []string{"ghost", "recover"} {
		t.Run(cmd, func(t *testing.T) {
			shorten(t)
			s := startServices(t, services{taken: map[string]bool{"alice": true}, answer: func(p *peer, to, text string) {
				switch {
				case strings.HasPrefix(text, "IDENTIFY"):
					nickserv("pw-pw")(p, to, text)
				case strings.HasPrefix(text, strings.ToUpper(cmd)+" alice"):
					p.send(":alice!old@h QUIT :%s command used by alt", cmd)
				}
			}})
			c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", AltNick: "alt", GhostCmd: cmd})
			eventually(t, "back on alice", func() bool { return c.Nick() == "alice" })
			got := s.last().got()
			if i, g := index(got, "PRIVMSG NickServ :IDENTIFY alice pw-pw"), index(got, "PRIVMSG NickServ :"+strings.ToUpper(cmd)+" alice pw-pw"); i < 0 || g < i {
				t.Fatalf("want IDENTIFY (naming the account) then %s: %q", cmd, got)
			}
			if exactly(got, "NICK alice") != 2 || !rec.has("evrd", "alice is taken: asking NickServ to "+cmd+" it", false) {
				t.Fatalf("sent %q, log %+v", got, rec.all())
			}
		})
	}
}

func TestRegainSwitchesTheNickItself(t *testing.T) {
	shorten(t)
	s := startServices(t, services{taken: map[string]bool{"alice": true}, answer: func(p *peer, to, text string) {
		if strings.HasPrefix(text, "REGAIN alice") {
			p.send(":%s!u@h NICK alice", p.me())
		}
	}})
	c, _ := connect(t, s, store.Network{Password: "pw-pw", GhostCmd: "regain"})
	s.send(":srv 900 alice_ alice_!u@h alice :You are now logged in as alice")
	eventually(t, "regained", func() bool { return c.Nick() == "alice" })
	time.Sleep(150 * time.Millisecond)
	if exactly(s.last().got(), "NICK alice") != 1 { // only the first try, while registering
		t.Fatalf("switched again: %q", s.last().got())
	}
}

func TestGhostAfterSASL(t *testing.T) {
	shorten(t)
	s := startServices(t, services{ls: "sasl", saslResult: "903", taken: map[string]bool{"alice": true}})
	c, rec := connect(t, s, store.Network{Password: "pw-pw", GhostCmd: "ghost"})
	eventually(t, "ghost", func() bool { return s.has("PRIVMSG NickServ :GHOST alice pw-pw") })
	eventually(t, "nick back", func() bool { return c.Nick() == "alice" }) // nobody said: tried after ghostWait
	if !rec.has("evrd", "Logged in with SASL as alice", false) || c.Health().Auth != AuthIdentified {
		t.Fatalf("log %+v", rec.all())
	}
}

func TestIdentifyByPasswordWaitsForTheNick(t *testing.T) {
	shorten(t)
	s := startServices(t, services{taken: map[string]bool{"alice": true}, answer: func(p *peer, to, text string) {
		switch {
		case text == "GHOST alice pw-pw":
			p.send(":NickServ!NickServ@services. NOTICE %s :\x02alice\x02 has been ghosted.", p.me())
		case strings.HasPrefix(text, "IDENTIFY"):
			nickserv("pw-pw")(p, to, text)
		}
	}})
	c, _ := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Password: "pw-pw", GhostCmd: "ghost", IdentifyArgs: store.IdentifyPassword})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
	got := s.last().got()
	g, n, i := index(got, "PRIVMSG NickServ :GHOST"), slices.Index(got, "NICK alice"), index(got, "PRIVMSG NickServ :IDENTIFY pw-pw")
	if g < 0 || i < g || c.Nick() != "alice" {
		t.Fatalf("want GHOST, NICK, then IDENTIFY: %q (%d %d)", got, n, i)
	}
}

func TestSASLFallsBackToNickServWhenUnsupported(t *testing.T) {
	s := startServices(t, services{ls: "multi-prefix", answer: nickserv("pw-pw")})
	c, rec := connect(t, s, store.Network{Password: "pw-pw"})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
	if !s.has("PASS pw-pw") {
		t.Fatalf("auto still tries PASS: %q", s.last().got())
	}
	noPassword(t, rec, "pw-pw")
}

func TestSASLRefusedThenNickServ(t *testing.T) {
	s := startServices(t, services{ls: "sasl", saslResult: "904", answer: nickserv("pw-pw")})
	c, rec := connect(t, s, store.Network{Password: "pw-pw"})
	eventually(t, "identified", func() bool { return c.Health().Auth == AuthIdentified })
	if !rec.has("evrd", "SASL login failed: SASL result", true) || !rec.has("evrd", "Will identify to NickServ once connected instead", false) {
		t.Fatalf("log %+v", rec.all())
	}
}

func TestSASLOnly(t *testing.T) {
	t.Run("refused", func(t *testing.T) {
		s := startServices(t, services{ls: "sasl", saslResult: "904", answer: nickserv("pw-pw")})
		c, rec := connect(t, s, store.Network{AuthMethod: store.AuthSASL, Password: "pw-pw"})
		time.Sleep(50 * time.Millisecond)
		if c.Health().Auth != AuthFailed || s.count("PRIVMSG NickServ") != 0 {
			t.Fatalf("auth %q, sent %q", c.Health().Auth, s.last().got())
		}
		if !rec.has("evrd", "SASL login failed", true) {
			t.Fatalf("log %+v", rec.all())
		}
	})
	t.Run("not offered", func(t *testing.T) {
		s := startServices(t, services{ls: "multi-prefix", answer: nickserv("pw-pw")})
		c, rec := connect(t, s, store.Network{AuthMethod: store.AuthSASL, Password: "pw-pw"})
		time.Sleep(50 * time.Millisecond)
		if c.Health().Auth != AuthFailed || s.count("PRIVMSG NickServ") != 0 || s.count("PASS") != 0 {
			t.Fatalf("auth %q, sent %q", c.Health().Auth, s.last().got())
		}
		if !rec.has("evrd", "SASL login failed: the server doesn't offer SASL", true) || !rec.has("evrd", "This network has no SASL", false) {
			t.Fatalf("log %+v", rec.all())
		}
	})
	t.Run("logged in", func(t *testing.T) {
		s := startServices(t, services{ls: "sasl", saslResult: "903"})
		c, rec := connect(t, s, store.Network{AuthMethod: store.AuthSASL, SASLUser: "acct", Password: "pw-pw"})
		if c.Health().Auth != AuthIdentified || !rec.has("evrd", "Logging in with SASL as acct", false) || !rec.has("evrd", "Logged in with SASL as acct", false) {
			t.Fatalf("auth %q, log %+v", c.Health().Auth, rec.all())
		}
	})
}

func TestNoLogin(t *testing.T) {
	s := startServices(t, services{ls: "sasl", answer: nickserv("pw-pw")})
	c, _ := connect(t, s, store.Network{AuthMethod: store.AuthNone, Password: "pw-pw", Channels: []string{"#a"}})
	eventually(t, "joined", func() bool { return s.has("JOIN #a") })
	if got := s.last().got(); index(got, "CAP REQ") >= 0 || index(got, "PASS") >= 0 || index(got, "PRIVMSG") >= 0 || c.Health().Auth != "" {
		t.Fatalf("logged in with method none: %q", got)
	}
}

func TestNickServWithoutAPassword(t *testing.T) {
	s := startServices(t, services{})
	_, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, Channels: []string{"#a"}})
	eventually(t, "joined", func() bool { return s.has("JOIN #a") })
	if !rec.has("evrd", "No password set: not identifying to NickServ", true) {
		t.Fatalf("log %+v", rec.all())
	}
}

func TestNoSuchServicesNick(t *testing.T) {
	s := startServices(t, services{answer: func(p *peer, to, text string) {
		p.send(":srv 401 %s %s :No such nick/channel", p.me(), to)
	}})
	c, rec := connect(t, s, store.Network{AuthMethod: store.AuthNickServ, ServicesNick: "NickServ@services.example", Password: "pw-pw"})
	eventually(t, "failed", func() bool { return c.Health().Auth == AuthFailed })
	if !rec.has("evrd", "no services nick NickServ@services.example", true) || !rec.has("evrd", "Check the login method and services nick", false) {
		t.Fatalf("log %+v", rec.all())
	}
	// someone else's 401 isn't about the login
	c2 := &Conn{net: store.Network{Nick: "x"}}
	c2.auth.waiting = true
	c2.servicesMissing(irc.Parse(":srv 401 x bob :No such nick"))
	c2.servicesMissing(irc.Parse(":srv 401 x"))
	if !c2.auth.waiting {
		t.Fatal("an unrelated 401 ended the login")
	}
}

func TestTheLogNeverShowsThePassword(t *testing.T) {
	s := startServices(t, services{ls: "sasl", saslResult: "904", answer: func(p *peer, to, text string) {
		// services that repeat what they got
		p.send(":NickServ!NickServ@services. NOTICE %s :Invalid password %s for you", p.me(), strings.Fields(text)[1])
	}})
	_, rec := connect(t, s, store.Network{Password: "correct-horse"})
	eventually(t, "refused", func() bool { return rec.has("evrd", "Services refused the login", true) })
	noPassword(t, rec, "correct-horse")
	if !rec.has("evrd", "Invalid password •••••• for you", true) {
		t.Fatalf("log %+v", rec.all())
	}
}

func TestRegisteredOnlyChannelIsExplained(t *testing.T) {
	s := startServices(t, services{})
	_, rec := connect(t, s, store.Network{Channels: []string{"#reg"}})
	eventually(t, "joined", func() bool { return s.has("JOIN #reg") })
	s.send(":srv 477 alice #reg :You must be registered to join that channel")
	eventually(t, "explained", func() bool { return rec.has("evrd", "only lets in people logged in to an account", false) })
	if !rec.has("server", "477 alice #reg", true) {
		t.Fatalf("log %+v", rec.all())
	}
}

func TestFromServices(t *testing.T) {
	c := &Conn{net: store.Network{ServicesNick: "NickServ@services.dal.net", AuthMethod: store.AuthCommand, AuthCommand: "/msg X@channels.undernet.org LOGIN a b"}}
	for prefix, want := range map[string]bool{
		"NickServ!s@services": true, "nickserv": true, "X!cservice@undernet.org": true,
		"irc.server.net": false, "": false, "bob!b@h": false,
	} {
		if got := c.fromServices(prefix); got != want {
			t.Errorf("%q: %v", prefix, got)
		}
	}
	c.net = store.Network{ServicesNick: "/NS"}
	if !c.fromServices("NickServ!a@b") {
		t.Error("/NS answers come from NickServ")
	}
}

// exactly counts the lines equal to l.
func exactly(lines []string, l string) int {
	n := 0
	for _, x := range lines {
		if x == l {
			n++
		}
	}
	return n
}
