// Package history stores chat history — every message and channel event of
// every buffer — outside the account database. Two backends implement Store:
//
//   - ScyllaDB (scylla://host1,host2/keyspace), shared by every evrd node, for
//     deployments that scale out; and
//   - Pebble (pebble:/var/lib/evrd/history), an embedded store on local disk,
//     for single-node installs, development and tests.
//
// Buffer names are case-insensitive: #Go and #go are one buffer.
package history

import (
	"fmt"
	"strings"
	"time"
)

// Message is one line of history: something said, or a channel event.
type Message struct {
	NetworkID int64
	Target    string // channel, or the other nick for a private buffer
	Sender    string
	// Kind is message | notice | action, or a channel event: join | part |
	// quit | kick | nick | topic | mode. For events, Sender is who did it,
	// Text the reason / topic / modes, and Extra the kicked nick or new nick.
	Kind  string
	Text  string
	Extra string
	Time  time.Time
	// Origin is which session sent it — transient, never stored — so the
	// sender's other sessions can show it while the sender avoids a double.
	Origin string
}

// Store is a history backend. Implementations are safe for concurrent use.
type Store interface {
	// Add appends a message to its buffer. It may return before the message
	// is durable (or, for a remote store, before it's visible to Recent).
	Add(m Message) error
	// Recent returns a buffer's last limit messages, oldest first.
	Recent(networkID int64, target string, limit int) ([]Message, error)
	// Buffers lists a network's buffers (channels and private conversations).
	Buffers(networkID int64) ([]string, error)
	// Prune keeps only the newest keep messages of each of a network's
	// buffers (keep <= 0 keeps everything).
	Prune(networkID int64, keep int) error
	// DeleteNetwork drops all of a network's history.
	DeleteNetwork(networkID int64) error
	// Networks lists the network IDs that have history, so history of
	// networks deleted elsewhere can be found and dropped.
	Networks() ([]int64, error)
	// Count counts a buffer's lines, stopping at max.
	Count(networkID int64, target string, max int) (int, error)
	// Ping checks the store is reachable.
	Ping() error
	// Close finishes pending writes and closes the store.
	Close() error
}

// Open opens the backend a DSN names:
//
//	pebble:/var/lib/evrd/history   (or a bare path)
//	scylla://host1,host2:9042/evrd?replication=1
func Open(dsn string) (Store, error) {
	switch {
	case strings.HasPrefix(dsn, "scylla://"), strings.HasPrefix(dsn, "cassandra://"):
		return openScylla(dsn)
	case strings.Contains(dsn, "://"):
		return nil, fmt.Errorf("history: unknown store %q (want pebble:<dir> or scylla://hosts/keyspace)", dsn)
	default:
		return openPebble(strings.TrimPrefix(dsn, "pebble:"))
	}
}
