package upstream

import (
	"strings"
	"testing"

	"github.com/4evrd/evrd/internal/store"
)

func TestSendMultiline(t *testing.T) {
	var msgs msgRec
	s := startScripted(t, capServer("message-tags batch draft/multiline=max-lines=5", true, ""))
	c := New(s.network(store.Network{ID: 3, Nick: "me"}), quiet, Options{Persist: msgs.add})
	run(t, c)
	eventually(t, "registered", func() bool { return c.Status() == StatusConnected && c.HasCap("draft/multiline") })
	ids := c.SendMultiline("s1", "+x=1", "NOTICE", "#c", []string{"a", "b", "c"}, []bool{false, true, false})
	eventually(t, "sent", func() bool { return index(s.last().got(), "BATCH -ml") >= 0 })
	got := s.last().got()
	i := index(got, "@+x=1 BATCH +ml")
	if i < 0 || !strings.HasSuffix(got[i], " draft/multiline #c") ||
		!strings.HasSuffix(got[i+1], " NOTICE #c :a") || !strings.Contains(got[i+2], ";draft/multiline-concat NOTICE #c :b") {
		t.Fatalf("sent %q", got)
	}
	m := msgs.all()
	if len(ids) != 2 || len(m) != 2 || m[0].Text != "ab" || m[1].Text != "c" || m[0].Kind != "notice" || m[0].ID() != ids[0] || m[0].Origin != "s1" {
		t.Fatalf("recorded %+v", m)
	}
}
