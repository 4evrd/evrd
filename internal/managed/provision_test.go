package managed

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/upstream"
)

// fakeBouncer records what provisioning asks of the connections.
type fakeBouncer struct {
	status    map[int64]upstream.Status
	clients   map[int64]int
	calls     []string
	reconnErr error
}

func (f *fakeBouncer) Reconnect(id int64) error {
	f.calls = append(f.calls, "reconnect")
	return f.reconnErr
}
func (f *fakeBouncer) CurrentNick(id int64) string { return "" }

func (f *fakeBouncer) Status(id int64) upstream.Status {
	if s, ok := f.status[id]; ok {
		return s
	}
	return upstream.StatusDisconnected
}
func (f *fakeBouncer) StopUser(int64)            { f.calls = append(f.calls, "stop") }
func (f *fakeBouncer) ResumeUser(int64)          { f.calls = append(f.calls, "resume") }
func (f *fakeBouncer) DeleteUser(id int64) error { f.calls = append(f.calls, "delete"); return nil }
func (f *fakeBouncer) EditChannels(id int64, before, after []string) {
	f.calls = append(f.calls, "channels")
}
func (f *fakeBouncer) ClientsByUser() map[int64]int { return f.clients }

func ptr[T any](v T) *T { return &v }

func testProvisioner(t *testing.T) (*Provisioner, *fakeBouncer, *accounts.Service) {
	t.Helper()
	acc, _ := testAccounts(t)
	fb := &fakeBouncer{status: map[int64]upstream.Status{}, clients: map[int64]int{}}
	return &Provisioner{Mode: New(testConfig(nil)), Accounts: acc, Bouncer: fb}, fb, acc
}

func TestPutCreatesTheAccountAndItsNetwork(t *testing.T) {
	p, fb, acc := testProvisioner(t)
	ctx := context.Background()
	u, err := p.Put(ctx, "Alice", UserInput{Subject: ptr("usr_1"), Email: ptr("a@example.org")})
	if err != nil {
		t.Fatal(err)
	}
	if !u.Created || u.Username != "alice" || u.Subject != "usr_1" || u.Nick != "alice" || u.Ident != "alice" ||
		u.Realname != "alice" || u.Status != "disconnected" || u.Auth.Method != "auto" || u.Email != "a@example.org" {
		t.Errorf("created %+v", u)
	}
	nets, _ := acc.ListNetworks(ctx, u.ID)
	if len(nets) != 1 || nets[0].Name != "ExampleNet" || nets[0].Host != "irc.example.org" || !nets[0].TLS || nets[0].Enabled {
		t.Fatalf("networks %+v", nets)
	}
	if len(fb.calls) != 0 {
		t.Errorf("connected a new account: %v", fb.calls)
	}

	// the same again changes nothing, and isn't "created"
	u, err = p.Put(ctx, "alice", UserInput{})
	if err != nil || u.Created {
		t.Errorf("again: %+v %v", u, err)
	}
	if nets, _ := acc.ListNetworks(ctx, u.ID); len(nets) != 1 {
		t.Errorf("a second network: %+v", nets)
	}
}

