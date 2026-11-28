package upstream

// How many users and channels the network has, from LUSERS: the server
// sends it after registration, and evrd asks again (AskLusers) when the
// managed API wants a fresh count. evrd's own LUSERS replies aren't passed
// to clients, which never asked.

import (
	"strconv"
	"strings"
	"time"
)

// lusersWait is how long evrd treats LUSERS replies as its own after it
// asked: a server that never sends RPL_GLOBALUSERS (266), the last of
// them, can't keep a client's own LUSERS from it for longer.
const lusersWait = 10 * time.Second

// Lusers is the network's size as the server last said. A count is -1
// when the server didn't say it.
type Lusers struct {
	Users    int
	Channels int
	At       time.Time // zero: never heard
}

// lusersState is what the connection knows; asked is when evrd's own LUSERS
// went out (zero: none out). Both under c.mu.
type lusersState struct {
	cur   Lusers
	asked time.Time
}

// Lusers is the network's size from this connection's last LUSERS.
func (c *Conn) Lusers() Lusers {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.lu.cur
	if l.At.IsZero() {
		return Lusers{Users: -1, Channels: -1}
	}
	return l
}

// AskLusers asks the server for LUSERS on this connection, keeping the
// replies from clients. No-op unless connected.
func (c *Conn) AskLusers() {
	if c.Status() != StatusConnected {
		return
	}
	c.mu.Lock()
	c.lu.asked = time.Now()
	c.mu.Unlock()
	c.send("LUSERS")
}

// lusersReply takes one of the LUSERS numerics. Holds c.mu.
func (c *Conn) lusersReply(cmd string, p []string) {
	if c.lu.cur.At.IsZero() {
		c.lu.cur = Lusers{Users: -1, Channels: -1}
	}
	switch cmd {
	case "251": // RPL_LUSERCLIENT: me :There are X users and Y invisible on Z servers
		if n, ok := sumUsers(lastOf(p)); ok {
			c.lu.cur.Users = n
			c.lu.cur.At = time.Now()
		}
	case "254": // RPL_LUSERCHANNELS: me N :channels formed
		if len(p) >= 2 {
			if n, err := strconv.Atoi(p[1]); err == nil && n >= 0 {
				c.lu.cur.Channels = n
				c.lu.cur.At = time.Now()
			}
		}
	case "266": // RPL_GLOBALUSERS: me [cur max] :Current global users: cur, max: max
		n := -1
		if len(p) >= 3 {
			if v, err := strconv.Atoi(p[1]); err == nil {
				n = v
			}
		}
		if n < 0 {
			n = firstNumberAfter(lastOf(p), ":")
		}
		if n >= 0 {
			c.lu.cur.Users = n
			c.lu.cur.At = time.Now()
		}
	}
}

// lusersOwn reports whether a LUSERS numeric answers evrd's own LUSERS
// (then it isn't passed on); 266 ends it.
func (c *Conn) lusersOwn(cmd string) bool {
	switch cmd {
	case "251", "252", "253", "254", "255", "265", "266":
	default:
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lu.asked.IsZero() {
		return false
	}
	if time.Since(c.lu.asked) > lusersWait {
		c.lu.asked = time.Time{}
		return false
	}
	if cmd == "266" {
		c.lu.asked = time.Time{}
	}
	return true
}

func lastOf(p []string) string {
	if len(p) == 0 {
		return ""
	}
	return p[len(p)-1]
}

// sumUsers reads "There are X users and Y invisible on Z servers": X+Y.
func sumUsers(s string) (int, bool) {
	f := strings.Fields(s)
	users, inv, got := 0, 0, false
	for i := 1; i < len(f); i++ {
		n, err := strconv.Atoi(f[i-1])
		if err != nil {
			continue
		}
		switch strings.ToLower(strings.TrimRight(f[i], ",")) {
		case "users", "user":
			users, got = n, true
		case "invisible":
			inv = n
		}
	}
	return users + inv, got
}

// firstNumberAfter is the first whole number in s after sep (-1: none).
func firstNumberAfter(s, sep string) int {
	_, rest, ok := strings.Cut(s, sep)
	if !ok {
		return -1
	}
	for _, f := range strings.FieldsFunc(rest, func(r rune) bool { return r < '0' || r > '9' }) {
		if n, err := strconv.Atoi(f); err == nil {
			return n
		}
	}
	return -1
}
