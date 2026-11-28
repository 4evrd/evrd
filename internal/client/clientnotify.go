package client

// clientnotify (ZNC's): with the account's ClientNotify on, its other IRC
// clients hear from *status when one attaches (only from an address not
// seen lately, with ClientNotifyNewOnly), and when one goes
// (ClientNotifyOnDisconnect). Web chats aren't counted: a browser opens
// and closes them with every tab and reload.

import (
	"context"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/messages"
)

// clientNotifyWait is how long the notice about a new client waits for the
// client to say what it is (its CTCP VERSION reply).
const clientNotifyWait = 3 * time.Second

// hearTells lets other sessions pass this client notices (TellOthers),
// which the bridge sends (told).
func (se *session) hearTells(att *bouncer.Attachment) {
	if att == nil || se.isManaged() {
		return // managed mode has no *status
	}
	if se.tells == nil {
		se.tells = make(chan messages.Text, 8)
	}
	tells := se.tells
	att.Tell(func(t messages.Text) {
		select {
		case tells <- t:
		default: // a client that isn't reading misses it
		}
	})
}

// told sends a notice another session passed on, in the account's
// language.
func (se *session) told(t messages.Text) {
	se.pr = nil // the account's language may have changed since
	se.statusf("%s", se.printer().Text(t))
}

// notifyAttached tells the account's other IRC clients this one attached,
// once it has said what it is.
func (se *session) notifyAttached() {
	if se.att == nil || se.isManaged() {
		return
	}
	u, err := se.srv.acc.UserByID(context.Background(), se.uid)
	if err != nil || !u.ClientNotify {
		return
	}
	ip := se.clientIP()
	if fresh := se.srv.b.SeeClientIP(u.ID, ip); u.ClientNotifyNewOnly && !fresh {
		return
	}
	att, uid, wait := se.att, se.uid, orDefault(se.srv.clientNotifyWait, clientNotifyWait)
	go func() {
		time.Sleep(wait)
		se.srv.b.TellOthers(uid, att.ID(), messages.T("clientNotify.attached", "ip", orElse(ip, "?"),
			"client", clientText(att.Info()), "count", se.srv.b.Clients(uid)))
	}()
}

// notifyDetached tells the account's other IRC clients this one went.
func (se *session) notifyDetached() {
	if se.att == nil || se.locked != nil || se.isManaged() {
		return
	}
	u, err := se.srv.acc.UserByID(context.Background(), se.uid)
	if err != nil || !u.ClientNotify || !u.ClientNotifyOnDisconnect {
		return
	}
	info := se.att.Info()
	// the count is before this one's slot is freed
	se.srv.b.TellOthers(se.uid, info.ID, messages.T("clientNotify.detached", "ip", orElse(info.IP, "?"),
		"client", clientText(info), "count", max(se.srv.b.Clients(se.uid)-1, 0)))
}

// clientText names a client in a notice: its software, or "IRC client".
func clientText(info bouncer.ClientInfo) any {
	if info.Software != "" {
		return info.Software
	}
	return messages.T("clients.ircClient")
}

// cmdClientNotify shows or changes clientnotify: on, off, newonly on|off,
// ondisconnect on|off (ZNC's method notice|message|off too).
func (se *session) cmdClientNotify(args []string) {
	u, err := se.srv.acc.UserByID(context.Background(), se.uid)
	if err != nil {
		se.say("accountFailedPlain")
		return
	}
	flag := func(v string) (bool, bool) {
		switch strings.ToLower(v) {
		case "on", "yes", "true":
			return true, true
		case "off", "no", "false":
			return false, true
		}
		return false, false
	}
	ok := true
	switch len(args) {
	case 0:
	case 1:
		u.ClientNotify, ok = flag(args[0])
	case 2:
		var v bool
		switch strings.ToLower(args[0]) {
		case "newonly":
			v, ok = flag(args[1])
			u.ClientNotifyNewOnly = v
		case "ondisconnect":
			v, ok = flag(args[1])
			u.ClientNotifyOnDisconnect = v
		case "method": // ZNC's: notice and message both mean on here
			m := strings.ToLower(args[1])
			ok = m == "notice" || m == "message" || m == "off"
			u.ClientNotify = m != "off"
		default:
			ok = false
		}
	default:
		ok = false
	}
	if !ok {
		se.say("usage.clientnotify")
		return
	}
	if len(args) > 0 {
		if err := se.srv.acc.UpdateUser(context.Background(), u); err != nil {
			se.say("saveFailed", "error", err)
			return
		}
		se.acct.ClientNotify, se.acct.ClientNotifyNewOnly, se.acct.ClientNotifyOnDisconnect = u.ClientNotify, u.ClientNotifyNewOnly, u.ClientNotifyOnDisconnect
	}
	se.say("clientNotify.state", "state", onOff(u.ClientNotify), "newonly", onOff(u.ClientNotifyNewOnly), "ondisconnect", onOff(u.ClientNotifyOnDisconnect))
}
