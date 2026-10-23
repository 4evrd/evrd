package evrctl

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

// result is one run of evrctl.
type result struct {
	code           int
	stdout, stderr string
}

// evrctl runs the command line args (without the program name) with stdin.
func evrctl(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Execute(append([]string{"evrctl"}, args...), strings.NewReader(stdin), &out, &errOut)
	return result{code, out.String(), errOut.String()}
}

// want checks a run's exit status and that its stdout and stderr contain
// the given text ("" for nothing in particular).
func (r result) want(t *testing.T, code int, stdout, stderr string) {
	t.Helper()
	if r.code != code || !strings.Contains(r.stdout, stdout) || !strings.Contains(r.stderr, stderr) {
		t.Fatalf("exit %d, stdout %q, stderr %q\nwant exit %d, stdout ~ %q, stderr ~ %q", r.code, r.stdout, r.stderr, code, stdout, stderr)
	}
}

// fails checks a run failed with exactly "evrctl: msg" and printed nothing.
func (r result) fails(t *testing.T, msg string) {
	t.Helper()
	if r.code != 1 || r.stdout != "" || r.stderr != "evrctl: "+msg+"\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q\nwant exit 1, stderr %q", r.code, r.stdout, r.stderr, "evrctl: "+msg+"\n")
	}
}

// noEnv keeps the developer's environment out of the config.
func noEnv(t *testing.T) {
	for _, v := range []string{"EVRD_CONFIG", "EVRD_DB", "EVRD_HISTORY", "EVRD_SECRET_KEY", "EVRD_SECRET_KEY_FILE", "EVRD_UPSTREAM_BIND", "EVRD_UPSTREAM_PREFIX6"} {
		t.Setenv(v, "")
	}
}

// writeConfig writes an evrd config file into dir.
func writeConfig(t *testing.T, dir string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "evrd.conf")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestUsageAndUnknownCommands(t *testing.T) {
	noEnv(t)
	r := evrctl(t, "")
	r.want(t, 2, "", "usage: evrctl [-config file | -db dsn] <command> [args]")
	if r.stdout != "" {
		t.Fatalf("stdout %q", r.stdout)
	}
	r = evrctl(t, "", "frob")
	r.want(t, 2, "", "evrctl: unknown command \"frob\"\nusage: evrctl")
	evrctl(t, "", "-h").want(t, 0, "", "usage: evrctl")
	evrctl(t, "", "-nope").want(t, 2, "", "unknown flag: --nope\nusage: evrctl")
	evrctl(t, "", "-db", "sqlite:"+filepath.Join(t.TempDir(), "x.db"), "user").want(t, 2, "", "usage: evrctl")
}

func TestVersion(t *testing.T) {
	r := evrctl(t, "", "version")
	if r.code != 0 || r.stdout != "evrctl "+version.Version+"\n" || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
}

func TestGenkey(t *testing.T) {
	r1, r2 := evrctl(t, "", "genkey"), evrctl(t, "", "genkey")
	if r1.code != 0 || r1.stderr != "" {
		t.Fatalf("%+v", r1)
	}
	k, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(r1.stdout, "\n"))
	if err != nil || len(k) != 32 {
		t.Fatalf("key %q: %d bytes, %v", r1.stdout, len(k), err)
	}
	if r1.stdout == r2.stdout {
		t.Fatal("the same key twice")
	}
	// It's a key evrd accepts.
	cfg := config.Default()
	cfg.Secrets.Key = strings.TrimSpace(r1.stdout)
	if cur, _, err := cfg.SecretKeys(); err != nil || len(cur) != 32 {
		t.Fatalf("secret_key: %v", err)
	}
}

