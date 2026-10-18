package accounts

import (
	"strings"

	"github.com/4evrd/evrd/internal/store"
)

// NormalizeNetwork trims and checks a network's settings, filling in the
// port. Shared by the web API and *status so both apply the same rules.
func NormalizeNetwork(n *store.Network) error {
	n.Name, n.Host, n.Nick = strings.TrimSpace(n.Name), strings.TrimSpace(n.Host), strings.TrimSpace(n.Nick)
	n.Username, n.Realname = strings.TrimSpace(n.Username), strings.TrimSpace(n.Realname)
	switch {
	case n.Name == "" || strings.ContainsAny(n.Name, " /:"):
		return invalid("a network needs a name without spaces, / or :")
	case len(n.Name) > 32:
		return invalid("keep network names to 32 characters")
	case n.Host == "" || strings.ContainsAny(n.Host, " /"):
		return invalid("a network needs a server hostname")
	case n.Nick == "":
		return invalid("a network needs a nick")
	case strings.ContainsAny(n.Nick, " ,*?!@#:"):
		return invalid("nicks can't contain spaces or , * ? ! @ # :")
	case n.Port < 0 || n.Port > 65535:
		return invalid("ports are 1–65535")
	}
	if n.Port == 0 {
		n.Port = 6697
	}
	var chans []string
	for _, ch := range n.Channels {
		if ch = strings.TrimSpace(ch); ch == "" {
			continue
		}
		if !strings.ContainsRune("#&+!", rune(ch[0])) || strings.ContainsAny(ch, " ,\x07") {
			return invalid("%q isn't a channel name — channels start with #", ch)
		}
		chans = append(chans, ch)
	}
	if chans == nil {
		chans = []string{}
	}
	n.Channels = chans
	return nil
}

// CheckNetworkName refuses a name another of the user's networks already has
// (names pick the network in PASS user/network:secret, so they must differ).
func (s *Service) CheckNetworkName(userID int64, name string, exceptID int64) error {
	nets, err := s.st.ListNetworks(userID)
	if err != nil {
		return err
	}
	for _, n := range nets {
		if n.ID != exceptID && strings.EqualFold(n.Name, name) {
			return invalid("you already have a network called %s", n.Name)
		}
	}
	return nil
}

// NeedsReconnect reports whether a settings change only applies on a new
// connection. Name and channel-list edits don't: the channel list is used on
// the next connect either way.
func NeedsReconnect(a, b store.Network) bool {
	return a.Host != b.Host || a.Port != b.Port || a.TLS != b.TLS || a.Nick != b.Nick ||
		a.Username != b.Username || a.Realname != b.Realname || a.Password != b.Password
}
