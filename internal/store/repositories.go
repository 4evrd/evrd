package store

// The repositories: data access, one interface per kind of record. Every
// method takes the caller's context, which reaches the database query.

import (
	"context"
	"time"
)

// UserRepositoryImpl keeps users: accounts, their settings and when they
// were last seen.
type UserRepositoryImpl interface {
	CreateUser(ctx context.Context, u User) (User, error)
	UserByID(ctx context.Context, id int64) (User, error)
	UserByUsername(ctx context.Context, username string) (User, error)
	UserBySubject(ctx context.Context, subject string) (User, error)
	ListUsers(ctx context.Context) ([]User, error)
	UpdateUser(ctx context.Context, u User) error
	// DeleteUser removes the user and everything they own: networks, their
	// history, sessions, client passwords and their language pack.
	DeleteUser(ctx context.Context, id int64) error
	// CountAdmins counts admins who are not suspended.
	CountAdmins(ctx context.Context) (int64, error)
	TouchUserSeen(ctx context.Context, userID int64, at time.Time) error
}

// SessionRepositoryImpl keeps web sign-in sessions.
type SessionRepositoryImpl interface {
	CreateSession(ctx context.Context, s Session) error
	SessionByID(ctx context.Context, id string) (Session, error)
	TouchSession(ctx context.Context, id string, at time.Time) error
	// RenewSession is TouchSession that also moves the session's end.
	RenewSession(ctx context.Context, id string, at, expires time.Time) error
	ListSessions(ctx context.Context, userID int64) ([]Session, error)
	DeleteSession(ctx context.Context, userID int64, id string) error
	// DeleteUserSessions signs a user out everywhere except keep ("" = all).
	DeleteUserSessions(ctx context.Context, userID int64, keep string) error
	DeleteExpiredSessions(ctx context.Context, now time.Time) error
}

// ClientPasswordRepositoryImpl keeps IRC client passwords.
type ClientPasswordRepositoryImpl interface {
	CreateClientPassword(ctx context.Context, c ClientPassword) (ClientPassword, error)
	ListClientPasswords(ctx context.Context, userID int64) ([]ClientPassword, error)
	DeleteClientPassword(ctx context.Context, userID, id int64) error
	TouchClientPassword(ctx context.Context, id int64, at time.Time) error
}

// NetworkRepositoryImpl keeps users' IRC networks and their channels.
type NetworkRepositoryImpl interface {
	ListNetworks(ctx context.Context, userID int64) ([]Network, error)
	CountNetworks(ctx context.Context, userID int64) (int64, error)
	// NetworkCounts is the number of networks per user ID.
	NetworkCounts(ctx context.Context) (map[int64]int64, error)
	AllNetworks(ctx context.Context) ([]Network, error)
	AllEnabledNetworks(ctx context.Context) ([]Network, error)
	NetworkByID(ctx context.Context, id int64) (Network, error)
	CreateNetwork(ctx context.Context, n Network) (Network, error)
	DeleteNetwork(ctx context.Context, userID, id int64) error
	SetNetworkEnabled(ctx context.Context, id int64, enabled bool) error
	// UpdateNetwork saves a network's editable settings (not Enabled).
	UpdateNetwork(ctx context.Context, n Network) error
	SetNetworkChannelSettings(ctx context.Context, id int64, settings map[string]ChannelSettings) error
	SetNetworkChannels(ctx context.Context, id int64, channels []string) error
}

// ReadMarkerRepositoryImpl keeps how far each buffer is read.
type ReadMarkerRepositoryImpl interface {
	SetReadMarker(ctx context.Context, networkID int64, buffer string, at time.Time) error
	// ReadMarkers is how far each of a network's buffers is read (by
	// lowercased buffer name).
	ReadMarkers(ctx context.Context, networkID int64) (map[string]time.Time, error)
}

// BufferPlaybackRepositoryImpl keeps per-buffer playback settings.
type BufferPlaybackRepositoryImpl interface {
	// BufferPlaybacks is a network's playback settings, by lowercased
	// buffer name ("" for the network's own).
	BufferPlaybacks(ctx context.Context, networkID int64) (map[string]BufferPlayback, error)
	// UpdateBufferPlayback changes the given buffers' playback settings (a
	// buffer back to all defaults is dropped).
	UpdateBufferPlayback(ctx context.Context, networkID int64, buffers []string, f func(*BufferPlayback)) error
}

