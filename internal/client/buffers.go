package client

// Playback buffers' *status commands; the rules are bouncer.Playback's.

import (
	"cmp"
	"context"
	"path"
	"slices"
	"strconv"
	"strings"

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
	p, err := se.srv.b.Playback(se.net, se.account(), except)
	if err != nil {
		se.srv.log.Warn("load buffer playback", "network", se.net.ID, "err", err)
	}
	return p
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
		se.statusf("Couldn't save: %v", err)
		return false
	}
	return true
}

// --- commands ----------------------------------------------------------------

func (se *session) cmdBuffers([]string) {
	p := se.playRules()
	names := se.matchBuffers([]string{"*"})
	if len(names) == 0 {
		se.statusf("No buffers on %s yet.", se.net.Name)
		return
	}
	for _, n := range names {
		mode := "keeps"
		if p.AutoClears(n) {
			mode = "auto-clears"
		}
		own := ""
		if bp := p.Own(n); bp.Lines != nil || bp.AutoClear != nil {
			own = " (own settings)"
		}
		se.statusf("  %-24s %4d lines · %-11s · %d to play back%s", n, p.Lines(n), mode, se.srv.b.Waiting(p, se.net.ID, n), own)
	}
	se.statusf("setbuffer <buffer|*> <lines> · autoclear <buffer> on|off · clearbuffer <buffer> · playbuffer <buffer>")
}

func (se *session) cmdPlayBuffer(args []string) {
	if se.web != nil {
		se.statusf("The web chat already shows each buffer's history — scroll up.")
		return
	}
	if len(args) != 1 {
		se.statusf("Usage: playbuffer <channel|nick> — or playback <channel|nick> [lines] for everything saved")
		return
	}
	p := se.playRules()
	name := se.matchBuffers(args[:1])[0]
	if sent := se.playbackSince(name, max(p.Lines(name), 1), p.ClearedAt(name)); sent == 0 {
		se.statusf("The buffer for %s is empty.", name)
	} else {
		se.statusf("Replayed %d line(s) of %s.", sent, name)
	}
}

func (se *session) clearBuffers(names []string, what string) {
	if len(names) == 0 {
		se.statusf("No %s to clear on %s.", what, se.net.Name)
		return
	}
	if err := se.srv.b.ClearBuffers(se.net.ID, names); err != nil {
		se.statusf("Couldn't save: %v", err)
	} else {
		se.statusf("Cleared %s on %s — playback starts after now. (History still has them: playback <buffer> or the web chat.)", plural(len(names), what), se.net.Name)
	}
}

func (se *session) cmdClearBuffer(args []string) {
	if len(args) == 0 {
		se.statusf("Usage: clearbuffer <channel|nick>[,…] (wildcards work: clearbuffer #evrd*)")
		return
	}
	var pats []string
	for _, a := range args {
		pats = append(pats, strings.Split(a, ",")...)
	}
	se.clearBuffers(se.matchBuffers(pats), "buffer")
}

func (se *session) cmdClearAllBuffers([]string) { se.clearBuffers(se.allBuffers(true, true), "buffer") }
func (se *session) cmdClearAllChannelBuffers([]string) {
	se.clearBuffers(se.allBuffers(true, false), "channel buffer")
}
func (se *session) cmdClearAllQueryBuffers([]string) {
	se.clearBuffers(se.allBuffers(false, true), "query buffer")
}

func (se *session) cmdSetBuffer(args []string) {
	if len(args) == 0 || len(args) > 2 {
		se.statusf("Usage: setbuffer <channel|nick|*> [lines|-] — * is this network's default; - goes back to the default")
		return
	}
	whole := args[0] == "*"
	if len(args) == 1 {
		if p := se.playRules(); whole {
			se.statusf("%s plays back %d lines per buffer.", se.net.Name, p.NetworkLines())
		} else {
			se.statusf("%s plays back %d lines.", args[0], p.Lines(args[0]))
		}
		return
	}
	var lines *int
	if args[1] != "-" {
		v, err := strconv.Atoi(args[1])
		if err != nil || v < 0 || v > maxPlayback {
			se.statusf("Lines is a number from 0 to %d (or - for the default).", maxPlayback)
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
		se.statusf("Buffers on %s play back %d lines unless they have their own setting.", se.net.Name, p.NetworkLines())
		return
	}
	for _, t := range targets {
		se.statusf("%s plays back %d lines.", t, p.Lines(t))
	}
}

func (se *session) cmdAutoClear(args []string) {
	if len(args) != 2 {
		se.statusf("Usage: autoclear <channel|nick>[,…] on|off|- — on plays only what's new since a client was last here; - goes back to your account's setting")
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
		se.statusf("autoclear is on, off or - (your account's setting).")
		return
	}
	targets := se.matchBuffers(strings.Split(args[0], ","))
	if !se.updatePlayback(targets, func(bp *store.BufferPlayback) { bp.AutoClear = v }) {
		return
	}
	p := se.playRules()
	for _, t := range targets {
		if p.AutoClears(t) {
			se.statusf("%s auto-clears: playback is only what's new.", t)
		} else {
			se.statusf("%s keeps its buffer: playback is its last %d lines.", t, p.Lines(t))
		}
	}
}
