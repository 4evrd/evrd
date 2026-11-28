package client

// Certificate sign-in (certauth, as ZNC's module): an IRC client that
// presents one of its user's TLS client certificates is signed in as that
// user without a password, with SASL EXTERNAL or with no password at all.
// Then the network is the one PASS (user/network, no password) or USER
// (user/network as its user name) names, or BOUNCER BIND's, or the first.
//
// The ircs listeners ask for a certificate but don't require one, and
// don't check who signed it: self-signed is the point. TLS proves the
// client holds its key. A certificate is known by the SHA-256 of the whole
// certificate (certfp, as Libera.Chat and OFTC show it) or of its public
// key, which outlasts renewing the certificate with the same key.
//
// A certificate counts like an IRC client password: a per-device secret
// that never leaves the device, revoked on its own. It needs a second
// factor only where client passwords do (irc_mfa_client_passwords).
// Managed mode has none of this: its accounts sign in with the managing
// service's tokens.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/irctls"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/telemetry"
)

// certauthNick is ZNC's certauth module's: /msg *certauth add.
const certauthNick = "*certauth"

// certFormat is how a sign-in by certificate shows among the admin's IRC
// sign-ins (store.IRCSignIn's Format).
const certFormat = "certificate"

// ClientCerts is users' client certificates (accounts.ClientCerts).
type ClientCerts = accounts.ClientCertServiceImpl

// SetClientCerts signs IRC clients in by their certificates, kept in c.
func (s *Server) SetClientCerts(c ClientCerts) { s.certs = c }

// requestClientCerts is conf asking IRC clients for a certificate: any
// one, or none.
func requestClientCerts(conf *tls.Config) *tls.Config {
	conf = conf.Clone()
	conf.ClientAuth = tls.RequestClientCert
	return conf
}

// peerCert is a client certificate's fingerprints (SHA-256, lowercase
// hex): the certificate's and its public key's. Zero: no certificate.
type peerCert struct{ cert, key string }

func peerCertOf(c *x509.Certificate) peerCert {
	return peerCert{cert: irctls.Fingerprint(c), key: irctls.KeyFingerprint(c)}
}

// param is the certificate as EVRD-PROXY passes it on: "cert,key", or "-".
func (p peerCert) param() string {
	if p.cert == "" {
		return "-"
	}
	return p.cert + "," + p.key
}

func parsePeerCert(param string) *peerCert {
	cert, key, _ := strings.Cut(param, ",")
	_, ok1 := accounts.ParseFingerprint(cert)
	_, ok2 := accounts.ParseFingerprint(key)
	if !ok1 || !ok2 {
		return nil
	}
	return &peerCert{cert: cert, key: key}
}

// certState is a session's certificate sign-in.
type certState struct {
	forwarded *peerCert  // the certificate the node that forwarded the client saw
	external  bool       // SASL EXTERNAL signed the client in
	refused   bool       // SASL EXTERNAL refused it: no sign-in by certificate then
	login     *certLogin // attach signs the client in by its certificate
}

// certLogin is a sign-in by certificate: whose, on which device and
// network — or the numeric to refuse it with, why, and the reason logged.
type certLogin struct {
	u                 store.User
	device            string
	network           string
	code, why, reason string
}

// certsOn reports whether clients can sign in with certificates.
func (se *session) certsOn() bool { return se.srv.certs != nil && !se.isManaged() }

// peerCert is the certificate the client sent, if any.
func (se *session) peerCert() peerCert {
	if se.cert.forwarded != nil {
		return *se.cert.forwarded
	}
	switch c := se.conn.(type) {
	case *tls.Conn:
		if certs := c.ConnectionState().PeerCertificates; len(certs) > 0 {
			return peerCertOf(certs[0])
		}
	case *links.ClientConn:
		return peerCert{cert: c.Info.CertFP, key: c.Info.KeyFP}
	}
	return peerCert{}
}

// certDevice is how a certificate sign-in shows among the user's clients:
// the certificate's label, or the start of its fingerprint.
func certDevice(c store.ClientCert) string {
	if c.Label != "" {
		return c.Label
	}
	return "Certificate " + c.Fingerprint[:min(8, len(c.Fingerprint))]
}

// userLogin is USER's user name when it's a bouncer login (user/network):
// what the client names when it sends no PASS.
func (se *session) userLogin() (username, network string) {
	u := strings.TrimPrefix(se.user, "~")
	if !strings.Contains(u, "/") {
		return "", "" // just an ident
	}
	username, network, _ = cutLogin(u)
	return username, network
}

