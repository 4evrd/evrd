package upstream

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"

	"github.com/4evrd/evrd/internal/messages"
)

// The reasons a connection fails or drops, as evrd puts them: the
// irc.reason.* texts (internal/messages), so the connection log and events
// can be read in each user's language. The ones from TLS and the holder
// arrive as text (an error from evrd-links is only its message), so they
// are recognised by their English; anything else is passed on as it is.

var (
	untrustedRE = regexp.MustCompile(`^untrusted certificate \((.*)\) — SHA-256 (\S+)$`)
	notTLSRE    = regexp.MustCompile(`^the server didn't answer with TLS on this port — it said (".*") \(is it a plain-text port\?\)$`)
	refusedRE   = regexp.MustCompile(`^the server refused the connection: (.*)$`)
)

// certReasons are irctls' words for why a certificate isn't trusted.
var certReasons = map[string]string{
	"it's for a different host name":                 "certHost",
	"signed by an unknown authority":                 "certAuthority",
	"expired or not yet valid":                       "certExpired",
	"invalid":                                        "certInvalid",
	"certificate validation is off for this network": "certNoValidation",
	"no certificate":                                 "certNone",
}

// fixedReasons are evrd's own failures that come as text.
var fixedReasons = map[string]string{
	"the server hung up during the TLS handshake": "hungUp",
	"the server speaks TLS on this port":          "speaksTLS",
	"no servers configured":                       "noServers",
	"the server doesn't offer SASL":               "noSASL",
	"the server refused the SASL capability":      "refusedSASL",
}

// reasonOf is why err ended a connection, in words. Its English is what
// describeErr says.
func reasonOf(err error) messages.Text {
	var dnsErr *net.DNSError
	var opErr *net.OpError
	var cert *tls.CertificateVerificationError
	switch {
	case errors.Is(err, ErrPingTimeout):
		return messages.T("reason.pingTimeout")
	case errors.Is(err, io.EOF):
		return messages.T("reason.closed")
	case errors.As(err, &dnsErr):
		return messages.T("reason.noHost", "host", dnsErr.Name)
	case errors.Is(err, context.DeadlineExceeded):
		return messages.T("reason.timedOut")
	case errors.As(err, &opErr) && opErr.Op == "dial":
		switch s := err.Error(); {
		case strings.Contains(s, "refused"):
			return messages.T("reason.refused")
		case strings.Contains(s, "unreachable") || strings.Contains(s, "no route to host"):
			return messages.T("reason.unreachable")
		}
		return messages.T("reason.cantConnect")
	case err == nil:
		return messages.T("reason.disconnected")
	case errors.As(err, &cert):
		return messages.T("reason.certRejected")
	}
	return reasonText(err.Error())
}

// reasonText is a failure that came as text, in words where evrd knows it.
func reasonText(s string) messages.Text {
	if key, ok := fixedReasons[s]; ok {
		return messages.T("reason." + key)
	}
	if m := untrustedRE.FindStringSubmatch(s); m != nil {
		why := messages.Raw(m[1])
		if key, ok := certReasons[m[1]]; ok {
			why = messages.T("reason." + key)
		}
		return messages.T("reason.untrusted", "why", why, "fingerprint", m[2])
	}
	if m := notTLSRE.FindStringSubmatch(s); m != nil {
		return messages.T("reason.notTLS", "said", m[1])
	}
	if m := refusedRE.FindStringSubmatch(s); m != nil {
		return messages.T("reason.serverRefused", "said", m[1])
	}
	switch {
	case strings.Contains(s, "connection reset by peer"):
		return messages.T("reason.reset")
	case strings.Contains(s, "broken pipe"):
		return messages.T("reason.brokenPipe")
	case strings.HasSuffix(s, "i/o timeout"):
		return messages.T("reason.timedOut")
	}
	return messages.Raw(s)
}
