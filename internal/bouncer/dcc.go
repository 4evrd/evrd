package bouncer

// clientIPs are where a user's IRC clients attached from: bouncedcc lets
// only them connect to a DCC meant for the user.
func (b *Bouncer) clientIPs(userID int64) []string {
	var out []string
	for _, c := range b.ClientsOf(userID) {
		if c.Kind == "irc" && c.IP != "" {
			out = append(out, c.IP)
		}
	}
	return out
}
