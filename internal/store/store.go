// Package store is evrd's persistence layer: users, their IRC networks, and
// buffered message history, backed by SQLite (pure-Go driver, no CGO).
package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

type Store struct{ db *sql.DB }

// User is a bouncer account.
type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

// Network is one IRC network configured by a user.
type Network struct {
	ID       int64
	UserID   int64
	Name     string
	Host     string
	Port     int
	TLS      bool
	Nick     string
	Username string
	Realname string
	Password string
	Channels []string
	Enabled  bool
}

// Message is a single buffered line for a target (channel or nick).
type Message struct {
	ID        int64
	NetworkID int64
	Target    string
	Sender    string
	Kind      string // message | notice | action | system
	Text      string
	Time      time.Time
}

// Open opens (and migrates) the SQLite database. The DSN may be a plain path
// or a "sqlite:/path" URL.
func Open(dsn string) (*Store, error) {
	path := strings.TrimPrefix(dsn, "sqlite:")
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: serialise writers
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS users (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  username      TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at    INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS networks (
  id        INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id   INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name      TEXT NOT NULL,
  host      TEXT NOT NULL,
  port      INTEGER NOT NULL,
  tls       INTEGER NOT NULL DEFAULT 1,
  nick      TEXT NOT NULL,
  username  TEXT NOT NULL DEFAULT '',
  realname  TEXT NOT NULL DEFAULT '',
  password  TEXT NOT NULL DEFAULT '',
  channels  TEXT NOT NULL DEFAULT '',
  enabled   INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE IF NOT EXISTS messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  network_id INTEGER NOT NULL REFERENCES networks(id) ON DELETE CASCADE,
  target     TEXT NOT NULL,
  sender     TEXT NOT NULL,
  kind       TEXT NOT NULL,
  text       TEXT NOT NULL,
  ts         INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_target ON messages(network_id, target, id);
`)
	return err
}

// --- users ---------------------------------------------------------------

func (s *Store) CreateUser(username, passwordHash string) (User, error) {
	res, err := s.db.Exec(
		`INSERT INTO users(username, password_hash, created_at) VALUES(?, ?, ?)`,
		username, passwordHash, time.Now().Unix(),
	)
	if err != nil {
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, PasswordHash: passwordHash}, nil
}

func (s *Store) UserByUsername(username string) (User, error) {
	var u User
	err := s.db.QueryRow(
		`SELECT id, username, password_hash FROM users WHERE username = ?`, username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT id, username, password_hash FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// --- networks ------------------------------------------------------------

func scanNetwork(sc interface{ Scan(...any) error }) (Network, error) {
	var n Network
	var tls, enabled int
	var chans string
	err := sc.Scan(&n.ID, &n.UserID, &n.Name, &n.Host, &n.Port, &tls,
		&n.Nick, &n.Username, &n.Realname, &n.Password, &chans, &enabled)
	if err != nil {
		return Network{}, err
	}
	n.TLS = tls != 0
	n.Enabled = enabled != 0
	if chans != "" {
		n.Channels = strings.Split(chans, ",")
	}
	return n, nil
}

const networkCols = `id, user_id, name, host, port, tls, nick, username, realname, password, channels, enabled`

func (s *Store) ListNetworks(userID int64) ([]Network, error) {
	rows, err := s.db.Query(`SELECT `+networkCols+` FROM networks WHERE user_id = ? ORDER BY id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// AllEnabledNetworks returns every enabled network across all users (for
// auto-connect on daemon start).
func (s *Store) AllEnabledNetworks() ([]Network, error) {
	rows, err := s.db.Query(`SELECT ` + networkCols + ` FROM networks WHERE enabled = 1 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Network
	for rows.Next() {
		n, err := scanNetwork(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) NetworkByID(id int64) (Network, error) {
	n, err := scanNetwork(s.db.QueryRow(`SELECT `+networkCols+` FROM networks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Network{}, ErrNotFound
	}
	return n, err
}

func (s *Store) CreateNetwork(n Network) (Network, error) {
	res, err := s.db.Exec(
		`INSERT INTO networks(user_id, name, host, port, tls, nick, username, realname, password, channels, enabled)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.UserID, n.Name, n.Host, n.Port, b2i(n.TLS), n.Nick, n.Username, n.Realname,
		n.Password, strings.Join(n.Channels, ","), b2i(n.Enabled),
	)
	if err != nil {
		return Network{}, err
	}
	n.ID, _ = res.LastInsertId()
	return n, nil
}

func (s *Store) DeleteNetwork(userID, id int64) error {
	_, err := s.db.Exec(`DELETE FROM networks WHERE id = ? AND user_id = ?`, id, userID)
	return err
}

func (s *Store) SetNetworkEnabled(id int64, enabled bool) error {
	_, err := s.db.Exec(`UPDATE networks SET enabled = ? WHERE id = ?`, b2i(enabled), id)
	return err
}

// --- messages ------------------------------------------------------------

func (s *Store) AddMessage(m Message) error {
	_, err := s.db.Exec(
		`INSERT INTO messages(network_id, target, sender, kind, text, ts) VALUES(?, ?, ?, ?, ?, ?)`,
		m.NetworkID, m.Target, m.Sender, m.Kind, m.Text, m.Time.Unix(),
	)
	return err
}

// RecentMessages returns up to limit most-recent messages for a target, oldest
// first.
func (s *Store) RecentMessages(networkID int64, target string, limit int) ([]Message, error) {
	rows, err := s.db.Query(
		`SELECT id, network_id, target, sender, kind, text, ts FROM messages
		 WHERE network_id = ? AND target = ? ORDER BY id DESC LIMIT ?`,
		networkID, target, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var ts int64
		if err := rows.Scan(&m.ID, &m.NetworkID, &m.Target, &m.Sender, &m.Kind, &m.Text, &ts); err != nil {
			return nil, err
		}
		m.Time = time.Unix(ts, 0)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// reverse to oldest-first
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