// certUser is the account the client's certificate is on (username: the
// one it named, "" for any).
func (se *session) certUser(username string) (store.User, store.ClientCert, error) {
	pc := se.peerCert()
	if !se.certsOn() || pc.cert == "" {
		return store.User{}, store.ClientCert{}, accounts.ErrInvalidCredentials
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return se.srv.certs.CertUser(ctx, username, pc.cert, pc.key)
}

// certOwner is whose account the client's certificate is on, for a client
// that names none (to forward it to the node that owns the account).
func (se *session) certOwner() (store.User, bool) {
	if se.cert.refused {
		return store.User{}, false
	}
	username, _ := se.userLogin()
	u, _, err := se.certUser(username)
	return u, err == nil
}

// prepareCertLogin decides, before attach checks anything else, whether
// the client signs in by its certificate: it sent no password (hasPassword)
// but a certificate on the account it names in login (or PASS was a
// password alone: passwordOnly), or SASL EXTERNAL signed it in. A
// certificate no account has is said in a notice, and the sign-in carries
// on as without one.
func (se *session) prepareCertLogin(login string, hasPassword, passwordOnly bool) {
	se.cert.login = nil
	if hasPassword || !se.certsOn() || se.cert.refused {
		return
	}
	username, network, _ := cutLogin(login)
	if passwordOnly {
		username, network = "", ""
	}
	if username == "" {
		username, network = se.userLogin()
	} else if network == "" {
		_, network = se.userLogin()
	}
	if se.cert.external {
		se.cert.login = &certLogin{u: se.saslUser, device: se.saslDevice, network: network}
		return
	}
	pc := se.peerCert()
	if pc.cert == "" {
		return
	}
	u, c, err := se.certUser(username)
	switch {
	case err == nil:
		se.cert.login = &certLogin{u: u, device: certDevice(c), network: network}
	case errors.Is(err, accounts.ErrCertAmbiguous):
		se.cert.login = &certLogin{code: "464", why: se.tr("certauth.ambiguous"), reason: "cert_ambiguous"}
	case errors.Is(err, accounts.ErrSuspended):
		se.cert.login = &certLogin{code: "465", why: "This account is suspended", reason: "suspended"}
	case errors.Is(err, accounts.ErrInvalidCredentials):
		se.send(fmt.Sprintf(":%s NOTICE * :%s", se.serverName(), ircText(se.tr("certauth.unknown", "fingerprint", pc.cert))))
	default:
		se.srv.log.Warn("irc sign-in by certificate", "err", err)
	}
}

// certAttach signs the client in as prepareCertLogin decided.
func (se *session) certAttach() bool {
	l := se.cert.login
	if l.code != "" {
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result(l.reason))
		se.loginFailed("", "", l.reason)
		se.refusal = l.reason
		se.numeric(l.code, ":"+ircText(l.why))
		return false
	}
	se.srv.log.Info("irc sign-in with a certificate", "user", l.u.Username, "device", l.device, "sasl", se.cert.external, "ip", se.clientIP())
	return se.attachAs(l.u, l.u.Username, l.network, l.device, "")
}

// saslExternal finishes SASL EXTERNAL: authzid is the account (or
// account/network) the client wants, "" for whichever its certificate is
// on.
func (se *session) saslExternal(authzid string, reply func(code, text string)) {
	username, network, _ := cutLogin(authzid)
	u, c, err := se.certUser(username)
	if err != nil {
		why, reason := se.tr("certauth.unknown", "fingerprint", se.peerCert().cert), "invalid"
		switch {
		case errors.Is(err, accounts.ErrCertAmbiguous):
			why, reason = se.tr("certauth.ambiguous"), "cert_ambiguous"
		case errors.Is(err, accounts.ErrSuspended):
			why, reason = "This account is suspended", "suspended"
		}
		telemetry.IRCLogins.Add(context.Background(), 1, telemetry.Result(reason))
		se.loginFailed(username, network, reason)
		se.cert.refused = true // not another account by the same certificate
		reply("904", ":"+ircText(why))
		return
	}
	se.saslUser, se.saslDevice, se.saslOTP = u, certDevice(c), ""
	se.cert.external, se.cert.refused = true, false
	// as with PLAIN, attach (here, or on the node this client is forwarded
	// to, which sees the certificate too) takes the account from PASS
	login := u.Username
	if network != "" {
		login += "/" + network
	}
	se.pass = login
	se.regLines = append([]string{"PASS " + login}, dropPASS(se.regLines)...)
	nick := orElse(se.nick, "*")
	reply("900", fmt.Sprintf("%s!%s@%s %s :You are now logged in as %s", nick, orElse(se.user, nick), se.serverName(), u.Username, u.Username))
	reply("903", ":SASL authentication successful")
}

