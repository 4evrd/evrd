package config

import (
	"net/netip"
	"testing"
	"time"
)

func TestDCCSettings(t *testing.T) {
	c := Default()
	if c.DCC.Timeout != time.Minute || len(c.DCC.Public) != 0 {
		t.Fatalf("defaults %+v", c.DCC)
	}
	for _, kv := range [][2]string{
		{"dcc_public", "203.0.113.5 2001:db8::5"}, {"dcc_listen", "203.0.113.5"}, {"dcc_ports", "50000-50010 50020"},
		{"dcc_timeout", "90s"}, {"dcc_max_mb", "100"}, {"dcc_private", "yes"},
	} {
		if err := c.set(kv[0], kv[1]); err != nil {
			t.Fatalf("%s: %v", kv[0], err)
		}
		if got := c.Value(kv[0]); got != kv[1] && kv[0] != "dcc_timeout" {
			t.Errorf("%s reads back as %q", kv[0], got)
		}
	}
	d := c.DCC
	if len(d.Public) != 2 || d.Public[1] != netip.MustParseAddr("2001:db8::5") || d.Listen != "203.0.113.5" ||
		len(d.Ports) != 2 || d.Ports[1] != (PortRange{50020, 50020}) || d.Timeout != 90*time.Second || d.MaxMB != 100 || !d.Private {
		t.Fatalf("%+v", d)
	}
	for _, bad := range [][2]string{
		{"dcc_public", "dcc.example.org"}, {"dcc_public", "1.2.3.4 5.6.7.8"}, {"dcc_ports", "80-90"},
		{"dcc_ports", "6000-5000"}, {"dcc_ports", "lots"}, {"dcc_timeout", "1ms"}, {"dcc_max_mb", "-1"},
	} {
		if err := Check(bad[0], bad[1]); err == nil {
			t.Errorf("%s %s accepted", bad[0], bad[1])
		}
	}

	// the admin UI's values, then each node's environment
	t.Setenv("EVRD_DCC_PUBLIC", "198.51.100.7")
	t.Setenv("EVRD_DCC_PORTS", "40000-40001")
	eff, err := Default().WithSettings(map[string]string{"dcc_public": "203.0.113.5", "dcc_ports": "50000-50100", "dcc_listen": " "})
	if err != nil {
		t.Fatal(err)
	}
	if eff.Value("dcc_public") != "198.51.100.7" || eff.Value("dcc_ports") != "40000-40001" || eff.DCC.Listen != "" {
		t.Fatalf("%+v", eff.DCC)
	}
}
