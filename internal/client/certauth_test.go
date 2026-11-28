package client

import (
	"bufio"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/irctls"
	"github.com/4evrd/evrd/internal/links"
	"github.com/4evrd/evrd/internal/mfa"
	"github.com/4evrd/evrd/internal/store"
)

// clientCert is a self-signed client certificate, made with key (a new
// one if nil), as `openssl req -x509` makes them.
func clientCert(t *testing.T, key crypto.Signer) (tls.Certificate, *x509.Certificate) {
	t.Helper()
	if key == nil {
		var err error
		if key, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader); err != nil {
			t.Fatal(err)
		}
	}
	serial, _ := rand.Int(rand.Reader, big.NewInt(1<<62))
	tmpl := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "alice"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, leaf
}

// certRig is a rig with sign-in by certificate and an ircs listener.
func certRig(t *testing.T) (*rig, string, *accounts.ClientCerts) {
	t.Helper()
	r := newRig(t)
	certs := accounts.NewClientCerts(r.st, r.st)
	r.srv.SetClientCerts(certs)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	go r.srv.listen(r.ctx, "ircs://"+addr)
	for i := 0; i < 50; i++ {
		if c, err := net.Dial("tcp", addr); err == nil {
			c.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return r, addr, certs
}

// dialTLS connects to addr over TLS with cert (none if nil), without
// registering.
func (r *rig) dialTLS(addr string, cert *tls.Certificate) *ircClient {
	r.t.Helper()
	conf := &tls.Config{InsecureSkipVerify: true}
	if cert != nil {
		conf.Certificates = []tls.Certificate{*cert}
	}
	c, err := tls.Dial("tcp", addr, conf)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { c.Close() })
	return &ircClient{t: r.t, c: c, sc: bufio.NewScanner(c)}
}

// register sends lines (PASS…), then NICK and USER with user.
func (ic *ircClient) register(user string, lines ...string) *ircClient {
	for _, l := range lines {
		ic.send(l)
	}
	ic.send("NICK alice")
	ic.send("USER " + user + " 0 * :Alice")
	return ic
}

// gone waits for the user's clients to go, closing ic.
func (r *rig) gone(ic *ircClient) {
	r.t.Helper()
	ic.c.Close()
	eventually(r.t, "the client is gone", func() bool { return len(r.b.ClientsOf(r.user.ID)) == 0 })
}

func TestCertSignInWithoutPassword(t *testing.T) {
	r, addr, certs := certRig(t)
	cert, leaf := clientCert(t, nil)
	if _, err := certs.AddClientCert(t.Context(), r.user.ID, irctls.ColonHex(irctls.Fingerprint(leaf)), ""); err != nil {
		t.Fatal(err)
	}

	// no PASS at all: the first network
	ic := r.dialTLS(addr, &cert).register("alice")
	ic.expect(" 001 alice_one ")
	ic.expect("JOIN #one")
	var info []string
	for _, c := range r.b.ClientsOf(r.user.ID) {
		info = append(info, c.Device, c.CertFP)
	}
	if len(info) != 2 || info[0] != "Certificate "+irctls.Fingerprint(leaf)[:8] || info[1] != irctls.Fingerprint(leaf) {
		t.Fatalf("clients: %q", info)
	}
	if !strings.Contains(r.logs.String(), "irc sign-in with a certificate") {
		t.Fatalf("not logged: %s", r.logs.String())
	}
	list, _ := certs.ListClientCerts(t.Context(), r.user.ID)
	if list[0].LastUsedAt == nil {
		t.Fatal("not marked used")
	}
	r.gone(ic)

	// USER user/network picks the network, as does PASS user/network
	// without a password
	ic = r.dialTLS(addr, &cert).register("alice/two")
	ic.expect(" 001 alice_two ")
	r.gone(ic)
	ic = r.dialTLS(addr, &cert).register("alice", "PASS alice/two")
	ic.expect(" 001 alice_two ")
	if strings.Contains(ic.expect("JOIN #two"), "NOTICE") {
		t.Fatal("told the PASS format")
	}
	r.gone(ic)

	// with a password, the password is what counts, as before
	ic = r.dialTLS(addr, &cert).register("alice", "PASS alice/two:wrong")
	ic.expect(" 464 alice :Invalid credentials")
	ic = r.dialTLS(addr, &cert).register("alice", "PASS alice/two:alice-password")
	ic.expect(" 001 alice_two ")
	r.gone(ic)

	// a PASS naming someone else's account isn't this certificate's
	if _, err := r.acc.Create(accounts.CreateInput{Username: "bob", Password: "bob-password1"}); err != nil {
		t.Fatal(err)
	}
	ic = r.dialTLS(addr, &cert).register("alice", "PASS bob/two")
	ic.expect("isn't on the account")
	ic.expect(" 464 ")
	// a PASS that names no account is a password on its own: the
	// certificate says whose
	ic = r.dialTLS(addr, &cert).register("alice", "PASS whatever")
	ic.expect(" 001 alice_one ")
}

func TestCertSignInByKey(t *testing.T) {
	r, addr, certs := certRig(t)
	cert, leaf := clientCert(t, nil)
	// the key's fingerprint: a certificate renewed with the same key works
	if _, err := certs.AddClientCert(t.Context(), r.user.ID, irctls.KeyFingerprint(leaf), "laptop"); err != nil {
		t.Fatal(err)
	}
	renewed, _ := clientCert(t, cert.PrivateKey.(crypto.Signer))
	ic := r.dialTLS(addr, &renewed).register("alice")
	ic.expect(" 001 alice_one ")
	eventually(t, "the device is the label", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].Device == "laptop"
	})
}

