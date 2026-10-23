package commands

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// tool is a command line like evrctl's: root flags, a command with its own
// flags, one whose arguments are all its own, and one that fails.
type tool struct {
	root          *cobra.Command
	out           bytes.Buffer
	config, email string
	admin         bool
	ran           string
	args          []string
}

func newTool() *tool {
	t := &tool{}
	record := func(c *cobra.Command, args []string) error {
		t.ran, t.args = c.Name(), args
		return nil
	}
	t.root = &cobra.Command{Use: "tool", Args: Unknown, RunE: record}
	t.root.SetOut(&t.out)
	t.root.SetErr(&t.out)
	t.root.PersistentFlags().StringVar(&t.config, "config", "", "config file")
	add := &cobra.Command{Use: "add", RunE: record}
	add.Flags().BoolVar(&t.admin, "admin", false, "admin")
	add.Flags().StringVar(&t.email, "email", "", "email")
	passwd := &cobra.Command{Use: "passwd", Aliases: []string{"pw"}, DisableFlagParsing: true, RunE: record}
	fail := &cobra.Command{Use: "fail", RunE: func(*cobra.Command, []string) error { return errors.New("it broke") }}
	exit := &cobra.Command{Use: "exit", RunE: func(*cobra.Command, []string) error { return Status(3) }}
	user := &cobra.Command{Use: "user"}
	user.AddCommand(add, passwd)
	t.root.AddCommand(user, fail, exit)
	return t
}

func (t *tool) run(args ...string) int {
	return Execute(context.Background(), t.root, append([]string{"tool"}, args...))
}

func TestExecute(t *testing.T) {
	for _, tc := range []struct {
		args          []string
		code          int
		ran           string
		rest          []string
		config, email string
		admin         bool
		out           string
	}{
		// Go-style single-dash long flags, and double-dash ones
		{args: []string{"-config", "c.conf", "user", "add", "-admin", "-email", "e@x", "bob", "pw"}, ran: "add", rest: []string{"bob", "pw"}, config: "c.conf", email: "e@x", admin: true},
		{args: []string{"--config=c.conf", "user", "add", "--email", "e@x", "bob"}, ran: "add", rest: []string{"bob"}, config: "c.conf", email: "e@x"},
		{args: []string{"-config=c.conf", "user", "add", "-admin=true", "bob"}, ran: "add", rest: []string{"bob"}, config: "c.conf", admin: true},
		// a command's own flags after the command; a root flag there too
		{args: []string{"user", "add", "-config", "c.conf", "bob"}, ran: "add", rest: []string{"bob"}, config: "c.conf"},
		// flags come before arguments: after one, "-admin" is an argument,
		// as is "-" (stdin)
		{args: []string{"user", "add", "bob", "-admin"}, ran: "add", rest: []string{"bob", "-admin"}},
		{args: []string{"user", "add", "bob", "-"}, ran: "add", rest: []string{"bob", "-"}},
		{args: []string{"user", "add", "--", "-bob"}, ran: "add", rest: []string{"-bob"}},
		// a command that takes no flags gets every argument, even "-x" ones
		{args: []string{"-config", "c.conf", "user", "passwd", "bob", "-secret-password"}, ran: "passwd", rest: []string{"bob", "-secret-password"}, config: "c.conf"},
		{args: []string{"user", "pw", "-h"}, ran: "passwd", rest: []string{"-h"}},
		// no command
		{args: nil, ran: "tool", rest: []string{}},
		// errors: a flag, 2 with the usage; a command, 2; a failure, 1
		{args: []string{"-nope"}, code: 2, out: "unknown flag: --nope\nUsage:"},
		{args: []string{"user", "add", "-email"}, code: 2, out: "flag needs an argument: --email"},
		{args: []string{"frob"}, code: 2, out: "tool: unknown command \"frob\"\nUsage:"},
		{args: []string{"fail"}, code: 1, out: "tool: it broke\n"},
		{args: []string{"exit"}, code: 3},
		// help: 0
		{args: []string{"-h"}, out: "Usage:"},
		{args: []string{"-help"}, out: "Usage:"},
		{args: []string{"user", "add", "-h"}, out: "--admin"},
	} {
		tl := newTool()
		code := tl.run(tc.args...)
		if code != tc.code || tl.ran != tc.ran || (tc.rest != nil && !slices.Equal(tl.args, tc.rest)) ||
			tl.config != tc.config || tl.email != tc.email || tl.admin != tc.admin || !strings.Contains(tl.out.String(), tc.out) {
			t.Errorf("%q: exit %d, ran %q %q, config %q email %q admin %v, output %q", tc.args, code, tl.ran, tl.args, tl.config, tl.email, tl.admin, tl.out.String())
		}
	}
}

func TestStatus(t *testing.T) {
	if Status(0) != nil {
		t.Fatal("Status(0) isn't nil")
	}
	var st ExitStatus
	if err := Status(2); !errors.As(err, &st) || st != 2 || err.Error() != "exit status 2" {
		t.Fatalf("Status(2) = %v", err)
	}
}

func TestNormalize(t *testing.T) {
	root := newTool().root
	for _, tc := range []struct{ in, want []string }{
		{[]string{"-config", "x", "user", "add", "-admin", "bob"}, []string{"--config", "x", "user", "add", "--admin", "bob"}},
		// a value that looks like a flag stays a value
		{[]string{"-config", "-odd", "user"}, []string{"--config", "-odd", "user"}},
		// short flags and stdin's "-" are left alone
		{[]string{"-h"}, []string{"-h"}},
		{[]string{"user", "add", "-"}, []string{"user", "add", "-"}},
		// after an argument, or "--", nothing changes
		{[]string{"user", "add", "bob", "-admin"}, []string{"user", "add", "bob", "-admin"}},
		{[]string{"--", "-config"}, []string{"--", "-config"}},
		// nor in a command without flags
		{[]string{"user", "passwd", "-config"}, []string{"user", "passwd", "-config"}},
		// a flag without its value at the end
		{[]string{"-config"}, []string{"--config"}},
	} {
		if got := Normalize(root, tc.in); !slices.Equal(got, tc.want) {
			t.Errorf("Normalize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
