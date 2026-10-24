package netfile

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func sample() store.Network {
	return store.Network{
		ID: 7, Name: "libera", Enabled: true, Nick: "bob", AltNick: "bob_", Username: "bob", Realname: "Bob",
		Servers:  []store.Server{{Host: "irc.libera.chat", Port: 6697, TLS: true, Password: "serverpass"}, {Host: "plain.libera.chat", Port: 6667}},
		Password: "saslpass", SASLUser: "bobby", AuthMethod: store.AuthSASL, AuthCommand: "PRIVMSG x :login %password%",
		JoinAfterAuth: true, TrustPKI: true, Fingerprints: []string{"ab12"}, FloodRate: 1.5, FloodBurst: 4, JoinDelay: 3,
		Channels:        []string{"#evrd", "#go"},
		ChannelSettings: map[string]store.ChannelSettings{"#evrd": {Key: "chankey", Detached: true}},
		Modules:         map[string]store.ModuleConfig{"perform": {Enabled: true, Settings: map[string]string{"commands": "OPER bob operpass"}}},
	}
}

func lines(n int) *int { return &n }

func TestRoundTrip(t *testing.T) {
	t.Cleanup(func() { store.SetSecretKeys(nil) })
	store.SetSecretKeys(key(1))
	n := sample()
	yes := true
	pb := map[int64]map[string]store.BufferPlayback{7: {
		"":       {Lines: lines(200)},
		"#evrd":  {Lines: lines(50), AutoClear: &yes},
		"alice":  {Lines: lines(0)},
		"#other": {},
	}}
	out, sealed, err := Export([]store.Network{n}, pb)
	if err != nil || !sealed {
		t.Fatalf("export: sealed %v, %v", sealed, err)
	}
	for _, secret := range []string{"serverpass", "saslpass", "chankey", "operpass", "%password%"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("export has %q in plaintext:\n%s", secret, out)
		}
	}
	if !Looks(out) {
		t.Fatalf("an export doesn't look like a networks file:\n%s", out)
	}
	f, err := Parse(out)
	if err != nil || len(f.Networks) != 1 {
		t.Fatalf("parse: %v %+v", err, f)
	}
	x := f.Networks[0]
	got := store.Network{ID: 7}
	settings := x.Apply(&got)
	got.Enabled = n.Enabled
	if !reflect.DeepEqual(got, n) {
		t.Errorf("round trip:\n got %+v\nwant %+v", got, n)
	}
	want := Settings{"": {Lines: lines(200)}, "#evrd": {Lines: lines(50), AutoClear: &yes}, "alice": {Lines: lines(0)}}
	if !reflect.DeepEqual(settings, want) {
		t.Errorf("playback %+v, want %+v", settings, want)
	}
	if len(x.Notes()) != 0 {
		t.Errorf("notes %v", x.Notes())
	}

	// on a server with another key the secrets can't be read: they're
	// noted, and an existing network's own are kept
	store.SetSecretKeys(key(2))
	f, _ = Parse(out)
	x = f.Networks[0]
	other := store.Network{}
	x.Apply(&other)
	if other.Password != "" || other.Servers[0].Password != "" || other.Channel("#evrd").Key != "" || other.AuthCommand != "" || len(other.Modules["perform"].Settings) != 0 {
		t.Errorf("secrets from another key: %+v", other)
	}
	keys := map[string]bool{}
	for _, no := range x.Notes() {
		keys[no.Key] = true
	}
	for _, k := range []string{"irc.netfile.serverPassword", "irc.netfile.loginPassword", "irc.netfile.loginCommand", "irc.netfile.channelKey", "irc.netfile.moduleSettings"} {
		if !keys[k] {
			t.Errorf("no %s note: %v", k, x.Notes())
		}
	}
	f, _ = Parse(out)
	x = f.Networks[0]
	mine := sample()
	mine.Password, mine.Servers[0].Password = "mine", "myserver"
	x.Apply(&mine)
	if mine.Password != "mine" || mine.Servers[0].Password != "myserver" || mine.Channel("#evrd").Key != "chankey" {
		t.Errorf("unreadable secrets replaced the network's own: %+v", mine)
	}
}

func TestNoKeyLeavesSecretsOut(t *testing.T) {
	store.SetSecretKeys(nil)
	out, sealed, err := Export([]store.Network{sample()}, nil)
	if err != nil || sealed {
		t.Fatalf("sealed %v, %v", sealed, err)
	}
	for _, secret := range []string{"serverpass", "saslpass", "chankey", "operpass", "password:", "key:"} {
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("export without a key has %q:\n%s", secret, out)
		}
	}
}

