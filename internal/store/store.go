// Package store is evrd's account database: users, their sessions and IRC
// client passwords, and their IRC networks. Chat history lives elsewhere, in
// internal/history.
//
// Store is an interface; the implementation uses GORM (see gorm.go), so the
// backend is chosen by DSN scheme — postgres in deployments, sqlite for tests
// and single-file installs — with no change to callers.
package store

import (
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("store: not found")

// Roles a user can have.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// User is a bouncer account.
type User struct {
	ID       int64  `gorm:"primaryKey"`
	Username string `gorm:"uniqueIndex;not null"`
	Email    string
	// PasswordHash is the bcrypt hash of the local password; empty when the
	// account only signs in through the identity provider.
	PasswordHash string
	// OIDCSubject links the account to its identity-provider user (the ID
	// token's "sub"). Nil for local-only accounts.
	OIDCSubject *string `gorm:"column:oidc_subject;uniqueIndex"`
	Role        string  `gorm:"not null;default:user"`
	// SSOClaimable marks an account made ahead of its owner's first sign-in
	// (by the provisioning API): the first SSO sign-in with the same username
	// and email links to it, even without oidc_link_existing.
	SSOClaimable bool `gorm:"not null;default:false"`

	Suspended       bool `gorm:"not null;default:false"`
	SuspendedReason string

	// Per-user limits. Nil means "use the server default"; 0 means unlimited.
	MaxNetworks  *int
	MaxClients   *int
	BacklogLines *int

	// Defaults that prefill a new network.
	DefaultNick     string
	DefaultAltNick  string // tried when the nick is taken
	DefaultRealname string
	DefaultIdent    string
	// QuitMsg is sent when a network is disconnected (networks can override).
	QuitMsg string
	// BindHost is the default local address networks connect from.
	BindHost string
	// DenySetBindHost stops the user choosing a bind host (admins set it).
	DenySetBindHost bool `gorm:"not null;default:false"`
	// AutoAway marks the user away on every network while no client (IRC or
	// web) is attached.
	AutoAway bool `gorm:"not null;default:false"`
	// Language is the web app's language (a code from the app's list); ""
	// follows the browser.
	Language string `gorm:"not null;default:''"`

	// Playback to IRC clients: how many lines per channel and query, and
	// whether a buffer, once played back, starts empty next time (only lines
	// since the last playback are sent).
	PlaybackLines        int  `gorm:"not null;default:50"`
	AutoClearChanBuffer  bool `gorm:"not null;default:false"`
	AutoClearQueryBuffer bool `gorm:"not null;default:true"`
	// Timestamps for clients without server-time: the time is added to the
	// text (before or after) in TimestampFormat (strftime) and Timezone.
	PrependTimestamp bool   `gorm:"not null;default:true"`
	AppendTimestamp  bool   `gorm:"not null;default:false"`
	TimestampFormat  string `gorm:"not null;default:'[%H:%M:%S]'"`
	Timezone         string
	// CTCPReplies answers CTCP requests (VERSION, TIME, …) for every network
	// with a fixed text; an empty text blocks the request.
	CTCPReplies map[string]string `gorm:"serializer:json"`
	// AllowedHosts restricts where clients may sign in from (IPs, CIDRs or
	// wildcards like 192.168.*); empty allows anywhere.
	AllowedHosts []string `gorm:"serializer:json"`

	// MustChangePassword is set for a temporary password an admin chose.
	MustChangePassword bool `gorm:"not null;default:false"`

	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastLoginAt *time.Time
	// LastSeenAt is the last time a client attached or detached, or the web
	// app was used.
	LastSeenAt *time.Time
}

// IsAdmin reports whether the user has the admin role.
func (u User) IsAdmin() bool { return u.Role == RoleAdmin }

// Session is a signed-in web session. ID is the SHA-256 of the cookie token,
// so a leaked database row can't be replayed as a cookie.
type Session struct {
	ID         string `gorm:"primaryKey;size:64"`
	UserID     int64  `gorm:"index;not null"`
	Method     string // password | oidc
	IDToken    string // OIDC id_token, kept only as the logout hint
	IP         string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time `gorm:"index"`
}

// ClientPassword lets a native IRC client sign in (PASS user:secret) — the
// only way to attach for accounts that sign in to the web through SSO.
type ClientPassword struct {
	ID         int64  `gorm:"primaryKey"`
	UserID     int64  `gorm:"index;not null"`
	Name       string `gorm:"not null"`
	Hash       string `gorm:"not null"`
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// Network is one IRC network configured by a user.
type Network struct {
	ID     int64 `gorm:"primaryKey"`
	UserID int64 `gorm:"index;not null"`
	Name   string
	// Host, Port and TLS mirror the first entry of Servers (kept as columns
	// for listings and older rows).
	Host string
	Port int
	TLS  bool
	// Servers are tried in order: each reconnect moves on to the next one.
	Servers []Server `gorm:"serializer:secretservers"`

	Nick     string
	AltNick  string // tried when Nick is taken; empty = the user's default
	Username string // ident: the user part of nick!user@host
	Realname string
	QuitMsg  string // empty = the user's default
	// BindHost is the local address (or a hostname for one) to connect
	// from; empty = the user's default, then the server's policy.
	BindHost string

	// Password is the SASL PLAIN password; SASLUser the account name (empty =
	// the ident, then the nick). Without SASL support it's sent as PASS.
	Password string `gorm:"serializer:secret"`
	SASLUser string

	// How evrd logs in to the network's services (docs/services.md). The
	// zero values are what evrd always did: SASL when offered, else
	// IDENTIFY to NickServ once connected.
	AuthMethod   string // see Auth
	ServicesNick string // where IDENTIFY goes: "" = NickServ ("NickServ@services.dal.net", "NS")
	// IdentifyArgs: IdentifyAccount sends IDENTIFY account password,
	// IdentifyPassword just the password; "" names the account only when
	// it isn't the nick in use.
	IdentifyArgs string
	IdentifyOn   string // "" (once connected) or IdentifyOnPrompt (when services ask)
	// AuthCommand is AuthCommand's line (raw IRC or /msg …), with %nick%,
	// %account% and %password% (a template: the password stays in Password).
	AuthCommand string `gorm:"serializer:secret"`
	// GhostCmd frees the nick when it's taken: "", "ghost", "regain" or
	// "recover", sent to the services nick.
	GhostCmd string
	// JoinAfterAuth holds the channel joins until services confirm the
	// login (or AuthTimeout seconds pass; 0 = DefaultAuthTimeout).
	JoinAfterAuth bool
	AuthTimeout   int

	// Server certificates: accepted if the SHA-256 fingerprint is listed, or
	// TrustAllCerts is set, or (TrustPKI) the chain verifies for the host.
	TrustAllCerts bool
	TrustPKI      bool     `gorm:"not null;default:true"`
	Fingerprints  []string `gorm:"serializer:json"` // lowercase hex, no colons

	// Encoding is a charset name for networks that don't speak UTF-8.
	// "^name" only decodes incoming text that isn't valid UTF-8 and sends
	// UTF-8; "name" decodes and sends in that charset.
	Encoding string

	// FloodRate is lines per second sent once FloodBurst lines have gone out
	// at once; 0 turns flood protection off.
	FloodRate  float64 `gorm:"not null;default:2"`
	FloodBurst int     `gorm:"not null;default:9"`
	JoinDelay  int     // seconds to wait after connecting before joining

	Channels        []string                   `gorm:"serializer:json"`
	ChannelSettings map[string]ChannelSettings `gorm:"serializer:json"` // by lowercased name
	Enabled         bool

	// Modules are the network's modules (internal/modules) by name: on or
	// off, and their settings.
	Modules map[string]ModuleConfig `gorm:"serializer:secretjson"` // encrypted like passwords
}

// ModuleConfig is one module on a network.
type ModuleConfig struct {
	Enabled  bool              `json:"enabled"`
	Settings map[string]string `json:"settings,omitempty"`
}

// Server is one of a network's servers.
type Server struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      bool   `json:"tls"`
	Password string `json:"password,omitempty"` // sent as PASS
}

// ChannelSettings are per-channel options.
type ChannelSettings struct {
	Key string `json:"key,omitempty"`
	// Detached channels stay joined but are hidden from IRC clients until
	// they /join them again.
	Detached bool `json:"detached,omitempty"`
	// Disabled channels keep their settings but aren't joined on connect.
	Disabled bool `json:"disabled,omitempty"`
}

// Services login methods (Network.AuthMethod).
const (
	AuthAuto     = "auto"     // SASL PLAIN when offered, else IDENTIFY (and PASS)
	AuthSASL     = "sasl"     // SASL PLAIN only
	AuthNickServ = "nickserv" // IDENTIFY to the services nick once connected
	AuthCommand  = "command"  // the user's own line once connected (Undernet's X…)
	AuthNone     = "none"     // no login (or a module's, like q)

	IdentifyAccount  = "account"  // IDENTIFY account password (Atheme, Anope)
	IdentifyPassword = "password" // IDENTIFY password (older services)
	IdentifyOnPrompt = "prompt"   // identify when services ask

	DefaultAuthTimeout = 15 // seconds to wait for services to answer
)

// Auth is the network's login method: AuthAuto for rows saved before there
// was a choice.
func (n Network) Auth() string {
	if n.AuthMethod == "" {
		return AuthAuto
	}
	return n.AuthMethod
}

// ServerList is the network's servers, falling back to Host/Port/TLS for
// rows saved before Servers existed.
func (n Network) ServerList() []Server {
	if len(n.Servers) > 0 {
		return n.Servers
	}
	if n.Host == "" {
		return nil
	}
	return []Server{{Host: n.Host, Port: n.Port, TLS: n.TLS}}
}

// Channel returns a channel's settings.
func (n Network) Channel(name string) ChannelSettings {
	return n.ChannelSettings[strings.ToLower(name)]
}

// ReadMarker is how far a user has read a buffer.
type ReadMarker struct {
	NetworkID int64  `gorm:"primaryKey;autoIncrement:false"`
	Buffer    string `gorm:"primaryKey;size:255"` // lowercased
	ReadAt    time.Time
}

// BufferPlayback is a buffer's playback settings and how far it's been
// cleared, like ZNC's per-channel buffers. Buffer "" holds the network's own:
// its default line count, and when an IRC client was last on it.
type BufferPlayback struct {
	NetworkID int64  `gorm:"primaryKey;autoIncrement:false"`
	Buffer    string `gorm:"primaryKey;size:255"` // lowercased
	// Lines is how many lines to play back (nil: the network's, then the
	// account's; 0: none).
	Lines *int
	// AutoClear plays only what's new since an IRC client was last on the
	// network (nil: the account's setting for channels or queries).
	AutoClear *bool
	// ClearedAt hides older lines from playback (ClearBuffer).
	ClearedAt *time.Time
}

// UserEvent is an entry in a user's activity feed (network lost, netsplit,
// client attached…).
type UserEvent struct {
	ID     int64     `gorm:"primaryKey"`
	UserID int64     `gorm:"index:idx_user_event,priority:1;not null"`
	At     time.Time `gorm:"index:idx_user_event,priority:2"`
	// NetworkID is the network it's about (0: none, or recorded before
	// events carried it).
	NetworkID int64 `gorm:"not null;default:0"`
	Kind      string
	Title     string
	Detail    string
}

// Node is an evrd process in the cluster, as it advertises itself.
type Node struct {
	ID string `gorm:"primaryKey;size:128"`
	// HTTPURL and IRCAddr are the node's internal endpoints: where other
	// nodes forward a user's web requests and IRC clients.
	HTTPURL     string
	IRCAddr     string
	StartedAt   time.Time
	HeartbeatAt time.Time `gorm:"index"`
}

// Setting is a server setting changed in the admin UI: a config directive
// and its value, shared by every node (see config.Specs).
type Setting struct {
	Key       string `gorm:"primaryKey;size:64"`
	Value     string `gorm:"serializer:secret"` // encrypted like other secrets (client secrets live here)
	UpdatedAt time.Time
	UpdatedBy string
}

// CertCacheEntry is a TLS certificate and key evrd keeps for itself: Let's
// Encrypt's (the ACME client's cache) or the self-signed one, shared by
// every node and encrypted like other secrets.
type CertCacheEntry struct {
	Key       string `gorm:"primaryKey;size:255"`
	Data      string `gorm:"serializer:secret"`
	UpdatedAt time.Time
}

// CacheEntry is a document evrd fetched and keeps for when its source is
// out of reach: the network directory (internal/directory). Shared by every
// node, so a restart or a new node starts with the last good copy.
type CacheEntry struct {
	Key       string `gorm:"primaryKey;size:255"`
	Data      string
	UpdatedAt time.Time
}

// LanguagePack is a translation of the web app an admin uploaded: a new
// language, or strings that replace a built-in one's. Shared by every node.
type LanguagePack struct {
	Code string `gorm:"primaryKey;size:16"`
	Name string
	// Messages is the JSON object of "area.key": text (or plural forms).
	Messages  string
	UpdatedAt time.Time
	UpdatedBy string
}

// UserLanguagePack is a user's own translation, applied only for them on
// top of the server's.
type UserLanguagePack struct {
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Code      string `gorm:"size:16;not null"`
	Name      string
	Messages  string
	UpdatedAt time.Time
}

// NodeConf is a node's last attempt to write the settings into its config
// file (settings_write_conf).
type NodeConf struct {
	NodeID  string `gorm:"primaryKey;size:128"`
	Path    string
	WroteAt time.Time
	Error   string
	// Keys are the settings last written, so a node that was down when one
	// was reset still removes it from its file.
	Keys []string `gorm:"serializer:json"`
}

// UserLease says which node owns a user's live state (their upstream
// connections, attached clients and web chat sockets) until ExpiresAt. Epoch
// grows each time the lease changes hands.
type UserLease struct {
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	NodeID    string `gorm:"index;size:128;not null"`
	Epoch     int64  `gorm:"not null"`
	ExpiresAt time.Time
}

// LegacyMessage is a row of the messages table that held chat history before
// it moved to internal/history. Only read, once, to migrate it.
type LegacyMessage struct {
	ID        int64
	NetworkID int64
	Target    string
	Sender    string
	Kind      string
	Text      string
	Extra     string
	Time      time.Time
}
