package client

// Playback buffers' *status commands; the rules are bouncer.Playback's.

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/bouncer"
	"github.com/4evrd/evrd/internal/store"
)

// account is the user as of now (a web *status session has none loaded).
func (se *session) account() store.User {
	if se.acct.ID == 0 {
		if u, err := se.srv.acc.UserByID(context.Background(), se.uid); err == nil {
			se.acct = u
		}
	}
	return se.acct
}

// playRules is this network's buffer settings (bouncer.Playback), with this
// client not counted as on the network.
func (se *session) playRules() bouncer.Playback {
	except := ""
	if se.att != nil {
		except = se.att.ID()
	}
	p, err := se.srv.b.DevicePlayback(se.net, se.account(), except, se.device())
	if err != nil {
		se.srv.log.Warn("load buffer playback", "network", se.net.ID, "err", err)
	}
	return p
}

// device is this client's device for playback positions: its client
// password ("" without one: such clients can't be told apart).
func (se *session) device() string {
	if se.att == nil {
		return ""
	}
	if d := se.att.Info().Device; d != "" {
		return "password:" + d
	}
	return ""
}

// savePosition records how far this client's device has been given its
// network's lines: up to now when it left on its own (QUIT, or closing the
// connection), else up to when it was last heard from — a client that just
// went quiet may have missed what was sent since, so it gets that again
// rather than a gap.
func (se *session) savePosition() {
	key := se.device()
	if key == "" || se.net.ID == 0 || se.web != nil || se.locked != nil {
		return
	}
	se.dropMu.Lock()
	reason := se.dropped
	se.dropMu.Unlock()
	at := time.Now()
	if reason != bouncer.DropQuit && reason != bouncer.DropClosed && reason != "" {
		at = time.Unix(0, se.lastRead.Load())
	}
	if err := se.srv.b.SetDevicePosition(se.net.ID, key, at); err != nil {
		se.srv.log.Warn("save device position", "network", se.net.ID, "err", err)
	}
}

// markOnNetwork records that this IRC client is (or was, until now) on its
// network.
func (se *session) markOnNetwork() {
	if se.net.ID == 0 || se.web != nil {
		return
	}
	if err := se.srv.b.MarkOnNetwork(se.net.ID); err != nil {
		se.srv.log.Warn("save buffer playback", "network", se.net.ID, "err", err)
	}
}

