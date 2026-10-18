package upstream

// ZNC-style per-network behaviour: server failover, certificate trust,
// non-UTF-8 networks, flood protection, channel keys / join delay, CTCP
// replies and the quit message.

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/htmlindex"

	"github.com/4evrd/evrd/internal/store"
)

// UntrustedCertError is returned when a server's certificate isn't trusted.
// Fingerprint is what to add to the network's trusted fingerprints to accept
// it anyway.
type UntrustedCertError struct {
	Fingerprint string // SHA-256 of the leaf certificate, lowercase hex
	Reason      string
}

func (e *UntrustedCertError) Error() string {
	return fmt.Sprintf("untrusted certificate (%s) — SHA-256 %s", e.Reason, ColonHex(e.Fingerprint))
}

// ColonHex formats a hex fingerprint as AB:CD:… the way ZNC shows them.
func ColonHex(h string) string {
	h = strings.ToUpper(h)
	var b strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			b.WriteByte(':')
		}
		b.WriteString(h[i:min(i+2, len(h))])
	}
	return b.String()
}

// Fingerprint is the SHA-256 of a certificate, lowercase hex.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// tlsConfig checks the server certificate the way ZNC does: a listed
// fingerprint always wins, then "trust all", then normal PKI validation.
func (c *Conn) tlsConfig(host string) *tls.Config {
	n := c.net
	return &tls.Config{
		ServerName:         host,
		InsecureSkipVerify: true, // verified below
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return &UntrustedCertError{Reason: "no certificate"}
			}
			leaf := cs.PeerCertificates[0]
			fp := Fingerprint(leaf)
			if slices.Contains(n.Fingerprints, fp) || n.TrustAllCerts {
				return nil
			}
			reason := "certificate validation is off for this network"
			if n.TrustPKI {
				opts := x509.VerifyOptions{DNSName: host, Intermediates: x509.NewCertPool()}
				for _, ic := range cs.PeerCertificates[1:] {
					opts.Intermediates.AddCert(ic)
				}
				_, err := leaf.Verify(opts)
				if err == nil {
					return nil
				}
				reason = shortCertErr(err)
			}
			c.mu.Lock()
			c.health.UntrustedFingerprint = fp
			c.mu.Unlock()
			return &UntrustedCertError{Fingerprint: fp, Reason: reason}
		},
	}
}

func shortCertErr(err error) string {
	var hn x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	switch {
	case errors.As(err, &hn):
		return "it's for a different host name"
	case errors.As(err, &ua):
		return "signed by an unknown authority"
	case errors.As(err, &ci):
		if ci.Reason == x509.Expired {
			return "expired or not yet valid"
		}
		return "invalid"
	}
	return err.Error()
}

// --- encodings -----------------------------------------------------------------

// codec converts between a network's charset and UTF-8. The zero value
// passes everything through.
type codec struct {
	enc      encoding.Encoding
	fallback bool // decode only lines that aren't valid UTF-8; send UTF-8
}

func newCodec(spec string) codec {
	if spec == "" {
		return codec{}
	}
	e, err := htmlindex.Get(strings.TrimPrefix(spec, "^"))
	if err != nil {
		return codec{}
	}
	return codec{enc: e, fallback: strings.HasPrefix(spec, "^")}
}

func (k codec) decode(b []byte) string {
	if k.enc == nil || (k.fallback && utf8.Valid(b)) {
		return string(b)
	}
	out, err := k.enc.NewDecoder().Bytes(b)
	if err != nil {
		return string(b)
	}
	return string(out)
}

func (k codec) encode(s string) string {
	if k.enc == nil || k.fallback {
		return s
	}
	out, err := encoding.ReplaceUnsupported(k.enc.NewEncoder()).String(s)
	if err != nil {
		return s
	}
	return out
}

// --- flood protection ----------------------------------------------------------

// flood is a token bucket: burst lines go out at once, then rate per second.
// Senders wait in order (the mutex is held while waiting).
type flood struct {
	mu     sync.Mutex
	rate   float64
	burst  float64
	tokens float64
	last   time.Time
}

func (f *flood) set(rate float64, burst int) {
	f.mu.Lock()
	f.rate, f.burst = rate, float64(max(burst, 1))
	f.tokens = f.burst
	f.last = time.Now()
	f.mu.Unlock()
}

func (f *flood) wait() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rate <= 0 {
		return
	}
	now := time.Now()
	f.tokens = min(f.burst, f.tokens+now.Sub(f.last).Seconds()*f.rate)
	f.last = now
	if f.tokens < 1 {
		d := time.Duration((1 - f.tokens) / f.rate * float64(time.Second))
		time.Sleep(d)
		f.tokens, f.last = 1, time.Now()
	}
	f.tokens--
}

// exempt lines skip flood protection: link upkeep and registration.
func exempt(line string) bool {
	cmd, _, _ := strings.Cut(line, " ")
	switch strings.ToUpper(cmd) {
	case "PING", "PONG", "CAP", "AUTHENTICATE", "PASS", "USER", "QUIT":
		return true
	}
	return false
}

// --- channels --------------------------------------------------------------------

// joinLines builds the JOIN commands for channels: keyed channels first (so
// keys line up), several channels per line, skipping disabled ones.
func joinLines(channels []string, settings map[string]store.ChannelSettings) []string {
	var keyed, plain []string
	var keys []string
	for _, ch := range channels {
		cs := settings[strings.ToLower(ch)]
		if cs.Disabled {
			continue
		}
		if cs.Key != "" {
			keyed = append(keyed, ch)
			keys = append(keys, cs.Key)
		} else {
			plain = append(plain, ch)
		}
	}
	var lines []string
	const per = 10
	for i := 0; i < len(keyed); i += per {
		j := min(i+per, len(keyed))
		lines = append(lines, "JOIN "+strings.Join(keyed[i:j], ",")+" "+strings.Join(keys[i:j], ","))
	}
	for i := 0; i < len(plain); i += per {
		lines = append(lines, "JOIN "+strings.Join(plain[i:min(i+per, len(plain))], ","))
	}
	return lines
}

// --- CTCP ------------------------------------------------------------------------

// ctcp answers a CTCP request addressed to us. It reports whether the
// request was handled here (and so isn't passed to clients): a configured
// reply (empty = blocked), or, with no client attached, the stock answers.
func (c *Conn) ctcp(prefix, target, text string) bool {
	if len(text) < 2 || text[0] != '\x01' || !strings.EqualFold(target, c.Nick()) {
		return false
	}
	body := strings.TrimSuffix(text[1:], "\x01")
	cmd, arg, _ := strings.Cut(body, " ")
	cmd = strings.ToUpper(cmd)
	if cmd == "ACTION" {
		return false
	}
	from := nickOf(prefix)
	reply := func(s string) { c.send("NOTICE " + from + " :\x01" + cmd + " " + s + "\x01") }
	c.mu.Lock()
	custom, ok := c.ctcpReplies[cmd]
	c.mu.Unlock()
	if ok {
		if custom != "" {
			reply(custom)
		}
		return true
	}
	if c.idle == nil || !c.idle() {
		return false // a client will answer
	}
	switch cmd {
	case "VERSION":
		reply("evrd " + c.version)
	case "PING":
		reply(arg)
	case "TIME":
		reply(time.Now().Format(time.RFC1123Z))
	case "CLIENTINFO":
		reply("ACTION CLIENTINFO PING TIME VERSION")
	default:
		return false
	}
	return true
}