func TestCertSignInRefusals(t *testing.T) {
	r, addr, certs := certRig(t)
	cert, leaf := clientCert(t, nil)
	fp := irctls.Fingerprint(leaf)

	// a certificate no account has: said, then as without one
	ic := r.dialTLS(addr, &cert).register("alice")
	ic.expect("NOTICE * :The certificate this client sent (SHA-256 " + fp + ") isn't on the account")
	ic.expect(" 464 alice :Password required")
	// no certificate: nothing said
	ic = r.dialTLS(addr, nil).register("alice")
	if line := ic.expect(" 464 "); !strings.Contains(line, "Password required") {
		t.Fatal(line)
	}
	// the password still works with an unknown certificate
	ic = r.dialTLS(addr, &cert).register("alice", "PASS alice:alice-password")
	ic.expect(" 001 alice_one ")
	r.gone(ic)

	// on two accounts: the client has to say whose
	bob, err := r.acc.Create(accounts.CreateInput{Username: "bob", Password: "bob-password1"})
	if err != nil {
		t.Fatal(err)
	}
	r.st.CreateNetwork(context.Background(), store.Network{UserID: bob.ID, Name: "b", Host: "127.0.0.1", Port: 1, Nick: "bob"})
	for _, u := range []int64{r.user.ID, bob.ID} {
		if _, err := certs.AddClientCert(t.Context(), u, fp, ""); err != nil {
			t.Fatal(err)
		}
	}
	ic = r.dialTLS(addr, &cert).register("alice")
	ic.expect(" 464 alice :That certificate is on more than one account")
	ic = r.dialTLS(addr, &cert).register("alice", "PASS alice")
	ic.expect(" 001 alice_one ")
	r.gone(ic)
	ic = r.dialTLS(addr, &cert).register("alice/two")
	ic.expect(" 001 alice_two ")
	r.gone(ic)

	// a suspended account
	u := r.user
	u.Suspended = true
	r.st.UpdateUser(t.Context(), u)
	ic = r.dialTLS(addr, &cert).register("alice", "PASS alice")
	ic.expect(" 465 alice :This account is suspended")
	u.Suspended = false
	r.st.UpdateUser(t.Context(), u)

	// a plain-text connection has no certificate
	ic = r.attach("alice")
	ic.expect(" 464 ")
}