func TestPutChangesTheConnection(t *testing.T) {
	p, fb, acc := testProvisioner(t)
	ctx := context.Background()
	u, _ := p.Put(ctx, "alice", UserInput{})
	nets, _ := acc.ListNetworks(ctx, u.ID)
	id := nets[0].ID

	// not connected: saved for the next connection, nothing else
	u, err := p.Put(ctx, "alice", UserInput{Nick: ptr("Alice"), Realname: ptr(" Alice A "), Ident: ptr("al"),
		Auth: &AuthInput{Method: "sasl", Account: "alice", Password: "pw"}})
	if err != nil || u.Nick != "Alice" || u.Realname != "Alice A" || u.Ident != "al" ||
		u.Auth.Method != "sasl" || u.Auth.Account != "alice" || !u.Auth.HasPassword || len(fb.calls) != 0 {
		t.Fatalf("%+v %v %v", u, err, fb.calls)
	}
	// connected: a new identity reconnects, new channels are joined
	fb.status[id] = upstream.StatusConnected
	fb.clients[u.ID] = 2
	if u, _ = p.Put(ctx, "alice", UserInput{Nick: ptr("Alice2")}); !slices.Equal(fb.calls, []string{"reconnect"}) || u.Status != "connected" || u.Clients != 2 {
		t.Errorf("nick change: %v %+v", fb.calls, u)
	}
	fb.calls = nil
	if _, err := p.Put(ctx, "alice", UserInput{Channels: &[]string{"#a", "#b"}}); err != nil || !slices.Equal(fb.calls, []string{"channels"}) {
		t.Errorf("channels: %v %v", fb.calls, err)
	}
	fb.calls = nil
	// the password kept when not given; "none" clears it
	if u, _ = p.Put(ctx, "alice", UserInput{Auth: &AuthInput{Method: "nickserv", Account: "alice"}}); !u.Auth.HasPassword || u.Auth.Method != "nickserv" {
		t.Errorf("nickserv %+v", u)
	}
	if u, _ = p.Put(ctx, "alice", UserInput{Auth: &AuthInput{Method: "none"}}); u.Auth.HasPassword || u.Auth.Method != "none" {
		t.Errorf("none %+v", u)
	}
	fb.reconnErr = bouncer.ErrNotOwner // another node's user: fine
	if _, err := p.Put(ctx, "alice", UserInput{Nick: ptr("Alice3")}); err != nil {
		t.Error(err)
	}
	fb.reconnErr = errors.New("boom")
	if _, err := p.Put(ctx, "alice", UserInput{Nick: ptr("Alice4")}); err == nil {
		t.Error("a failed reconnect passed")
	}
}

func TestPutDisablesAndEnables(t *testing.T) {
	p, fb, _ := testProvisioner(t)
	ctx := context.Background()
	p.Put(ctx, "alice", UserInput{})
	if u, _ := p.Put(ctx, "alice", UserInput{Disabled: ptr(true)}); !u.Disabled || !slices.Equal(fb.calls, []string{"stop"}) {
		t.Errorf("disable: %+v %v", u, fb.calls)
	}
	if u, _ := p.Put(ctx, "alice", UserInput{Disabled: ptr(false)}); u.Disabled || !slices.Equal(fb.calls, []string{"stop", "resume"}) {
		t.Errorf("enable: %+v %v", u, fb.calls)
	}
	if u, _ := p.Put(ctx, "bob", UserInput{Disabled: ptr(true)}); !u.Disabled || !u.Created {
		t.Errorf("created disabled: %+v", u)
	}
}

func TestPutSubjects(t *testing.T) {
	p, _, _ := testProvisioner(t)
	ctx := context.Background()
	p.Put(ctx, "alice", UserInput{Subject: ptr("usr_1")})
	if _, err := p.Put(ctx, "bob", UserInput{Subject: ptr("usr_1")}); !errors.Is(err, ErrSubjectTaken) {
		t.Errorf("new account with a taken subject: %v", err)
	}
	p.Put(ctx, "bob", UserInput{})
	if _, err := p.Put(ctx, "bob", UserInput{Subject: ptr("usr_1")}); !errors.Is(err, ErrSubjectTaken) {
		t.Errorf("taken subject: %v", err)
	}
	if u, err := p.Put(ctx, "alice", UserInput{Subject: ptr("usr_1")}); err != nil || u.Subject != "usr_1" {
		t.Errorf("own subject: %+v %v", u, err)
	}
	if u, _ := p.Put(ctx, "alice", UserInput{Subject: ptr("")}); u.Subject != "" {
		t.Errorf("subject not cleared: %+v", u)
	}
}

