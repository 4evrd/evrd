package client

import (
	"context"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// setUser changes the rig's account.
func (r *rig) setUser(f func(*store.User)) {
	r.t.Helper()
	u, err := r.st.UserByID(context.Background(), r.user.ID)
	if err != nil {
		r.t.Fatal(err)
	}
	f(&u)
	if err := r.st.UpdateUser(context.Background(), u); err != nil {
		r.t.Fatal(err)
	}
}

func TestClientNotifyTellsTheOtherClients(t *testing.T) {
	r := newRig(t)
	r.srv.clientNotifyWait = 50 * time.Millisecond
	r.setUser(func(u *store.User) { u.ClientNotify, u.ClientNotifyOnDisconnect = true, true })
	a := r.attached()
	b := r.attached()
	a.expect("Another client attached to your account: IRC client from 127.0.0.1. Clients attached now: 2")
	if b.tryExpect("Another client attached", 300*time.Millisecond) {
		t.Error("the new client heard of itself")
	}
	b.send("QUIT :bye")
	a.expect("A client left your account: IRC client from 127.0.0.1. Clients attached now: 1.")
}

func TestClientNotifyOffAndNewAddressesOnly(t *testing.T) {
	r := newRig(t)
	r.srv.clientNotifyWait = 50 * time.Millisecond
	a := r.attached()
	r.attached()
	if a.tryExpect("Another client attached", 300*time.Millisecond) {
		t.Error("told with clientnotify off (the default)")
	}

	// only new addresses: 127.0.0.1 is known once a client came from it
	r.setUser(func(u *store.User) { u.ClientNotify, u.ClientNotifyNewOnly = true, true })
	a = r.attached()
	r.attached()
	if a.tryExpect("Another client attached", 300*time.Millisecond) {
		t.Error("told about an address seen before")
	}
	// and no disconnect notice without ondisconnect
	if ok, _ := r.st.SeeClientIP(context.Background(), r.user.ID, "127.0.0.1", time.Now(), 50); ok {
		t.Error("127.0.0.1 wasn't remembered")
	}
}

func TestClientNotifyCommand(t *testing.T) {
	r := newRig(t)
	ic := r.attached()
	ic.status("clientnotify", "Client notices: off · new addresses only: off · on disconnect: off.")
	ic.status("clientnotify on", "Client notices: on · new addresses only: off")
	ic.status("clientnotify newonly on", "new addresses only: on · on disconnect: off.")
	ic.status("clientnotify ondisconnect yes", "on disconnect: on.")
	ic.status("clientnotify method off", "Client notices: off · new addresses only: on · on disconnect: on.")
	ic.status("clientnotify maybe", "Usage: clientnotify")
	ic.status("clientnotify newonly sometimes", "Usage: clientnotify")
	u, _ := r.st.UserByID(context.Background(), r.user.ID)
	if u.ClientNotify || !u.ClientNotifyNewOnly || !u.ClientNotifyOnDisconnect {
		t.Errorf("saved: %v %v %v", u.ClientNotify, u.ClientNotifyNewOnly, u.ClientNotifyOnDisconnect)
	}
}