func TestHandWritten(t *testing.T) {
	store.SetSecretKeys(nil)
	f, err := Parse([]byte(`
networks:
  - name: oftc
    servers:
      - host: irc.oftc.net
      - host: plain.oftc.net
        tls: false
    login: {method: nickserv, password: hunter2}
    channels:
      - name: "#debian"
        key: k
    colour: blue
`))
	if err != nil {
		t.Fatal(err)
	}
	x := f.Networks[0]
	n := store.Network{Nick: "default", FloodRate: 2}
	x.Apply(&n)
	if n.Nick != "default" || n.FloodRate != 2 {
		t.Errorf("fields left out changed: %+v", n)
	}
	if s := n.Servers; len(s) != 2 || s[0] != (store.Server{Host: "irc.oftc.net", Port: 6697, TLS: true}) || s[1] != (store.Server{Host: "plain.oftc.net", Port: 6667}) {
		t.Errorf("servers %+v", s)
	}
	if n.Password != "hunter2" || n.AuthMethod != "nickserv" || n.Channel("#debian").Key != "k" {
		t.Errorf("plaintext secrets: %+v", n)
	}
	if no := x.Notes(); len(no) != 1 || no[0].Key != "irc.netfile.unknownField" || no[0].Args[1] != "colour" {
		t.Errorf("notes %v", no)
	}
}

func TestLooksAndParse(t *testing.T) {
	if Looks([]byte("<User bob>\n")) || !Looks([]byte("# hi\n\nevrd_networks: 1\n")) {
		t.Error("Looks")
	}
	if _, err := Parse([]byte("evrd_networks: 9\nnetworks: []\n")); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("newer format: %v", err)
	}
	if _, err := Parse([]byte("evrd_networks: 1\n")); err != ErrNotNetworks {
		t.Errorf("no networks: %v", err)
	}
}

func TestMerge(t *testing.T) {
	store.SetSecretKeys(nil)
	f, err := Parse([]byte(`
networks:
  - name: libera
    nick: filenick
    realname: From the file
    servers:
      - {host: irc.libera.chat, port: 6697, password: fileserverpw}
      - {host: new.libera.chat}
    login: {method: sasl, account: x, password: filepw}
    certificates: {fingerprints: [aa, bb]}
    playback: {lines: 10}
    channels:
      - {name: "#Evrd", key: filekey, playback: {lines: 5}}
      - {name: "#new", key: newkey, playback: {lines: 7}}
    modules: {keepnick: {enabled: true}, perform: {enabled: false}}
`))
	if err != nil {
		t.Fatal(err)
	}
	n := sample()
	before := sample()
	x := f.Networks[0]
	pb := x.Merge(&n, map[string]store.BufferPlayback{"": {Lines: lines(200)}})
	if n.Nick != before.Nick || n.Realname != before.Realname || n.Password != "saslpass" || n.SASLUser != "bobby" {
		t.Errorf("merge changed what the network had: %+v", n)
	}
	if n.Servers[0].Password != "serverpass" || len(n.Servers) != 3 || n.Servers[2].Host != "new.libera.chat" {
		t.Errorf("servers %+v", n.Servers)
	}
	if strings.Join(n.Channels, " ") != "#evrd #go #new" || n.Channel("#evrd").Key != "chankey" || n.Channel("#new").Key != "newkey" {
		t.Errorf("channels %v %+v", n.Channels, n.ChannelSettings)
	}
	if strings.Join(n.Fingerprints, " ") != "ab12 aa bb" || !n.Modules["keepnick"].Enabled || !n.Modules["perform"].Enabled {
		t.Errorf("fingerprints, modules: %v %+v", n.Fingerprints, n.Modules)
	}
	if !reflect.DeepEqual(pb, Settings{"#evrd": {Lines: lines(5)}, "#new": {Lines: lines(7)}}) { // "" has its own already
		t.Errorf("playback %+v", pb)
	}
	empty := store.Network{Name: "libera"}
	f, _ = Parse([]byte("networks:\n  - name: libera\n    nick: filenick\n    login: {method: sasl, account: x, password: filepw}\n"))
	x = f.Networks[0]
	x.Merge(&empty, nil)
	if empty.Nick != "filenick" || empty.AuthMethod != "sasl" || empty.Password != "filepw" {
		t.Errorf("empty fields not filled: %+v", empty)
	}
}