func TestPutRefusesBadInput(t *testing.T) {
	p, _, _ := testProvisioner(t)
	ctx := context.Background()
	if _, err := p.Put(ctx, "no spaces please", UserInput{}); err == nil {
		t.Error("bad username")
	}
	for name, in := range map[string]UserInput{
		"nick":     {Nick: ptr("a b")},
		"empty":    {Nick: ptr("")},
		"ident":    {Ident: ptr("a:b")},
		"altnick":  {AltNick: ptr("x,y")},
		"realname": {Realname: ptr("a\r\nQUIT")},
		"method":   {Auth: &AuthInput{Method: "magic"}},
		"account":  {Auth: &AuthInput{Method: "sasl", Account: "a b"}},
		"channel":  {Channels: &[]string{"nochan"}},
		"channel2": {Channels: &[]string{"#a b"}},
	} {
		if _, err := p.Put(ctx, "alice", in); !errors.Is(err, ErrBadInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if u, err := p.Put(ctx, "alice", UserInput{AltNick: ptr("")}); err != nil || u.AltNick != "" {
		t.Errorf("clearing altNick: %v", err)
	}
}

func TestGetListDelete(t *testing.T) {
	p, fb, _ := testProvisioner(t)
	ctx := context.Background()
	if _, err := p.Get(ctx, "alice"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	p.Put(ctx, "alice", UserInput{Nick: ptr("Al")})
	p.Put(ctx, "bob", UserInput{})
	if u, err := p.Get(ctx, " Alice "); err != nil || u.Nick != "Al" {
		t.Errorf("get %+v %v", u, err)
	}
	us, err := p.List(ctx)
	if err != nil || len(us) != 2 {
		t.Fatalf("list %+v %v", us, err)
	}
	if err := p.Delete(ctx, "alice"); err != nil || !slices.Equal(fb.calls, []string{"delete"}) {
		t.Errorf("delete: %v %v", err, fb.calls)
	}
	if err := p.Delete(ctx, "nobody"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("delete nobody: %v", err)
	}
}

func TestReconcileFollowsTheConfig(t *testing.T) {
	p, _, acc := testProvisioner(t)
	ctx := context.Background()
	u, _ := p.Put(ctx, "alice", UserInput{Nick: ptr("Al")})
	acc.Create(accounts.CreateInput{Username: "admin", Role: store.RoleAdmin}) // no network: left so
	if n, err := p.Reconcile(ctx); err != nil || n != 0 {
		t.Errorf("nothing to do: %d %v", n, err)
	}
	c := testConfig(nil)
	c.Managed.Network, c.Managed.Host, c.Managed.Port, c.Managed.TLS = "NewNet", "irc2.example.org", 6667, false
	p.Mode = New(c)
	if n, err := p.Reconcile(ctx); err != nil || n != 1 {
		t.Fatalf("reconcile: %d %v", n, err)
	}
	nets, _ := acc.ListNetworks(ctx, u.ID)
	if len(nets) != 1 || nets[0].Name != "NewNet" || nets[0].Host != "irc2.example.org" || nets[0].TLS || nets[0].Nick != "Al" {
		t.Errorf("after: %+v", nets)
	}
}

func TestHealth(t *testing.T) {
	p, fb, acc := testProvisioner(t)
	ctx := context.Background()
	a, _ := p.Put(ctx, "alice", UserInput{})
	b, _ := p.Put(ctx, "bob", UserInput{Disabled: ptr(true)})
	acc.Create(accounts.CreateInput{Username: "carol"}) // no network
	na, _ := acc.ListNetworks(ctx, a.ID)
	nb, _ := acc.ListNetworks(ctx, b.ID)
	fb.status[na[0].ID] = upstream.StatusReconnecting
	fb.clients[a.ID] = 3

	h, err := p.Health(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h.OK || h.Tokens || h.WebIRC || h.Users.Total != 3 || h.Users.Disabled != 1 || h.Users.Attached != 1 || h.Clients != 3 ||
		h.Connections["reconnecting"] != 1 || h.Connections["disconnected"] != 1 || h.Network.Name != "ExampleNet" {
		t.Errorf("health %+v", h)
	}
	// one connection up: the network is there, the other one's trouble is its own
	fb.status[nb[0].ID] = upstream.StatusConnected
	p.Mode.SetTokens(NewVerifier("https://issuer", "*", ""))
	if h, _ := p.Health(ctx); !h.OK || !h.Tokens {
		t.Errorf("healthy %+v", h)
	}
}
