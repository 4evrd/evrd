// Package zncimport reads ZNC's log module files into evrd's history, so
// people moving from ZNC keep their old conversations.
//
// ZNC writes one file per buffer per day, either per user
// (users/<user>/moddata/log/<network>/<buffer>/<YYYY-MM-DD>.log) or per
// network (users/<user>/networks/<network>/moddata/log/<buffer>/<date>.log),
// with lines like "[12:34:56] <nick> hello" in the user's timezone.
package zncimport

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/history"
)

var (
	lineRE   = regexp.MustCompile(`^\[(\d\d):(\d\d):(\d\d)\] (.*)$`)
	joinRE   = regexp.MustCompile(`^\*\*\* Joins: (\S+) \(`)
	partRE   = regexp.MustCompile(`^\*\*\* Parts: (\S+) \([^)]*\)(?: \((.*)\))?$`)
	quitRE   = regexp.MustCompile(`^\*\*\* Quits: (\S+) \([^)]*\)(?: \((.*)\))?$`)
	kickRE   = regexp.MustCompile(`^\*\*\* (\S+) was kicked by (\S+)(?: \((.*)\))?$`)
	nickRE   = regexp.MustCompile(`^\*\*\* (\S+) is now known as (\S+)$`)
	topicRE  = regexp.MustCompile(`^\*\*\* (\S+) changes topic to '(.*)'$`)
	modeRE   = regexp.MustCompile(`^\*\*\* (\S+) sets mode: (.+)$`)
	msgRE    = regexp.MustCompile(`^<([^>]+)> (.*)$`)
	actionRE = regexp.MustCompile(`^\* (\S+) (.*)$`)
	noticeRE = regexp.MustCompile(`^-([^-\s]+)- (.*)$`)
)

// ParseLine turns one log line, from a file for buffer on day, into a
// history message. ok is false for lines that aren't chat or a channel
// event (ZNC's "Connected to IRC" and the like).
func ParseLine(day time.Time, buffer, line string) (m history.Message, ok bool) {
	g := lineRE.FindStringSubmatch(strings.TrimRight(line, "\r\n"))
	if g == nil {
		return m, false
	}
	h, mi, s := atoi(g[1]), atoi(g[2]), atoi(g[3])
	m.Time = time.Date(day.Year(), day.Month(), day.Day(), h, mi, s, 0, day.Location())
	m.Target = buffer
	rest := g[4]
	set := func(kind, sender, text, extra string) (history.Message, bool) {
		m.Kind, m.Sender, m.Text, m.Extra = kind, sender, text, extra
		return m, true
	}
	switch {
	case strings.HasPrefix(rest, "*** "):
		if x := joinRE.FindStringSubmatch(rest); x != nil {
			return set("join", x[1], "", "")
		}
		if x := partRE.FindStringSubmatch(rest); x != nil {
			return set("part", x[1], x[2], "")
		}
		if x := quitRE.FindStringSubmatch(rest); x != nil {
			return set("quit", x[1], x[2], "")
		}
		if x := kickRE.FindStringSubmatch(rest); x != nil {
			return set("kick", x[2], x[3], x[1])
		}
		if x := nickRE.FindStringSubmatch(rest); x != nil {
			return set("nick", x[1], "", x[2])
		}
		if x := topicRE.FindStringSubmatch(rest); x != nil {
			return set("topic", x[1], x[2], "")
		}
		if x := modeRE.FindStringSubmatch(rest); x != nil {
			return set("mode", x[1], x[2], "")
		}
		return m, false
	case strings.HasPrefix(rest, "<"):
		if x := msgRE.FindStringSubmatch(rest); x != nil {
			return set("message", x[1], x[2], "")
		}
	case strings.HasPrefix(rest, "* "):
		if x := actionRE.FindStringSubmatch(rest); x != nil {
			return set("action", x[1], x[2], "")
		}
	case strings.HasPrefix(rest, "-"):
		if x := noticeRE.FindStringSubmatch(rest); x != nil {
			return set("notice", x[1], x[2], "")
		}
	}
	return m, false
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

// File is one day of one buffer.
type File struct {
	Network, Buffer string
	Day             time.Time
	Path            string
}

// Find lists a ZNC user's log files (userDir is users/<user>), both layouts.
// When both have the same network, buffer and day — the log module loaded
// for the user and for a network — the user's copy wins, so nothing is
// imported twice. Buffers whose names start with * (ZNC's own: *status)
// are left out.
func Find(userDir string, loc *time.Location) ([]File, error) {
	seen := map[string]bool{}
	var out []File
	add := func(network, buffer, path string) {
		day, err := time.ParseInLocation("2006-01-02", strings.TrimSuffix(filepath.Base(path), ".log"), loc)
		if err != nil || strings.HasPrefix(buffer, "*") {
			return
		}
		k := strings.ToLower(network + "\x00" + buffer + "\x00" + day.Format("2006-01-02"))
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, File{Network: network, Buffer: buffer, Day: day, Path: path})
	}
	userLogs, _ := filepath.Glob(filepath.Join(userDir, "moddata", "log", "*", "*", "*.log"))
	for _, p := range userLogs {
		buf := filepath.Dir(p)
		add(filepath.Base(filepath.Dir(buf)), filepath.Base(buf), p)
	}
	netLogs, _ := filepath.Glob(filepath.Join(userDir, "networks", "*", "moddata", "log", "*", "*.log"))
	for _, p := range netLogs {
		buf := filepath.Dir(p)
		add(filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(buf)))), filepath.Base(buf), p)
	}
	// oldest first, so each buffer's history is written in order
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Day.Equal(out[j].Day) {
			return out[i].Day.Before(out[j].Day)
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// Read parses a file's lines, calling add for each message.
func Read(f File, add func(history.Message) error) (n int, err error) {
	fh, err := os.Open(f.Path)
	if err != nil {
		return 0, err
	}
	defer fh.Close()
	return readFrom(fh, f, add)
}

func readFrom(r io.Reader, f File, add func(history.Message) error) (int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		m, ok := ParseLine(f.Day, f.Buffer, sc.Text())
		if !ok {
			continue
		}
		if err := add(m); err != nil {
			return n, err
		}
		n++
	}
	return n, sc.Err()
}
