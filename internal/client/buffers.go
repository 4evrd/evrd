package client

// Playback buffers, per network and per channel or query, like ZNC's: how
// many lines each plays back when an IRC client attaches, whether it plays
// only what's new (auto-clear), and ClearBuffer. The lines themselves stay in
// history (the web chat and chathistory still have them); a buffer's settings
// and clear mark only change what playback sends.

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

// account is the user as of now (a web *status session has none loaded).
func (se *session) account() store.User {
	if se.acct.ID == 0 {
		if u, err := se.srv.b.Store().UserByID(se.uid); err == nil {
			se.acct = u
		}
	}
	return se.acct
}

// playbacks is this network's buffer settings.
func (se *session) playbacks() map[string]store.BufferPlayback {
	bps, err := se.srv.b.Store().BufferPlaybacks(se.net.ID)
	if err != nil {
		se.srv.log.Warn("load buffer playback", "network", se.net.ID, "err", err)
	}
	return bps
}

// lastOnNetwork is when an IRC client was last on this network: now while
// another one is, else when one last left (never: zero).
func (se *session) lastOnNetwork(bps map[string]store.BufferPlayback) time.Time {
	for _, c := range se.srv.b.ClientsOf(se.uid) {
		if c.Kind == "irc" && (se.att == nil || c.ID != se.att.ID()) && slices.Contains(c.Networks, se.net.Name) {
			return time.Now()
		}
	}
	if at := bps[""].ClearedAt; at != nil {
		return *at
	}
	return time.Time{}
}

// markOnNetwork records that an IRC client is (or was, until now) on this
// network, so auto-clearing buffers start over from here.
func (se *session) markOnNetwork() {
	if se.net.ID == 0 || se.web != nil {
		return
	}
	now := time.Now()
	if err := se.srv.b.Store().UpdateBufferPlayback(se.net.ID, []string{""}, func(bp *store.BufferPlayback) { bp.ClearedAt = &now }); err != nil {
		se.srv.log.Warn("save buffer playback", "network", se.net.ID, "err", err)
	}
}

// bufferLines is how many lines a buffer plays back: its own setting, else
// the network's, else the account's.
func (se *session) bufferLines(bps map[string]store.BufferPlayback, name string) int {
	if l := bps[strings.ToLower(name)].Lines; l != nil {
		return *l
	}
	if l := bps[""].Lines; l != nil {
		return *l
	}
	return se.account().PlaybackLines
}

// autoClears reports whether a buffer plays only what's new.
func (se *session) autoClears(bps map[string]store.BufferPlayback, name string) bool {
	if a := bps[strings.ToLower(name)].AutoClear; a != nil {
		return *a
	}
	if isChannelName(name) {
		return se.account().AutoClearChanBuffer
	}
	return se.account().AutoClearQueryBuffer
}

// playFrom is what a buffer plays back from: after its clear mark and, when
// it auto-clears, after an IRC client was last on the network.
func (se *session) playFrom(bps map[string]store.BufferPlayback, name string, lastOn time.Time) time.Time {
	var since time.Time
	if at := bps[strings.ToLower(name)].ClearedAt; at != nil {
		since = *at
	}
	if se.autoClears(bps, name) && lastOn.After(since) {
		since = lastOn
	}
	return since
}

// matchBuffers resolves buffer names or wildcards (#evrd*, *) against this
// network's channels and buffers. Names that match nothing are kept as given.
func (se *session) matchBuffers(patterns []string) []string {
	known := map[string]string{}
	names, _ := se.srv.b.History().Buffers(se.net.ID)
	if se.up != nil {
		names = append(names, se.up.Channels()...)
	}
	for _, n := range names {
		if n != "" {
			known[strings.ToLower(n)] = n
		}
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
	if err := se.srv.b.Store().UpdateBufferPlayback(se.net.ID, buffers, f); err != nil {
		se.statusf("Couldn't save: %v", err)
		return false
	}
	return true
}

// --- commands ----------------------------------------------------------------

func (se *session) cmdBuffers([]string) {
	bps := se.playbacks()
	lastOn := se.lastOnNetwork(bps)
	names := se.matchBuffers([]string{"*"})
	if len(names) == 0 {
		se.statusf("No buffers on %s yet.", se.net.Name)
		return
	}
	for _, n := range names {
		lines := se.bufferLines(bps, n)
		msgs, _ := se.srv.b.History().Recent(se.net.ID, n, lines)
		since := se.playFrom(bps, n, lastOn)
		waiting := 0
		for _, m := range msgs {
			if m.Time.After(since) {
				waiting++
			}
		}
		mode := "keeps"
		if se.autoClears(bps, n) {
			mode = "auto-clears"
		}
		own := ""
		if bp, ok := bps[strings.ToLower(n)]; ok && (bp.Lines != nil || bp.AutoClear != nil) {
			own = " (own settings)"
		}
		se.statusf("  %-24s %4d lines · %-11s · %d to play back%s", n, lines, mode, waiting, own)
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
	bps := se.playbacks()
	name := se.matchBuffers(args[:1])[0]
	since := time.Time{}
	if at := bps[strings.ToLower(name)].ClearedAt; at != nil {
		since = *at
	}
	if sent := se.playbackSince(name, max(se.bufferLines(bps, name), 1), since); sent == 0 {
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
	now := time.Now()
	if se.updatePlayback(names, func(bp *store.BufferPlayback) { bp.ClearedAt = &now }) {
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
	bps := se.playbacks()
	whole := args[0] == "*"
	if len(args) == 1 {
		if whole {
			se.statusf("%s plays back %d lines per buffer.", se.net.Name, se.bufferLines(bps, ""))
		} else {
			se.statusf("%s plays back %d lines.", args[0], se.bufferLines(bps, args[0]))
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
	bps = se.playbacks()
	if whole {
		se.statusf("Buffers on %s play back %d lines unless they have their own setting.", se.net.Name, se.bufferLines(bps, ""))
		return
	}
	for _, t := range targets {
		se.statusf("%s plays back %d lines.", t, se.bufferLines(bps, t))
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
	bps := se.playbacks()
	for _, t := range targets {
		if se.autoClears(bps, t) {
			se.statusf("%s auto-clears: playback is only what's new.", t)
		} else {
			se.statusf("%s keeps its buffer: playback is its last %d lines.", t, se.bufferLines(bps, t))
		}
	}
}