// UserEventRepositoryImpl keeps users' activity feeds.
type UserEventRepositoryImpl interface {
	AddUserEvent(ctx context.Context, e UserEvent) error
	// UserEvents lists a user's newest events since since, newest first.
	UserEvents(ctx context.Context, userID int64, since time.Time, limit int) ([]UserEvent, error)
	// PruneUserEvents drops events older than before.
	PruneUserEvents(ctx context.Context, before time.Time) error
}

// ClusterRepositoryImpl keeps cluster nodes, their settings and users'
// leases.
type ClusterRepositoryImpl interface {
	UpsertNode(ctx context.Context, n Node) error
	// DeleteNodesBefore drops nodes last heard from before t.
	DeleteNodesBefore(ctx context.Context, t time.Time) error
	// LiveNodes lists nodes that sent a heartbeat after since.
	LiveNodes(ctx context.Context, since time.Time) ([]Node, error)
	SetNodeConf(ctx context.Context, c NodeConf) error
	NodeConfs(ctx context.Context) ([]NodeConf, error)
	// AcquireLease takes a user's lease for node until until, if it's free
	// (missing or expired at now) or already node's. Reports whether node
	// holds it afterwards.
	AcquireLease(ctx context.Context, userID int64, node string, until, now time.Time) (bool, error)
	// RenewLeases extends every lease node still holds (unexpired at now).
	RenewLeases(ctx context.Context, node string, until, now time.Time) error
	// HeldLeases lists the users whose unexpired lease node holds.
	HeldLeases(ctx context.Context, node string, now time.Time) ([]int64, error)
	// ReleaseLease expires node's lease on a user so another node can take it.
	ReleaseLease(ctx context.Context, userID int64, node string, now time.Time) error
	// Leases lists every unexpired lease.
	Leases(ctx context.Context, now time.Time) ([]UserLease, error)
	// LeaseOwner is the node holding a user's unexpired lease, if any.
	LeaseOwner(ctx context.Context, userID int64, now time.Time) (Node, UserLease, bool, error)
	// DeleteOrphanLeases drops leases of users that no longer exist.
	DeleteOrphanLeases(ctx context.Context) error
}

// SettingRepositoryImpl keeps settings changed in the admin UI. SaveSettings
// sets and deletes keys in one go.
type SettingRepositoryImpl interface {
	Settings(ctx context.Context) ([]Setting, error)
	SaveSettings(ctx context.Context, set map[string]string, del []string, by string) error
}

// CacheRepositoryImpl keeps TLS certificates evrd gets or makes (see
// CertCacheEntry) and fetched documents (see CacheEntry). CertCache and
// Cached return ErrNotFound for a missing key.
type CacheRepositoryImpl interface {
	CertCache(ctx context.Context, key string) (string, error)
	PutCertCache(ctx context.Context, key, data string) error
	DeleteCertCache(ctx context.Context, key string) error
	Cached(ctx context.Context, key string) (string, error)
	PutCached(ctx context.Context, key, data string) error
}

// LanguagePackRepositoryImpl keeps translations of the web app (see
// LanguagePack). LanguagePacks leaves out the messages; LanguagePack and
// UserLanguagePack return ErrNotFound when there's none.
type LanguagePackRepositoryImpl interface {
	LanguagePacks(ctx context.Context) ([]LanguagePack, error)
	LanguagePack(ctx context.Context, code string) (LanguagePack, error)
	PutLanguagePack(ctx context.Context, p LanguagePack) error
	DeleteLanguagePack(ctx context.Context, code string) error
	UserLanguagePack(ctx context.Context, userID int64) (UserLanguagePack, error)
	PutUserLanguagePack(ctx context.Context, p UserLanguagePack) error
	DeleteUserLanguagePack(ctx context.Context, userID int64) error
}

// MFARepositoryImpl keeps second factors: a user's authenticator app and
// recovery codes. TOTP returns ErrNotFound when there's none.
type MFARepositoryImpl interface {
	TOTP(ctx context.Context, userID int64) (UserTOTP, error)
	PutTOTP(ctx context.Context, t UserTOTP) error
	DeleteTOTP(ctx context.Context, userID int64) error
	// UseTOTPStep records step as used if it's later than the last one,
	// reporting whether it was (false: the code was used already).
	UseTOTPStep(ctx context.Context, userID, step int64) (bool, error)
	RecoveryCodes(ctx context.Context, userID int64) ([]RecoveryCode, error)
	// ReplaceRecoveryCodes drops the user's codes and keeps these hashes.
	ReplaceRecoveryCodes(ctx context.Context, userID int64, hashes []string) error
	// UseRecoveryCode marks an unused code used, reporting whether it was
	// unused.
	UseRecoveryCode(ctx context.Context, id int64, at time.Time) (bool, error)
}