func TestUsers(t *testing.T) {
	noEnv(t)
	dir := t.TempDir()
	dsn := "sqlite:" + filepath.Join(dir, "evrd.db")
	key := strings.TrimSpace(evrctl(t, "", "genkey").stdout)
	conf := writeConfig(t, dir, "db "+dsn, "secret_key "+key)
	user := func(stdin string, args ...string) result {
		return evrctl(t, stdin, append([]string{"-config", conf, "user"}, args...)...)
	}
	open := func() (store.Store, *accounts.Service) {
		st, err := store.Open(dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		return st, accounts.New(st, config.Default().Limits)
	}

	// add
	r := user("", "add", "-admin", "-email", "root@example.org", "root", "root-password")
	if r.code != 0 || r.stdout != "created admin \"root\" (id 1)\n" || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	r = user("bob-password\n", "add", "Bob", "-") // the password from stdin; names are lowercased
	if r.code != 0 || r.stdout != "created user \"bob\" (id 2)\n" {
		t.Fatalf("%+v", r)
	}
	user("", "add", "carol").want(t, 0, "created user \"carol\" (id 3)", "")
	user("", "add", "root", "another-password").fails(t, "create user: that username is taken")
	user("", "add", "x!").fails(t, "create user: usernames are 2–32 characters: a–z, 0–9, - and _, starting with a letter")
	user("", "add", "dave", "short").fails(t, "create user: use at least 10 characters")
	user("", "add").fails(t, "usage: evrctl user add [-admin] [-email e] <name> [password]")
	user("", "add", "a", "b", "c").fails(t, "usage: evrctl user add [-admin] [-email e] <name> [password]")
	user("", "add", "dave", "-").fails(t, "read password: EOF")
	user("", "add", "-nope", "dave").want(t, 2, "", "unknown flag: --nope")
	user("", "add", "-h").want(t, 0, "", "-admin")

	st, acc := open()
	u, err := st.UserByUsername(t.Context(), "root")
	if err != nil || u.Role != store.RoleAdmin || u.Email != "root@example.org" {
		t.Fatalf("root: %+v %v", u, err)
	}
	if _, _, err := acc.IRCLogin("bob", "bob-password"); err != nil {
		t.Fatalf("bob's password from stdin: %v", err)
	}
	if c, _ := st.UserByUsername(t.Context(), "carol"); c.PasswordHash != "" {
		t.Fatal("carol has a password")
	}

	// passwd
	if err := st.CreateSession(t.Context(), store.Session{ID: "s1", UserID: 2, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	r = user("", "passwd", "bob", "new-bob-password")
	if r.code != 0 || r.stdout != "password set for \"bob\" (web sessions signed out)\n" {
		t.Fatalf("%+v", r)
	}
	if _, _, err := acc.IRCLogin("bob", "new-bob-password"); err != nil {
		t.Fatalf("new password: %v", err)
	}
	if ss, _ := st.ListSessions(t.Context(), 2); len(ss) != 0 {
		t.Fatalf("sessions kept: %+v", ss)
	}
	user("", "passwd", "bob", "short").fails(t, "use at least 10 characters")
	user("", "passwd", "nobody", "a-long-password").fails(t, "no user \"nobody\"")
	user("", "passwd", "bob").fails(t, "usage: evrctl user passwd <name> <password>")

	// role
	user("", "role", "bob", "admin").want(t, 0, "\"bob\" is now admin\n", "")
	user("", "role", "root", "user").want(t, 0, "\"root\" is now user\n", "")
	user("", "role", "bob", "user").fails(t, "this is the last admin — make someone else an admin first")
	user("", "role", "bob", "owner").fails(t, "role must be user or admin")
	user("", "role", "nobody", "user").fails(t, "no user \"nobody\"")
	user("", "role", "bob").fails(t, "usage: evrctl user role <name> user|admin")
	if u, _ := st.UserByUsername(t.Context(), "root"); u.Role != store.RoleUser {
		t.Fatalf("root's role %q", u.Role)
	}

	// suspend, unsuspend
	st.CreateSession(t.Context(), store.Session{ID: "s2", UserID: 1, ExpiresAt: time.Now().Add(time.Hour)})
	user("", "suspend", "root", "testing").want(t, 0, "suspended \"root\"\n", "")
	if u, _ := st.UserByUsername(t.Context(), "root"); !u.Suspended || u.SuspendedReason != "testing" {
		t.Fatalf("root: suspended %v (%q)", u.Suspended, u.SuspendedReason)
	}
	if ss, _ := st.ListSessions(t.Context(), 1); len(ss) != 0 {
		t.Fatalf("sessions kept: %+v", ss)
	}
	user("", "suspend", "bob").fails(t, "this is the last admin — make someone else an admin first")
	user("", "suspend", "nobody").fails(t, "no user \"nobody\"")
	user("", "suspend").fails(t, "usage: evrctl user suspend <name> [reason]")
	user("", "suspend", "carol").want(t, 0, "suspended \"carol\"\n", "")

	// list: sign-in method, status and last sign-in
	sub := "sso-subject"
	sso, err := st.CreateUser(t.Context(), store.User{Username: "erin", Role: store.RoleUser, OIDCSubject: &sub})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 5, 4, 3, 2, 1, 0, time.Local)
	sso.LastLoginAt = &at
	if err := st.UpdateUser(t.Context(), sso); err != nil {
		t.Fatal(err)
	}
	r = user("", "list")
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	want := []string{
		`ID +USERNAME +ROLE +SIGN-IN +STATUS +LAST SIGN-IN`,
		`2 +bob +admin +password +active +never`, // by name
		`3 +carol +user +password +suspended +never`,
		`4 +erin +user +sso +active +2026-05-04 03:02:01`,
		`1 +root +user +password +suspended +never`,
	}
	lines := strings.Split(strings.TrimSuffix(r.stdout, "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("list:\n%s", r.stdout)
	}
	for i, re := range want {
		if !regexp.MustCompile("^" + re + "$").MatchString(strings.TrimRight(lines[i], " ")) {
			t.Fatalf("line %d %q, want %s\n%s", i, lines[i], re, r.stdout)
		}
	}

	user("", "unsuspend", "root").want(t, 0, "unsuspended \"root\"\n", "")
	if u, _ := st.UserByUsername(t.Context(), "root"); u.Suspended || u.SuspendedReason != "" {
		t.Fatalf("root still suspended: %+v", u)
	}
	user("", "unsuspend", "nobody").fails(t, "no user \"nobody\"")
	user("", "unsuspend").fails(t, "usage: evrctl user unsuspend <name>")

	// delete
	if _, err := st.CreateNetwork(t.Context(), store.Network{UserID: 3, Name: "libera"}); err != nil {
		t.Fatal(err)
	}
	user("", "delete", "carol").want(t, 0, "deleted \"carol\" (id 3)\n", "")
	if _, err := st.UserByUsername(t.Context(), "carol"); err != store.ErrNotFound {
		t.Fatalf("carol: %v", err)
	}
	if ns, _ := st.ListNetworks(t.Context(), 3); len(ns) != 0 {
		t.Fatalf("carol's networks kept: %+v", ns)
	}
	user("", "delete", "bob").fails(t, "this is the last admin — make someone else an admin first")
	user("", "delete", "nobody").fails(t, "no user \"nobody\"")
	user("", "delete").fails(t, "usage: evrctl user delete <name>")

	user("", "frob").fails(t, "unknown user subcommand \"frob\"")
}

func TestUserSetupErrors(t *testing.T) {
	noEnv(t)
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.conf")
	evrctl(t, "", "-config", missing, "user", "list").fails(t, "load config: open "+missing+": no such file or directory")
	t.Setenv("EVRD_CONFIG", missing) // the default for -config
	evrctl(t, "", "user", "list").fails(t, "load config: open "+missing+": no such file or directory")
	t.Setenv("EVRD_CONFIG", "")

	bad := writeConfig(t, dir, "db sqlite:"+filepath.Join(dir, "evrd.db"), "secret_key not-a-key")
	r := evrctl(t, "", "-config", bad, "user", "list")
	r.want(t, 1, "", "evrctl: secret key: ")

	r = evrctl(t, "", "-db", "sqlite:"+filepath.Join(dir, "no", "such", "dir", "evrd.db"), "user", "list")
	r.want(t, 1, "", "evrctl: open store: ")

	// -db overrides the config file's.
	other := "sqlite:" + filepath.Join(dir, "other.db")
	conf := writeConfig(t, dir, "db sqlite:"+filepath.Join(dir, "no", "such", "dir", "evrd.db"))
	evrctl(t, "", "-config", conf, "-db", other, "user", "add", "zed").want(t, 0, "created user \"zed\" (id 1)", "")
}

// historyWith makes a Pebble history holding msgs.
func historyWith(t *testing.T, dir string, msgs ...history.Message) string {
	t.Helper()
	dsn := "pebble:" + dir
	h, err := history.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if err := h.Add(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestHistoryCopy(t *testing.T) {
	noEnv(t)
	dir := t.TempDir()
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var msgs []history.Message
	for i := 0; i < 3; i++ {
		msgs = append(msgs, history.Message{NetworkID: 1, Target: "#go", Sender: "bob", Kind: "privmsg", Text: fmt.Sprint("go ", i), Time: t0.Add(time.Duration(i) * time.Second)})
	}
	msgs = append(msgs,
		history.Message{NetworkID: 1, Target: "alice", Sender: "alice", Kind: "privmsg", Text: "a query", Time: t0},
		history.Message{NetworkID: 2, Target: "#rust", Sender: "carol", Kind: "privmsg", Text: "rust", Time: t0},
	)
	from := historyWith(t, filepath.Join(dir, "from"), msgs...)
	to := "pebble:" + filepath.Join(dir, "to")

	r := evrctl(t, "", "history", "copy", from, to)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	if want := "network 1: 2 buffers\nnetwork 2: 1 buffers\ncopied 5 lines from 2 networks\n"; r.stdout != want {
		t.Fatalf("stdout %q, want %q", r.stdout, want)
	}
	h, err := history.Open(to)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	got, err := h.Recent(1, "#go", 10)
	if err != nil || len(got) != 3 || got[0].Text != "go 0" || got[2].Text != "go 2" || !got[2].Time.Equal(t0.Add(2*time.Second)) {
		t.Fatalf("#go: %+v %v", got, err)
	}
	if got, _ := h.Recent(2, "#rust", 10); len(got) != 1 || got[0].Sender != "carol" {
		t.Fatalf("#rust: %+v", got)
	}

	use := "usage: evrctl history copy <from> <to>"
	evrctl(t, "", "history").fails(t, use)
	evrctl(t, "", "history", "copy", from).fails(t, use)
	evrctl(t, "", "history", "move", from, to).fails(t, use)
	evrctl(t, "", "history", "copy", "nope://x", to).fails(t, `open nope://x: history: unknown store "nope://x" (want pebble:<dir> or scylla://hosts/keyspace)`)
	evrctl(t, "", "history", "copy", from, "nope://y").fails(t, `open nope://y: history: unknown store "nope://y" (want pebble:<dir> or scylla://hosts/keyspace)`)
}

func TestImportZNCCommand(t *testing.T) {
	noEnv(t)
	dir := t.TempDir()
	dsn := "sqlite:" + filepath.Join(dir, "evrd.db")
	hist := "pebble:" + filepath.Join(dir, "history")
	conf := writeConfig(t, dir, "db sqlite:"+filepath.Join(dir, "elsewhere.db"), "history "+hist)

	st, err := store.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := st.CreateUser(t.Context(), store.User{Username: "alice"})
	n, _ := st.CreateNetwork(t.Context(), store.Network{UserID: u.ID, Name: "libera"})
	st.Close()

	znc := filepath.Join(dir, "znc")
	logf := filepath.Join(znc, "moddata", "log", "libera", "#go", "2024-03-09.log")
	os.MkdirAll(filepath.Dir(logf), 0o755)
	os.WriteFile(logf, []byte("[10:00:00] <bob> hi\n[10:00:01] <bob> there\n"), 0o644)
	other := filepath.Join(znc, "moddata", "log", "oftc", "#x", "2024-03-09.log")
	os.MkdirAll(filepath.Dir(other), 0o755)
	os.WriteFile(other, []byte("[10:00:00] <bob> elsewhere\n"), 0o644)

	imp := func(args ...string) result {
		return evrctl(t, "", append([]string{"-config", conf, "-db", dsn, "history", "import-znc"}, args...)...)
	}

	// A dry run counts and writes nothing.
	r := imp("-dry-run", "-tz", "Europe/Berlin", "alice", znc)
	if r.code != 0 || r.stderr != "" {
		t.Fatalf("%+v", r)
	}
	for _, want := range []string{
		"libera                       2 lines\n",
		"oftc                 skipped: alice has no network by that name (1 days of logs)\n",
		"would import 2 lines into alice\n",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("stdout %q, want %q", r.stdout, want)
		}
	}
	if strings.Contains(r.stdout, "keeps all history") {
		t.Fatalf("dry run changed the backlog: %q", r.stdout)
	}

	r = imp("-tz", "Europe/Berlin", "alice", znc)
	if r.code != 0 || !strings.Contains(r.stdout, "imported 2 lines into alice\nalice now keeps all history") {
		t.Fatalf("%+v", r)
	}
	h, err := history.Open(hist)
	if err != nil {
		t.Fatal(err)
	}
	msgs, _ := h.Recent(n.ID, "#go", 10)
	h.Close()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	if len(msgs) != 2 || !msgs[0].Time.Equal(time.Date(2024, 3, 9, 10, 0, 0, 0, berlin)) {
		t.Fatalf("#go: %+v", msgs)
	}

	use := "usage: evrctl history import-znc [-dry-run] [-tz zone] <user> <znc-user-dir>"
	imp().fails(t, use)
	imp("alice").fails(t, use)
	imp("-nope", "alice", znc).want(t, 2, "", "unknown flag: --nope")
	imp("-tz", "Mars/Olympus", "alice", znc).fails(t, "timezone: unknown time zone Mars/Olympus")
	imp("nobody", znc).fails(t, `user "nobody": store: not found`)
	imp("alice", filepath.Join(dir, "empty")).fails(t, "no ZNC logs under "+filepath.Join(dir, "empty")+" (want …/moddata/log/<network>/<buffer>/<date>.log)")

	evrctl(t, "", "-config", filepath.Join(dir, "missing.conf"), "history", "import-znc", "alice", znc).want(t, 1, "", "evrctl: load config: open ")
	badKey := writeConfig(t, t.TempDir(), "secret_key nope")
	evrctl(t, "", "-config", badKey, "-db", dsn, "history", "import-znc", "alice", znc).want(t, 1, "", "evrctl: secret key: ")
	evrctl(t, "", "-db", "sqlite:"+filepath.Join(dir, "no", "dir", "x.db"), "history", "import-znc", "alice", znc).want(t, 1, "", "evrctl: open store: ")
	badHist := writeConfig(t, t.TempDir(), "history nope://x")
	evrctl(t, "", "-config", badHist, "-db", dsn, "history", "import-znc", "alice", znc).want(t, 1, "", "evrctl: open history (is evrd still running?): ")
}