// matchBuffers resolves buffer names or wildcards (#evrd*, *) against this
// network's channels and buffers. Names that match nothing are kept as given.
func (se *session) matchBuffers(patterns []string) []string {
	known := map[string]string{}
	for _, n := range se.srv.b.BufferNames(se.net) {
		known[strings.ToLower(n)] = n
	}
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if !seen[strings.ToLower(n)] {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	for _, p := range patterns {
		if !strings.ContainsAny(p, "*?") {
			add(cmp.Or(known[strings.ToLower(p)], p))
			continue
		}
		var hits []string
		for k, n := range known {
			if ok, _ := path.Match(strings.ToLower(p), k); ok {
				hits = append(hits, n)
			}
		}
		slices.SortFunc(hits, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
		for _, h := range hits {
			add(h)
		}
	}
	return out
}

// allBuffers is this network's buffers: channels, queries, or both.
func (se *session) allBuffers(channels, queries bool) []string {
	var out []string
	for _, n := range se.matchBuffers([]string{"*"}) {
		if isChannelName(n) && channels || !isChannelName(n) && queries {
			out = append(out, n)
		}
	}
	return out
}

func (se *session) updatePlayback(buffers []string, f func(*store.BufferPlayback)) bool {
	if err := se.srv.b.UpdatePlayback(se.net.ID, buffers, f); err != nil {
		se.say("saveFailed", "error", err)
		return false
	}
	return true
}

// --- commands ----------------------------------------------------------------

func (se *session) cmdBuffers([]string) {
	p := se.playRules()
	names := se.matchBuffers([]string{"*"})
	if len(names) == 0 {
		se.say("buffers.none", "network", se.net.Name)
		return
	}
	for _, n := range names {
		mode := se.tr("buffers.keeps")
		if p.AutoClears(n) {
			mode = se.tr("buffers.autoClears")
		}
		own := ""
		if bp := p.Own(n); bp.Lines != nil || bp.AutoClear != nil {
			own = " " + se.tr("buffers.own")
		}
		se.statusf("  %-24s %s · %-11s · %s%s", n, se.tr("buffers.lines", "lines", fmt.Sprintf("%4d", p.Lines(n))), mode, se.tr("buffers.waiting", "count", se.srv.b.Waiting(p, se.net.ID, n)), own)
	}
	se.say("buffers.footer")
}

func (se *session) cmdPlayBuffer(args []string) {
	if se.web != nil {
		se.say("web.history")
		return
	}
	if len(args) != 1 {
		se.say("usage.playbuffer")
		return
	}
	p := se.playRules()
	name := se.matchBuffers(args[:1])[0]
	if sent := se.playbackSince(name, max(p.Lines(name), 1), p.ClearedAt(name)); sent == 0 {
		se.say("playbuffer.empty", "buffer", name)
	} else {
		se.say("replayed", "count", sent, "buffer", name)
	}
}

// clearBuffers clears names; what is their kind: buffers, channelBuffers
// or queryBuffers.
func (se *session) clearBuffers(names []string, what string) {
	if len(names) == 0 {
		se.say("clear.none."+what, "network", se.net.Name)
		return
	}
	if err := se.srv.b.ClearBuffers(se.net.ID, names); err != nil {
		se.say("saveFailed", "error", err)
	} else {
		se.say("clear.done", "buffers", se.tr("count."+what, "count", len(names)), "network", se.net.Name)
	}
}

func (se *session) cmdClearBuffer(args []string) {
	if len(args) == 0 {
		se.say("usage.clearbuffer")
		return
	}
	var pats []string
	for _, a := range args {
		pats = append(pats, strings.Split(a, ",")...)
	}
	se.clearBuffers(se.matchBuffers(pats), "buffers")
}

func (se *session) cmdClearAllBuffers([]string) {
	se.clearBuffers(se.allBuffers(true, true), "buffers")
}
func (se *session) cmdClearAllChannelBuffers([]string) {
	se.clearBuffers(se.allBuffers(true, false), "channelBuffers")
}
func (se *session) cmdClearAllQueryBuffers([]string) {
	se.clearBuffers(se.allBuffers(false, true), "queryBuffers")
}

func (se *session) cmdSetBuffer(args []string) {
	if len(args) == 0 || len(args) > 2 {
		se.say("usage.setbuffer")
		return
	}
	whole := args[0] == "*"
	if len(args) == 1 {
		if p := se.playRules(); whole {
			se.say("setbuffer.network", "network", se.net.Name, "lines", p.NetworkLines())
		} else {
			se.say("setbuffer.buffer", "buffer", args[0], "lines", p.Lines(args[0]))
		}
		return
	}
	var lines *int
	if args[1] != "-" {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 0 || v > maxPlayback {
			se.say("setbuffer.badLines", "max", maxPlayback)
			return
		}
		lines = &v
	}
	targets := []string{""}
	if !whole {
		targets = se.matchBuffers(strings.Split(args[0], ","))
	}
	if !se.updatePlayback(targets, func(bp *store.BufferPlayback) { bp.Lines = lines }) {
		return
	}
	p := se.playRules()
	if whole {
		se.say("setbuffer.networkSet", "network", se.net.Name, "lines", p.NetworkLines())
		return
	}
	for _, t := range targets {
		se.say("setbuffer.buffer", "buffer", t, "lines", p.Lines(t))
	}
}

func (se *session) cmdAutoClear(args []string) {
	if len(args) != 2 {
		se.say("usage.autoclear")
		return
	}
	var v *bool
	switch strings.ToLower(args[1]) {
	case "on", "yes", "true":
		t := true
		v = &t
	case "off", "no", "false":
		f := false
		v = &f
	case "-", "default":
	default:
		se.say("autoclear.bad")
		return
	}
	targets := se.matchBuffers(strings.Split(args[0], ","))
	if !se.updatePlayback(targets, func(bp *store.BufferPlayback) { bp.AutoClear = v }) {
		return
	}
	p := se.playRules()
	for _, t := range targets {
		if p.AutoClears(t) {
			se.say("autoclear.on", "buffer", t)
		} else {
			se.say("autoclear.off", "buffer", t, "lines", p.Lines(t))
		}
	}
}
