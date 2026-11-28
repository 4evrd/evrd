package upstream

import (
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/testirc"
)

// crypt on a connection: what the user sends goes out encrypted but is
// saved as they wrote it; what arrives encrypted is saved and passed on
// decrypted; a key exchange is answered, saved, and kept from clients.
// (The texts are fish.py's known answers, as in internal/modules.)
func TestCryptOnTheConnection(t *testing.T) {
	var log testirc.Log
	host, port, _ := net.SplitHostPort(testirc.StartLogged(t, &log))
	p, _ := strconv.Atoi(port)
	var mu sync.Mutex
	var saved []history.Message
	var kept []string
	c := New(store.Network{ID: 45, Nick: "dora", Host: host, Port: p, Modules: map[string]store.ModuleConfig{
		"crypt": {Enabled: true, Settings: map[string]string{"keys": "#secret testkey"}},
	}}, quiet, Options{
		Persist: func(m history.Message) { mu.Lock(); saved = append(saved, m); mu.Unlock() },
		SaveModuleSetting: func(module, key, value string) {
			mu.Lock()
			kept = append(kept, module+" "+key+" "+value)
			mu.Unlock()
		},
	})
	raw, unsub := c.SubscribeRaw()
	defer unsub()
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected })
	savedText := func(sender string) string {
		mu.Lock()
		defer mu.Unlock()
		for i := len(saved) - 1; i >= 0; i-- {
			if saved[i].Sender == sender {
				return saved[i].Text
			}
		}
		return ""
	}

	c.SendFrom("", "PRIVMSG", "#secret", "Hello, world!")
	eventually(t, "sent encrypted", func() bool { return log.Has("PRIVMSG #secret :+OK 9vjCO08wIY41GHMAo.pCIjl/") })
	if got := savedText("dora"); got != "Hello, world!" {
		t.Fatalf("saved %q", got)
	}
	c.SendRaw("TOPIC #secret :the plan")
	eventually(t, "topic sent encrypted", func() bool { return log.Has("TOPIC #secret :+OK ") })
	if log.Has("the plan") {
		t.Fatal("the topic went out in the clear")
	}

	log.Send(":bob!b@h PRIVMSG #secret :+OK 9vjCO08wIY41GHMAo.pCIjl/")
	eventually(t, "saved decrypted", func() bool { return savedText("bob") == "🔒 Hello, world!" })
	sawLine := false
	for !sawLine {
		l := <-raw
		sawLine = strings.Contains(l.Text, "PRIVMSG #secret :🔒 Hello, world!")
		if strings.Contains(l.Text, "+OK") {
			t.Fatalf("clients got %q", l.Text)
		}
	}

	// bob's client starts a key exchange (an old one: ECB)
	pub := "XQFtlfE/DxTcRlgblm6YLX6yos047BodXJtt9llKZaGk4xHky7VyHyT6lS5UKkz2CPVj1ufZDXCEVi+99aTqkJMzTx4XuRbMjFhlQhq4z4FQ5GoO34ukxMb21vOvATqSa6cqK9XrHuvHL9GUZqvWuFNoHzT9co9LjgCK5ALPsG79cgTCQ9miA"
	log.Send(":bob!b@h NOTICE dora :DH1080_INIT " + pub)
	eventually(t, "answered", func() bool { return log.Has("NOTICE bob :DH1080_FINISH ") })
	eventually(t, "the key saved", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(kept) == 1 && strings.HasPrefix(kept[0], "crypt keys #secret testkey\nbob ")
	})
	for note := false; !note; {
		l := <-raw
		if strings.Contains(l.Text, "DH1080") {
			t.Fatalf("clients saw %q", l.Text)
		}
		note = l.Note.From == "*crypt" && strings.Contains(l.Note.Text.English(), "bob asked for a key exchange")
	}
	// it's the key now, without starting the modules afresh
	c.SendFrom("", "PRIVMSG", "bob", "hi")
	eventually(t, "sent to bob encrypted", func() bool { return log.Has("PRIVMSG bob :+OK ") })
	if !c.ClientLine("PRIVMSG *crypt :listkeys") || c.ClientLine("PRIVMSG bob :listkeys") {
		t.Fatal("*crypt's commands")
	}
}
