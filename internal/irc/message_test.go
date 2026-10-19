package irc

import (
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	for _, tc := range []struct {
		line string
		want Message
	}{
		{"PING :tok\r\n", Message{Command: "PING", Params: []string{"tok"}}},
		{":nick!u@h privmsg #chan :hello there", Message{Prefix: "nick!u@h", Command: "PRIVMSG", Params: []string{"#chan", "hello there"}}},
		{"@time=2024-01-01T00:00:00Z;account=bob;flag;;  :srv 001 me :Welcome", Message{
			Tags:   map[string]string{"time": "2024-01-01T00:00:00Z", "account": "bob", "flag": ""},
			Prefix: "srv", Command: "001", Params: []string{"me", "Welcome"},
		}},
		{"MODE #c +o  bob", Message{Command: "MODE", Params: []string{"#c", "+o", "bob"}}},
		{"TOPIC #c :", Message{Command: "TOPIC", Params: []string{"#c", ""}}},
		{"PRIVMSG #c ::-)", Message{Command: "PRIVMSG", Params: []string{"#c", ":-)"}}},
		{"", Message{}},
		{":onlyprefix", Message{Prefix: "onlyprefix"}},
	} {
		got := Parse(tc.line)
		if got.Command != tc.want.Command || got.Prefix != tc.want.Prefix ||
			!reflect.DeepEqual(got.Tags, tc.want.Tags) || len(got.Params) != len(tc.want.Params) {
			t.Errorf("Parse(%q) = %#v, want %#v", tc.line, got, tc.want)
			continue
		}
		for i := range got.Params {
			if got.Params[i] != tc.want.Params[i] {
				t.Errorf("Parse(%q) param %d = %q, want %q", tc.line, i, got.Params[i], tc.want.Params[i])
			}
		}
	}
}

func TestString(t *testing.T) {
	for _, tc := range []struct {
		m    Message
		want string
	}{
		{Message{Command: "PING", Params: []string{"tok"}}, "PING tok"},
		{Message{Prefix: "srv", Command: "001", Params: []string{"me", "Welcome to IRC"}}, ":srv 001 me :Welcome to IRC"},
		{Message{Command: "TOPIC", Params: []string{"#c", ""}}, "TOPIC #c :"},
		{Message{Command: "PRIVMSG", Params: []string{"#c", ":-)"}}, "PRIVMSG #c ::-)"},
		{Message{Command: "QUIT"}, "QUIT"},
	} {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("%#v.String() = %q, want %q", tc.m, got, tc.want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, l := range []string{":a!b@c PRIVMSG #x :hi there", "JOIN #a,#b key", ":srv 353 me = #c :@op +v plain", "NICK new"} {
		if got := Parse(l).String(); got != l {
			t.Errorf("%q round-trips to %q", l, got)
		}
	}
}
