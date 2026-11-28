package zncimport

import "testing"

func TestUserSettings(t *testing.T) {
	users, err := ParseConfig([]byte(`<User a>
 LoadModule = simple_away -notimer Back soon (since %awaytime%)
 LoadModule = clientnotify
 <Network n>
  Server = irc.example.org 6667
  LoadModule = simple_away -timer 5 not this one
 </Network>
</User>
<User b>
 <Network n>
  Server = irc.example.org 6667
  LoadModule = simple_away -timer 300
 </Network>
</User>
<User c>
 LoadModule = simple_away
</User>
<User d>
 LoadModule = webadmin
</User>
`))
	if err != nil {
		t.Fatal(err)
	}
	a, b, c, d := users[0].Settings, users[1].Settings, users[2].Settings, users[3].Settings
	if !a.AutoAway || *a.AutoAwayDelay != 0 || a.AwayMessage != "Back soon (since %awaytime%)" || !a.ClientNotify {
		t.Errorf("a: %+v", a)
	}
	// a network's simple_away counts for the user
	if !b.AutoAway || *b.AutoAwayDelay != 300 || b.AwayMessage != "" || b.ClientNotify {
		t.Errorf("b: %+v", b)
	}
	if !c.AutoAway || *c.AutoAwayDelay != simpleAwayDelay || c.AwayMessage != "" {
		t.Errorf("c: %+v", c)
	}
	if d != (Settings{}) {
		t.Errorf("d: %+v", d)
	}
}