func TestCertSASLExternal(t *testing.T) {
	r, addr, certs := certRig(t)
	cert, leaf := clientCert(t, nil)
	if _, err := certs.AddClientCert(t.Context(), r.user.ID, irctls.Fingerprint(leaf), "phone"); err != nil {
		t.Fatal(err)
	}
	external := func(cert *tls.Certificate, authzid string) *ircClient {
		t.Helper()
		ic := r.dialTLS(addr, cert)
		ic.send("CAP LS 302")
		ic.expect("sasl=PLAIN,EXTERNAL")
		ic.send("CAP REQ :sasl")
		ic.expect("ACK :sasl")
		ic.send("NICK alice")
		ic.send("USER alice 0 * :alice")
		ic.send("AUTHENTICATE EXTERNAL")
		ic.expect("AUTHENTICATE +")
		if authzid == "" {
			ic.send("AUTHENTICATE +")
		} else {
			ic.send("AUTHENTICATE " + base64.StdEncoding.EncodeToString([]byte(authzid)))
		}
		return ic
	}

	ic := external(&cert, "")
	ic.expect(":evrd 900 alice alice!alice@evrd alice :You are now logged in as alice")
	ic.expect(" 903 alice :SASL authentication successful")
	ic.send("CAP END")
	ic.expect(" 001 alice_one ")
	eventually(t, "the device is the label", func() bool {
		cs := r.b.ClientsOf(r.user.ID)
		return len(cs) == 1 && cs[0].Device == "phone"
	})
	r.gone(ic)

	// the authorization identity picks the network…
	ic = external(&cert, "alice/two")
	ic.expect(" 903 ")
	ic.send("CAP END")
	ic.expect(" 001 alice_two ")
	r.gone(ic)
	// …and must be the certificate's account
	ic = external(&cert, "bob")
	ic.expect(" 904 alice :The certificate this client sent")
	ic.send("CAP END")
	ic.expect(" 464 ")

	// an unknown certificate
	other, _ := clientCert(t, nil)
	ic = external(&other, "")
	ic.expect(" 904 alice :The certificate this client sent")

	// no certificate: EXTERNAL isn't offered
	ic = r.dialTLS(addr, nil)
	ic.send("CAP LS 302")
	if line := ic.expect("sasl="); !strings.Contains(line, "sasl=PLAIN ") && !strings.HasSuffix(line, "sasl=PLAIN") {
		t.Fatalf("offered: %s", line)
	}
	ic.send("CAP REQ :sasl")
	ic.send("AUTHENTICATE EXTERNAL")
	ic.expect(" 908 * PLAIN :are available SASL mechanisms")
}

func TestCertSignInSecondFactor(t *testing.T) {
	r, addr, certs := certRig(t)
	cert, leaf := clientCert(t, nil)
	if _, err := certs.AddClientCert(t.Context(), r.user.ID, irctls.Fingerprint(leaf), ""); err != nil {
		t.Fatal(err)
	}
	svc := mfa.New(r.st)
	secret, _ := enrol(t, svc, r.user)

	// a certificate is like a client password: no second factor needed,
	// even when the admin requires one for passwords…
	r.srv.SetSecondFactors(svc, mfa.Policy{Mode: mfa.PolicyRequired})
	ic := r.dialTLS(addr, &cert).register("alice")
	ic.expect("JOIN #one")
	r.gone(ic)

	// …unless client passwords need one too
	r.srv.SetMFAPolicy(mfa.Policy{Mode: mfa.PolicyRequired, ClientPasswords: true})
	ic = r.dialTLS(addr, &cert).register("alice")
	ic.expect("A second factor is needed before you're on one")
	ic.send("PRIVMSG *status :otp " + mfa.TOTPCode(secret, time.Now()))
	ic.expect("Second factor accepted.")
	ic.expect("JOIN #one")
}

