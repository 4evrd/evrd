package bouncer

import (
	"sync"
	"time"

	"github.com/4evrd/evrd/internal/upstream"
)

// netLogSize is how many connection log entries each network keeps.
const netLogSize = 300

// netLogs keeps each network's connection log (upstream.LogEntry), in
// memory on the node that runs it: what evrd did and what the server said
// while connecting, across reconnects and switching it off and on.
type netLogs struct {
	mu   sync.Mutex
	logs map[int64][]upstream.LogEntry
}

func (l *netLogs) add(networkID int64, e upstream.LogEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.logs == nil {
		l.logs = map[int64][]upstream.LogEntry{}
	}
	log := append(l.logs[networkID], e)
	if len(log) > netLogSize {
		log = append([]upstream.LogEntry(nil), log[len(log)-netLogSize:]...)
	}
	l.logs[networkID] = log
}

func (l *netLogs) get(networkID int64) []upstream.LogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]upstream.LogEntry{}, l.logs[networkID]...)
}

func (l *netLogs) drop(networkID int64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.logs, networkID)
}

// NetworkLog is a network's connection log, oldest first.
func (b *Bouncer) NetworkLog(networkID int64) []upstream.LogEntry { return b.netlog.get(networkID) }

func (b *Bouncer) logNote(networkID int64, text string) {
	b.netlog.add(networkID, upstream.LogEntry{At: time.Now(), From: "evrd", Text: text})
}
