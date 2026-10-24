package bouncer

import (
	"testing"
	"time"

	"github.com/4evrd/evrd/internal/store"
)

func TestFromForADevice(t *testing.T) {
	on := true
	cleared := time.Now().Add(-time.Hour)
	lastOn := time.Now()
	device := time.Now().Add(-10 * time.Minute)
	p := Playback{Buffers: map[string]store.BufferPlayback{"#a": {AutoClear: &on, ClearedAt: &cleared}}, LastOn: lastOn}

	if !p.From("#a").Equal(lastOn) {
		t.Fatal("no device: from when a client was last on")
	}
	p.Device = "password:phone"
	if !p.From("#a").Equal(cleared) {
		t.Fatal("a device's first visit: the normal backlog, from the clear mark")
	}
	p.DeviceAt = &device
	if !p.From("#a").Equal(device) {
		t.Fatal("a device that was here: from where it left off")
	}
	early := cleared.Add(-time.Hour)
	p.DeviceAt = &early
	if !p.From("#a").Equal(cleared) {
		t.Fatal("never before the clear mark")
	}
	off := false
	p.Buffers["#b"] = store.BufferPlayback{AutoClear: &off, ClearedAt: &cleared}
	p.DeviceAt = &device
	if !p.From("#b").Equal(cleared) {
		t.Fatal("a buffer that doesn't auto-clear plays its last lines, device or not")
	}
}
