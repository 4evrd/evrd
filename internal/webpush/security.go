package webpush

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"syscall"
	"time"
)

// Endpoint safety, as Ergo has it: a client picks the URL evrd POSTs to, so
// evrd only goes to https URLs, and never to an address inside its own
// network (loopback, private, link-local, CGNAT…): checked on the address
// actually dialed, so a name that resolves inside is refused too.

var (
	// ErrEndpoint means a push endpoint isn't an https URL.
	ErrEndpoint = errors.New("webpush: the endpoint must be an https URL")
	// errInternalIP is a dial refused: the address is internal.
	errInternalIP = errors.New("webpush: dialing an internal address is forbidden")

	// carrier-grade NAT, used as private space (Tailscale among others)
	cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")
)

// CheckEndpoint reports whether endpoint is a URL evrd will push to.
func CheckEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ErrEndpoint
	}
	return nil
}

// externalClient is an HTTP client that only connects to external
// addresses, doesn't follow redirects and ignores proxy settings.
func externalClient(timeout time.Duration) *http.Client {
	d := &net.Dialer{
		Timeout: timeout,
		Control: func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}
			if internalIP(ip) {
				return errInternalIP
			}
			return nil
		},
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:         d.DialContext,
			TLSHandshakeTimeout: timeout,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     90 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func internalIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || cgnatPrefix.Contains(ip)
}
