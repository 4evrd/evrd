package modules

import (
	"slices"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

// cryptHost is the fake host with what crypt uses besides: saving its
// settings and telling the user things.
type cryptHost struct {
	*fakeHost
	saved map[string]string
	said  []string
}

func (h *cryptHost) SaveSetting(module, key, value string) {
	if module != "crypt" {
		panic(module)
	}
	h.saved[key] = value
}
func (h *cryptHost) Notify(from string, t messages.Text) {
	h.said = append(h.said, from+" "+t.English())
}

func startCrypt(t *testing.T, settings map[string]string) (*cryptHost, *crypt) {
	t.Helper()
	h := &cryptHost{fakeHost: newFake(), saved: map[string]string{}}
	ms := Start(h, map[string]Config{"crypt": {Enabled: true, Settings: settings}}, nil)
	return h, ms[0].(*crypt)
}

func TestCryptEncryptsWhatTheUserSends(t *testing.T) {
	h, c := startCrypt(t, map[string]string{"keys": "#Secret testkey\nbob cbc:bobkey"})
	if out := c.RewriteClientLine("PRIVMSG #secret :Hello, world!"); !slices.Equal(out, []string{"PRIVMSG #secret :+OK 9vjCO08wIY41GHMAo.pCIjl/"}) {
		t.Fatalf("ECB: %q", out)
	}
	for _, l := range []string{"PRIVMSG #open :hi", "PRIVMSG #secret :\x01VERSION\x01", "PRIVMSG #secret :+OK already", "JOIN #secret", "TOPIC #secret"} {
		if out := c.RewriteClientLine(l); !slices.Equal(out, []string{l}) {
			t.Errorf("%q became %q", l, out)
		}
	}
	// an action, a notice and a topic, with the tags kept; CBC to bob
	out := c.RewriteClientLine("@+draft/reply=x PRIVMSG bob :\x01ACTION waves\x01")
	if len(out) != 1 || !strings.HasPrefix(out[0], "@+draft/reply=x PRIVMSG bob :\x01ACTION +OK *") || !strings.HasSuffix(out[0], "\x01") {
		t.Fatalf("action %q", out)
	}
	enc := strings.TrimSuffix(strings.TrimPrefix(irc.Parse(out[0]).Params[1], "\x01ACTION "), "\x01")
	if plain, cbc, ok := fishDecrypt("bobkey", enc); !ok || !cbc || plain != "waves" {
		t.Fatalf("action decrypts to %q %v", plain, ok)
	}
	for _, l := range []string{"NOTICE #secret :psst", "TOPIC #secret :the plan"} {
		out := c.RewriteClientLine(l)
		m := irc.Parse(out[0])
		if plain, _, _ := fishDecrypt("testkey", m.Params[1]); len(out) != 1 || plain != irc.Parse(l).Params[1] {
			t.Errorf("%q: %q", l, out)
		}
	}
	// long text is split into lines that fit, at spaces, and comes back whole
	long := strings.Repeat("all work and no play makes jack a dull boy ", 40)
	for _, target := range []string{"#secret", "bob"} {
		key := map[string]string{"#secret": "testkey", "bob": "bobkey"}[target]
		out := c.RewriteClientLine("PRIVMSG " + target + " :" + long)
		if len(out) < 3 {
			t.Fatalf("%s: %d lines", target, len(out))
		}
		var back string
		for _, l := range out {
			if len(l)+relayRoom > 510 {
				t.Errorf("%s: a line of %d bytes", target, len(l))
			}
			p, _, _ := fishDecrypt(key, irc.Parse(l).Params[1])
			back += p
		}
		if back != long {
			t.Errorf("%s: came back as %q", target, back)
		}
	}
	if len(h.sent) != 0 {
		t.Errorf("sent %q", h.sent)
	}
}

func TestCryptDecryptsWhatArrives(t *testing.T) {
	h, c := startCrypt(t, map[string]string{"keys": "#secret testkey\nbob testkey", "mark": "[e]"})
	rewrite := func(l string) (string, bool) {
		m := irc.Parse(l)
		keep := c.RewriteLine(&m)
		return m.String(), keep
	}
	for in, want := range map[string]string{
		":bob!b@h PRIVMSG #secret :+OK 9vjCO08wIY41GHMAo.pCIjl/":                ":bob!b@h PRIVMSG #secret :[e] Hello, world!",
		":bob!b@h PRIVMSG alice :mcps 9vjCO08wIY41GHMAo.pCIjl/":                 ":bob!b@h PRIVMSG alice :[e] Hello, world!",
		":bob!b@h NOTICE alice :+OK 9vjCO08wIY41GHMAo.pCIjl/":                   ":bob!b@h NOTICE alice :[e] Hello, world!",
		":bob!b@h PRIVMSG #secret :\x01ACTION +OK 9vjCO08wIY41GHMAo.pCIjl/\x01": ":bob!b@h PRIVMSG #secret :\x01ACTION [e] Hello, world!\x01",
		":bob!b@h TOPIC #secret :+OK 9vjCO08wIY41GHMAo.pCIjl/":                  ":bob!b@h TOPIC #secret :Hello, world!",
		":irc.example 332 alice #secret :+OK 9vjCO08wIY41GHMAo.pCIjl/":          ":irc.example 332 alice #secret :Hello, world!",
		":carol!c@h PRIVMSG #secret :+OK *AAECAwQFBgcMwrYaMR5JLUL1S0XmY6y+":     ":carol!c@h PRIVMSG #secret :[e] Hello, world!",
		":alice!a@h PRIVMSG bob :+OK 9vjCO08wIY41GHMAo.pCIjl/":                  ":alice!a@h PRIVMSG bob :[e] Hello, world!", // our own, echoed
		":bob!b@h PRIVMSG #secret :plain text":                                  ":bob!b@h PRIVMSG #secret :plain text",
		":bob!b@h PRIVMSG #secret :+OK not!fish":                                ":bob!b@h PRIVMSG #secret :+OK not!fish",
		":carol!c@h PRIVMSG alice :+OK 9vjCO08wIY41GHMAo.pCIjl/":                ":carol!c@h PRIVMSG alice :+OK 9vjCO08wIY41GHMAo.pCIjl/", // no key for carol
		":bob!b@h PRIVMSG #open :+OK 9vjCO08wIY41GHMAo.pCIjl/":                  ":bob!b@h PRIVMSG #open :+OK 9vjCO08wIY41GHMAo.pCIjl/",
	} {
		if got, keep := rewrite(in); !keep || got != want {
			t.Errorf("%q:\n got %q\nwant %q", in, got, want)
		}
	}
	// what decrypts to line breaks can't break the line
	enc, _ := fishEncrypt("testkey", false, "one\r\nQUIT :two")
	if got, _ := rewrite(":bob!b@h PRIVMSG #secret :" + enc); got != ":bob!b@h PRIVMSG #secret :[e] one QUIT :two" {
		t.Errorf("got %q", got)
	}
	if len(h.sent)+len(h.said) != 0 {
		t.Errorf("sent %q, said %q", h.sent, h.said)
	}
}

func TestCryptCommands(t *testing.T) {
	h, c := startCrypt(t, nil)
	cmd := func(text string) []string {
		h.said = nil
		if !c.ClientLine(irc.Message{Command: "PRIVMSG", Params: []string{"*crypt", text}}.String()) {
			t.Fatalf("%q not taken", text)
		}
		return h.said
	}
	if c.ClientLine("PRIVMSG bob :setkey x y") || c.ClientLine("JOIN #a") {
		t.Fatal("took a line that isn't for *crypt")
	}
	if said := cmd("listkeys"); len(said) != 1 || !strings.Contains(said[0], "No keys yet") {
		t.Fatalf("said %q", said)
	}
	if said := cmd("setkey #Secret cbc:correct horse battery"); len(said) != 1 || said[0] != "*crypt Key for #Secret set (CBC): what you send there is encrypted." {
		t.Fatalf("said %q", said)
	}
	cmd("setkey bob testkey")
	if h.saved["keys"] != "#Secret cbc:correct horse battery\nbob testkey" {
		t.Fatalf("saved %q", h.saved["keys"])
	}
	if said := cmd("LISTKEYS"); !slices.Equal(said, []string{"*crypt #Secret: CBC correct horse battery", "*crypt bob: ECB testkey"}) {
		t.Fatalf("said %q", said)
	}
	if k, ok := c.key("#secret"); !ok || k.key != "correct horse battery" || !k.cbc {
		t.Fatalf("key %+v", k)
	}
	if said := cmd("setkey bob " + strings.Repeat("k", 57)); !strings.Contains(said[0], "1 to 56 bytes") {
		t.Fatalf("said %q", said)
	}
	cmd("delkey BOB")
	if _, ok := c.key("bob"); ok || h.saved["keys"] != "#Secret cbc:correct horse battery" {
		t.Fatalf("saved %q", h.saved["keys"])
	}
	if said := cmd("delkey bob"); !strings.Contains(said[0], "no key for bob") {
		t.Fatalf("said %q", said)
	}
	for _, c := range []string{"help", "", "setkey #x", "keyx #chan"} {
		if said := cmd(c); len(said) == 0 || !strings.Contains(strings.Join(said, "\n"), "<") {
			t.Errorf("%q: %q", c, said)
		}
	}
	if said := cmd("frobnicate"); !strings.Contains(said[0], "frobnicate") {
		t.Fatalf("said %q", said)
	}
	// the keys it saved are the settings it starts with next time
	_, again := startCrypt(t, map[string]string{"keys": h.saved["keys"]})
	if k, ok := again.key("#SECRET"); !ok || k.key != "correct horse battery" || !k.cbc {
		t.Fatalf("restarted with %+v", again.keys)
	}
}

func TestCryptKeyExchange(t *testing.T) {
	// alice (this evrd) starts one with bob, another evrd running crypt
	ha, alice := startCrypt(t, nil)
	hb, bob := startCrypt(t, nil)
	hb.nick = "bob"
	alice.ClientLine("PRIVMSG *crypt :keyx bob")
	if len(ha.sent) != 1 || !strings.HasPrefix(ha.sent[0], "NOTICE bob :DH1080_INIT ") || !strings.HasSuffix(ha.sent[0], " CBC") {
		t.Fatalf("sent %q", ha.sent)
	}
	// bob's evrd gets it from the server, answers, and keeps it from bob's clients
	m := irc.Parse(":alice!a@h " + ha.sent[0])
	if bob.RewriteLine(&m) {
		t.Fatal("the INIT was shown")
	}
	if len(hb.sent) != 1 || !strings.HasPrefix(hb.sent[0], "NOTICE alice :DH1080_FINISH ") || !strings.HasSuffix(hb.sent[0], " CBC") {
		t.Fatalf("bob sent %q", hb.sent)
	}
	m = irc.Parse(":bob!b@h " + hb.sent[0])
	if alice.RewriteLine(&m) {
		t.Fatal("the FINISH was shown")
	}
	ka, _ := alice.key("bob")
	kb, _ := bob.key("alice")
	if ka.key == "" || ka.key != kb.key || !ka.cbc || !kb.cbc || len(ka.key) != 43 {
		t.Fatalf("keys %+v %+v", ka, kb)
	}
	if !strings.Contains(ha.saved["keys"], "bob cbc:"+ka.key) || !strings.Contains(hb.saved["keys"], "alice cbc:"+kb.key) {
		t.Fatalf("saved %q %q", ha.saved, hb.saved)
	}
	if len(ha.said) != 2 || !strings.Contains(ha.said[1], "done") || len(hb.said) != 1 || !strings.Contains(hb.said[0], "asked for a key exchange") || len(ha.logs) != 1 {
		t.Fatalf("said %q / %q, logged %q", ha.said, hb.said, ha.logs)
	}
	// and now they talk
	out := alice.RewriteClientLine("PRIVMSG bob :hi bob")
	m = irc.Parse(":alice!a@h " + out[0])
	bob.RewriteLine(&m)
	if m.Params[1] != "🔒 hi bob" {
		t.Fatalf("bob got %q", m.Params[1])
	}

	// an old client: DH1080_INIT without CBC is answered without, for ECB
	hc, carol := startCrypt(t, nil)
	p, _ := newDH()
	m = irc.Parse(":dave!d@h NOTICE alice :DH1080_INIT " + p.Public())
	carol.RewriteLine(&m)
	if len(hc.sent) != 1 || strings.HasSuffix(hc.sent[0], "CBC") {
		t.Fatalf("sent %q", hc.sent)
	}
	answer := strings.TrimPrefix(strings.Fields(hc.sent[0])[3], ":")
	if k, _ := carol.key("dave"); k.cbc || k.key == "" || k.key != must(p.Key(answer)) {
		t.Fatalf("dave's key %+v", k)
	}
	// a FINISH nobody asked for, a bad key, and one in a channel
	for _, l := range []string{":eve!e@h NOTICE alice :DH1080_FINISH AAAA", ":eve!e@h NOTICE #chan :DH1080_INIT " + p.Public()} {
		m = irc.Parse(l)
		if !carol.RewriteLine(&m) {
			t.Errorf("%q dropped", l)
		}
	}
	hc.said = nil
	m = irc.Parse(":eve!e@h NOTICE alice :DH1080_INIT AA")
	if carol.RewriteLine(&m) || len(hc.said) != 1 || !strings.Contains(hc.said[0], "failed") {
		t.Fatalf("bad key: said %q", hc.said)
	}
}

func TestSplitText(t *testing.T) {
	for _, s := range []string{"", "short", strings.Repeat("é", 100), strings.Repeat("word ", 50), strings.Repeat("x", 300)} {
		parts := splitText(s, 40)
		if strings.Join(parts, "") != s {
			t.Errorf("%q: %q", s, parts)
		}
		for _, p := range parts {
			if len(p) > 40 || !utf8Valid(p) {
				t.Errorf("%q: piece %q", s, p)
			}
		}
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "?") == s }

func must(s string, err error) string {
	if err != nil {
		panic(err)
	}
	return s
}
