package client

import (
	"testing"
	"time"
)

func TestReadMarkersSync(t *testing.T) {
	r := newRig(t)
	a := r.attach("alice/one:alice-password", "draft/read-marker")
	// after its JOIN, a channel's marker (none yet)
	a.expect("JOIN #one")
	a.expect(":evrd MARKREAD #one *")
	b := r.attached("draft/read-marker")
	n := r.network("one")

	at := time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	ts := "timestamp=" + at.Format(timeFormat)
	a.send("MARKREAD #one " + ts)
	a.expect(":evrd MARKREAD #one " + ts)
	// the other client hears it, and the web app's markers have it
	b.expect(":evrd MARKREAD #one " + ts)
	if ms, _ := r.b.ReadMarkers(n.ID); !ms["#one"].Equal(at) {
		t.Fatalf("markers %v", ms)
	}
	// it never goes back
	a.send("MARKREAD #one timestamp=" + at.Add(-time.Hour).Format(timeFormat))
	a.expect(":evrd MARKREAD #one " + ts)
	a.send("MARKREAD #ONE")
	a.expect(":evrd MARKREAD #ONE " + ts)

	// read in the web app: the clients hear it
	if err := r.b.MarkRead(n.ID, "#one"); err != nil {
		t.Fatal(err)
	}
	a.expect(":evrd MARKREAD #one timestamp=")

	a.send("MARKREAD")
	a.expect(":evrd FAIL MARKREAD NEED_MORE_PARAMS :Missing parameters")
	a.send("MARKREAD #one yesterday")
	a.expect(":evrd FAIL MARKREAD INVALID_PARAMS yesterday :Invalid parameters")

	// a JOIN from the network brings the marker too
	a.send("JOIN #new")
	a.expect("JOIN #new")
	a.expect(":evrd MARKREAD #new *")

	// without the capability, MARKREAD is the network's business
	plain := r.attached()
	plain.send("MARKREAD #one")
	plain.send("PRIVMSG *status :version")
	if seen := plain.until("PRIVMSG alice_one :evrd "); anyHas(seen, "MARKREAD") {
		t.Fatalf("answered without the cap: %q", seen)
	}
}
