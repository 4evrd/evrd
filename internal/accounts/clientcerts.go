package accounts

// Client certificates that sign users in to IRC without a password
// (certauth, as ZNC's module of that name): an IRC client that presents
// one of its user's certificates over TLS is that user. A certificate is
// known by its SHA-256 fingerprint, of the whole certificate (certfp, as
// Libera.Chat and OFTC show it) or of its public key; a client's
// certificate matches either.

import (
	"context"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/4evrd/evrd/internal/store"
)

// MaxClientCerts is how many client certificates an account may have.
const MaxClientCerts = 32

// ErrCertAmbiguous is a certificate on more than one account, the client
// not saying which it wants.
var ErrCertAmbiguous = errors.New("accounts: the certificate is on more than one account")

// ClientCertServiceImpl is users' client certificates, for the web API,
// *status and the IRC listener's sign-in.
type ClientCertServiceImpl interface {
	ListClientCerts(ctx context.Context, userID int64) ([]store.ClientCert, error)
	// AddClientCert adds a certificate by its fingerprint (hex, colons and
	// spaces allowed), with an optional label.
	AddClientCert(ctx context.Context, userID int64, fingerprint, label string) (store.ClientCert, error)
	DeleteClientCert(ctx context.Context, userID, id int64) error
	// CertUser is the account a client's certificate signs in to, and
	// which of its certificates it is: fingerprints are the client's
	// certificate's (and its key's), username the account the client
	// named ("" for whichever has it). ErrInvalidCredentials if none has
	// it, ErrCertAmbiguous if more than one does and username doesn't say
	// which, ErrSuspended for a suspended account.
	CertUser(ctx context.Context, username string, fingerprints ...string) (store.User, store.ClientCert, error)
}

// ClientCerts applies the rules to the client certificates.
type ClientCerts struct {
	users store.UserRepositoryImpl
	certs store.ClientCertRepositoryImpl
	now   func() time.Time
}

// NewClientCerts is the client certificate service on its repositories.
func NewClientCerts(users store.UserRepositoryImpl, certs store.ClientCertRepositoryImpl) *ClientCerts {
	return &ClientCerts{users: users, certs: certs, now: time.Now}
}

// ParseFingerprint reads a SHA-256 fingerprint as people paste it: hex,
// any case, with or without colons or spaces (AB:CD:…, as openssl and ZNC
// show it, or abcd…, as Libera.Chat's NickServ), even openssl's whole line
// (sha256 Fingerprint=AB:CD:…). It comes back lowercase hex; false if it
// isn't 32 bytes of hex.
func ParseFingerprint(s string) (string, bool) {
	if i := strings.LastIndexByte(s, '='); i >= 0 {
		s = s[i+1:]
	}
	s = NormalizeFingerprint(s)
	if b, err := hex.DecodeString(s); err != nil || len(b) != 32 {
		return "", false
	}
	return s, true
}

func (s *ClientCerts) ListClientCerts(ctx context.Context, userID int64) ([]store.ClientCert, error) {
	return s.certs.ListClientCerts(ctx, userID)
}

func (s *ClientCerts) AddClientCert(ctx context.Context, userID int64, fingerprint, label string) (store.ClientCert, error) {
	fp, ok := ParseFingerprint(fingerprint)
	if !ok {
		return store.ClientCert{}, invalid("clientCertFingerprint")
	}
	label = strings.TrimSpace(label)
	if utf8.RuneCountInString(label) > 64 {
		return store.ClientCert{}, invalid("clientCertLabel")
	}
	have, err := s.certs.ListClientCerts(ctx, userID)
	if err != nil {
		return store.ClientCert{}, err
	}
	if slices.ContainsFunc(have, func(c store.ClientCert) bool { return c.Fingerprint == fp }) {
		return store.ClientCert{}, invalid("clientCertTaken")
	}
	if len(have) >= MaxClientCerts {
		return store.ClientCert{}, invalid("clientCertLimit", "max", MaxClientCerts)
	}
	return s.certs.CreateClientCert(ctx, store.ClientCert{UserID: userID, Fingerprint: fp, Label: label})
}

func (s *ClientCerts) DeleteClientCert(ctx context.Context, userID, id int64) error {
	return s.certs.DeleteClientCert(ctx, userID, id)
}

func (s *ClientCerts) CertUser(ctx context.Context, username string, fingerprints ...string) (store.User, store.ClientCert, error) {
	var fps []string
	for _, f := range fingerprints {
		if f != "" {
			fps = append(fps, f)
		}
	}
	found, err := s.certs.ClientCertsByFingerprint(ctx, fps)
	if err != nil {
		return store.User{}, store.ClientCert{}, err
	}
	// one certificate per account: the first that matched
	var mine []store.ClientCert
	for _, c := range found {
		if !slices.ContainsFunc(mine, func(m store.ClientCert) bool { return m.UserID == c.UserID }) {
			mine = append(mine, c)
		}
	}
	var u store.User
	var cert store.ClientCert
	switch {
	case len(mine) == 0:
		return store.User{}, store.ClientCert{}, ErrInvalidCredentials
	case username != "":
		u, err = s.users.UserByUsername(ctx, strings.ToLower(username))
		if err != nil {
			return store.User{}, store.ClientCert{}, ErrInvalidCredentials
		}
		i := slices.IndexFunc(mine, func(c store.ClientCert) bool { return c.UserID == u.ID })
		if i < 0 {
			return store.User{}, store.ClientCert{}, ErrInvalidCredentials
		}
		cert = mine[i]
	case len(mine) > 1:
		// Someone may have added another's certificate (a fingerprint is
		// no secret): never guess whose account the client meant.
		return store.User{}, store.ClientCert{}, ErrCertAmbiguous
	default:
		cert = mine[0]
		if u, err = s.users.UserByID(ctx, cert.UserID); err != nil {
			return store.User{}, store.ClientCert{}, ErrInvalidCredentials
		}
	}
	if u.Suspended {
		return store.User{}, store.ClientCert{}, ErrSuspended
	}
	_ = s.certs.TouchClientCert(ctx, cert.ID, s.now())
	return u, cert, nil
}