// --- *status: addcert, listcerts, delcert; and ZNC's *certauth ---

// certauthCommand is /msg *certauth <command>, as ZNC's certauth module
// takes it: add [key], list, del <n>, show.
func (se *session) certauthCommand(text string) {
	se.pr = nil
	se.replyAs = certauthNick
	defer func() { se.replyAs = "" }()
	fields := strings.Fields(text)
	cmd := ""
	if len(fields) > 0 {
		cmd = strings.ToLower(fields[0])
	}
	switch cmd {
	case "add":
		se.cmdAddCert(fields[1:])
	case "del", "delete", "remove":
		se.cmdDelCert(fields[1:])
	case "list", "show":
		se.cmdListCerts(fields[1:])
	default:
		se.say("certs.help", "status", statusNick)
	}
}

// myCerts is the user's certificates; false (said why) if there are none
// to have.
func (se *session) myCerts() ([]store.ClientCert, bool) {
	if !se.certsOn() {
		se.say("certs.off")
		return nil, false
	}
	list, err := se.srv.certs.ListClientCerts(context.Background(), se.uid)
	if err != nil {
		se.sayErr(err)
		return nil, false
	}
	return list, true
}

func (se *session) cmdListCerts([]string) {
	list, ok := se.myCerts()
	if !ok {
		return
	}
	if len(list) == 0 {
		se.say("certs.none")
	}
	for i, c := range list {
		if c.Label != "" {
			se.say("certs.itemLabel", "number", i+1, "fingerprint", c.Fingerprint, "label", c.Label)
		} else {
			se.say("certs.item", "number", i+1, "fingerprint", c.Fingerprint)
		}
	}
	if se.web != nil {
		return // no client certificate on the web
	}
	if pc := se.peerCert(); pc.cert != "" {
		se.say("certs.current", "fingerprint", pc.cert)
	} else {
		se.say("certs.noCurrent")
	}
}

// cmdAddCert adds the certificate args name (a fingerprint, then an
// optional label), or this client's (args just a label, if any).
func (se *session) cmdAddCert(args []string) {
	if _, ok := se.myCerts(); !ok {
		return
	}
	fp, label := "", strings.Join(args, " ")
	if len(args) > 0 {
		if f, ok := accounts.ParseFingerprint(args[0]); ok {
			fp, label = f, strings.Join(args[1:], " ")
		}
	}
	if fp == "" {
		fp = se.peerCert().cert
		if se.web != nil {
			fp = ""
		}
	}
	if fp == "" {
		if len(args) > 0 {
			fp = args[0] // not a fingerprint: AddClientCert says so
			label = strings.Join(args[1:], " ")
		} else {
			se.say("certs.needCert")
			return
		}
	}
	c, err := se.srv.certs.AddClientCert(context.Background(), se.uid, fp, label)
	if err != nil {
		se.sayErr(err)
		return
	}
	se.say("certs.added", "fingerprint", c.Fingerprint, "user", se.account().Username)
}

// cmdDelCert removes a certificate by its number in listcerts or its
// fingerprint.
func (se *session) cmdDelCert(args []string) {
	if len(args) != 1 {
		se.say("usage.delcert")
		return
	}
	list, ok := se.myCerts()
	if !ok {
		return
	}
	i := -1
	if n, err := strconv.Atoi(args[0]); err == nil && n >= 1 && n <= len(list) {
		i = n - 1
	} else if fp, ok := accounts.ParseFingerprint(args[0]); ok {
		for j, c := range list {
			if c.Fingerprint == fp {
				i = j
			}
		}
	}
	if i < 0 {
		se.say("certs.notFound")
		return
	}
	if err := se.srv.certs.DeleteClientCert(context.Background(), se.uid, list[i].ID); err != nil {
		se.sayErr(err)
		return
	}
	se.say("certs.removed", "fingerprint", list[i].Fingerprint)
}