func TestCertStatusCommands(t *testing.T) {
	r, addr, _ := certRig(t)
	cert, leaf := clientCert(t, nil)
	fp := irctls.Fingerprint(leaf)

	ic := r.dialTLS(addr, &cert).register("alice", "PASS alice:alice-password")
	ic.expect(" 001 ")
	ic.status("listcerts", "No client certificates yet")
	ic.expect("This client's certificate: " + fp)
	// a label, not a fingerprint: this client's certificate
	ic.status("addcert my laptop", "Added "+fp+": an IRC client with that certificate signs in as alice without a password.")
	ic.status("addcert", "That certificate is already on your account")
	ic.status("listcerts", "1. "+fp+" - my laptop")
	other := strings.Repeat("ab", 32)
	ic.status("addcert "+strings.ToUpper(other), "Added "+other)
	// ZNC's *certauth, which answers as itself
	ic.send("PRIVMSG *certauth :list")
	line := ic.expect("2. " + other)
	if !strings.HasPrefix(line, ":*certauth!") {
		t.Fatalf("answered by %s", line)
	}
	ic.send("PRIVMSG *certauth :help")
	ic.expect(":*certauth!certauth@evrd PRIVMSG alice_one :*certauth commands")
	ic.send("PRIVMSG *certauth :del 2")
	ic.expect("Removed " + other)
	ic.status("delcert 5", "No such certificate")
	ic.status("delcert", "Usage: delcert <number|sha-256>")
	ic.status("help cert", "LISTCERTS")
	// *status answers as *status again
	if line := ic.status("delcert "+fp, "Removed "+fp); !strings.HasPrefix(line, ":*status!") {
		t.Fatalf("answered by %s", line)
	}
	r.gone(ic)

	// without a certificate, addcert needs a fingerprint
	ic = r.attach("alice:alice-password")
	ic.expect(" 001 ")
	ic.status("addcert", "This client didn't send a certificate")
	ic.status("addcert nothex", "That isn't a SHA-256 fingerprint")
	ic.status("listcerts", "This client didn't send a certificate.")
}

// Another node forwards a client with the certificate it saw; the owner
// signs it in by it.
func TestCertForwarded(t *testing.T) {
	r, _, certs := certRig(t)
	_, leaf := clientCert(t, nil)
	if _, err := certs.AddClientCert(t.Context(), r.user.ID, irctls.Fingerprint(leaf), ""); err != nil {
		t.Fatal(err)
	}
	r.srv.secret = "s3cret"
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	go r.srv.ListenInternal(r.ctx, addr)
	time.Sleep(50 * time.Millisecond)

	pc := peerCertOf(leaf)
	ic := r.dialRaw(addr)
	ic.send("EVRD-PROXY s3cret 192.0.2.7 - " + pc.param())
	ic.register("alice")
	ic.expect(" 001 alice_one ")
	r.gone(ic)

	// a node that saw none (or an older one) passes none
	ic = r.dialRaw(addr)
	ic.send("EVRD-PROXY s3cret 192.0.2.7 - -")
	ic.register("alice")
	ic.expect(" 464 ")
	ic = r.dialRaw(addr)
	ic.send("EVRD-PROXY s3cret 192.0.2.7 -")
	ic.register("alice")
	ic.expect(" 464 ")

	if p := parsePeerCert("nope,nada"); p != nil {
		t.Fatalf("parsed %+v", p)
	}
	if (peerCert{}).param() != "-" {
		t.Fatal("no certificate isn't -")
	}
}

// A client the connection holder has comes with its certificate's
// fingerprints.
func TestCertHeld(t *testing.T) {
	se := &session{conn: &links.ClientConn{Info: links.HeldClient{CertFP: "c", KeyFP: "k"}}}
	if pc := se.peerCert(); pc.cert != "c" || pc.key != "k" {
		t.Fatalf("%+v", pc)
	}
}
