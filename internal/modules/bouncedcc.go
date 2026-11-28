package modules

// bouncedcc: DCC chats and file transfers relayed through evrd (internal/dcc).

import (
	"github.com/4evrd/evrd/internal/dcc"
	"github.com/4evrd/evrd/internal/irc"
	"github.com/4evrd/evrd/internal/messages"
)

func init() {
	register(Info{
		Name: "bouncedcc", ZNC: "bouncedcc", Title: "Bounce DCC",
		Description: "Relays DCC chats and file transfers through evrd, so the other side never sees your address and your client needn't be reachable. An admin sets the address it uses (dcc_public).",
		new:         func(h Host, s Settings) Module { return newBounceDCC(h) },
	})
}

type bouncedcc struct {
	r *dcc.Relay
}

func newBounceDCC(h Host) *bouncedcc {
	r := &dcc.Relay{
		Send:  h.Send,
		Log:   h.Log,
		Reply: func(t messages.Text) { h.Reply(t.English()) },
	}
	if a, ok := h.(ClientAddrs); ok {
		r.ClientIPs = a.ClientIPs
	}
	return &bouncedcc{r: r}
}

func (b *bouncedcc) Registered()      {}
func (b *bouncedcc) Line(irc.Message) {}

func (b *bouncedcc) ServerCTCP(from, text string) string { return b.r.FromServer(from, text) }

func (b *bouncedcc) ClientCTCP(target, text, clientIP string) string {
	return b.r.FromClient(target, text, clientIP)
}