// PendingSignInRepositoryImpl keeps IRC sign-ins waiting on the website.
// PendingSignIn* return ErrNotFound when there's none.
type PendingSignInRepositoryImpl interface {
	CreatePendingSignIn(ctx context.Context, p PendingSignIn) error
	PendingSignIn(ctx context.Context, id string) (PendingSignIn, error)
	PendingSignInByToken(ctx context.Context, tokenHash string) (PendingSignIn, error)
	// DecidePendingSignIn sets a pending one's state (once: false when it
	// was decided already or is gone).
	DecidePendingSignIn(ctx context.Context, id, state string, userID int64, device string) (bool, error)
	DeletePendingSignIn(ctx context.Context, id string) error
	// CountPendingSignIns is how many undecided ones from ip, and for
	// username, are still open at now.
	CountPendingSignIns(ctx context.Context, ip, username string, now time.Time) (byIP, byUser int64, err error)
	DeleteExpiredSignIns(ctx context.Context, now time.Time) error
}

// DevicePositionRepositoryImpl keeps how far each device has been given each
// network's lines. DevicePosition's ok is false when there's none.
type DevicePositionRepositoryImpl interface {
	DevicePosition(ctx context.Context, networkID int64, deviceKey string) (at time.Time, ok bool, err error)
	SetDevicePosition(ctx context.Context, networkID int64, deviceKey string, at time.Time) error
}

// ClientDisconnectRepositoryImpl keeps IRC client disconnects.
type ClientDisconnectRepositoryImpl interface {
	// AddClientDisconnect records d, and marks the same device's last
	// disconnect before d.ConnectedAt (and after since) as reconnected then,
	// returning how long the device was gone in between (0: none found).
	AddClientDisconnect(ctx context.Context, d ClientDisconnect, since time.Time) (time.Duration, error)
	// ClientDisconnects lists disconnects that ended after since, newest
	// first: a user's, or everyone's for userID 0.
	ClientDisconnects(ctx context.Context, userID int64, since time.Time) ([]ClientDisconnect, error)
	DeleteClientDisconnectsBefore(ctx context.Context, before time.Time) error
}

// IRCSignInRepositoryImpl keeps IRC sign-ins (IRCSignIn).
type IRCSignInRepositoryImpl interface {
	AddIRCSignIn(ctx context.Context, s IRCSignIn) (int64, error)
	// IRCSignIns lists sign-ins after since, newest first, at most limit: a
	// user's, everyone's for userID 0, or no one's (unknown) for -1.
	IRCSignIns(ctx context.Context, userID int64, since time.Time, limit int) ([]IRCSignIn, error)
	// SettleIRCSignIn sets a sign-in's result, and gives it to userID when
	// nobody was known for it.
	SettleIRCSignIn(ctx context.Context, id, userID int64, result string) error
	DeleteIRCSignInsBefore(ctx context.Context, before time.Time) error
}

// Store is every repository on one database, plus what spans them. It's
// what Open returns; a service takes only the repositories it uses.
// Implementations must be safe for concurrent use.
type Store interface {
	UserRepositoryImpl
	SessionRepositoryImpl
	ClientPasswordRepositoryImpl
	NetworkRepositoryImpl
	ReadMarkerRepositoryImpl
	BufferPlaybackRepositoryImpl
	UserEventRepositoryImpl
	ClusterRepositoryImpl
	SettingRepositoryImpl
	CacheRepositoryImpl
	LanguagePackRepositoryImpl
	MFARepositoryImpl
	PendingSignInRepositoryImpl
	ClientDisconnectRepositoryImpl
	IRCSignInRepositoryImpl
	DevicePositionRepositoryImpl

	// EncryptSecrets re-encrypts network secrets under the current key.
	EncryptSecrets(ctx context.Context) (int, error)
	// MigrateLegacyMessages hands every row of the old messages table to fn,
	// in order, then drops the table. A no-op once the table is gone.
	MigrateLegacyMessages(ctx context.Context, fn func(LegacyMessage) error) (int, error)
	// Ping checks the database is reachable.
	Ping(ctx context.Context) error
	Close() error
}
