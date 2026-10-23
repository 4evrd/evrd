// Package commands runs evrd's command lines (evrd, evrctl, evrd-links) on
// Cobra, keeping them as they were with Go's flag package: single-dash long
// flags (-config), flags before arguments, and its exit statuses — 0 for -h,
// 2 for a command line that doesn't parse, 1 for an error.
package commands

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// ExitStatus ends a command with a status, anything to say already said.
type ExitStatus int

func (e ExitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// Status is a command's exit code as its error: nil for 0.
func Status(code int) error {
	if code == 0 {
		return nil
	}
	return ExitStatus(code)
}

// Execute runs root on args (args[0] the program name) and returns the exit
// status. Help, usage and errors go to root's error output; an error that
// isn't an ExitStatus is printed as "<name>: <error>", with status 1.
func Execute(ctx context.Context, root *cobra.Command, args []string) int {
	Prepare(root)
	root.SetArgs(Normalize(root, args[1:]))
	err := root.ExecuteContext(ctx)
	var st ExitStatus
	switch {
	case err == nil:
		return 0
	case errors.As(err, &st):
		return int(st)
	default:
		fmt.Fprintf(root.ErrOrStderr(), "%s: %v\n", root.Name(), err)
		return 1
	}
}

// Prepare sets up root and its subcommands as Go's flag package would
// parse them: each command's flags before its arguments or subcommand (the
// rest are arguments, even when they start with "-"), and a flag error
// printed with the usage, status 2. Execute calls it.
func Prepare(root *cobra.Command) {
	root.TraverseChildren = true
	root.SilenceErrors, root.SilenceUsage = true, true
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		fmt.Fprintln(c.ErrOrStderr(), err)
		c.Usage()
		return ExitStatus(2)
	})
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		c.Flags().SetInterspersed(false)
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

// Unknown is the Args check for a command whose arguments can only name a
// subcommand: anything else is printed as unknown, with the usage, status 2.
func Unknown(c *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	fmt.Fprintf(c.ErrOrStderr(), "%s: unknown command %q\n", c.Root().Name(), args[0])
	c.Usage()
	return ExitStatus(2)
}

// Normalize rewrites Go-style single-dash long flags ("-config x") as the
// double-dash ones Cobra parses ("--config x"), following the command line
// down to the subcommand it names. Arguments, and everything after the
// first one or "--", are left as they are.
func Normalize(cmd *cobra.Command, args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if cmd.DisableFlagParsing || a == "--" {
			return append(out, args[i:]...)
		}
		if len(a) > 1 && a[0] == '-' {
			name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
			if a[1] != '-' && len(name) > 1 {
				a = "-" + a
			}
			out = append(out, a)
			// a flag's value is the next argument, unless it's a bool
			if f := lookup(cmd, name); f != nil && f.NoOptDefVal == "" && !inline && i+1 < len(args) {
				i++
				out = append(out, args[i])
			}
			continue
		}
		sub := subcommand(cmd, a)
		if sub == nil {
			return append(out, args[i:]...)
		}
		out = append(out, a)
		cmd = sub
	}
	return out
}

// lookup finds a flag cmd takes: its own, or one its parents pass down.
func lookup(cmd *cobra.Command, name string) *pflag.Flag {
	if f := cmd.Flags().Lookup(name); f != nil {
		return f
	}
	for c := cmd; c != nil; c = c.Parent() {
		if f := c.PersistentFlags().Lookup(name); f != nil {
			return f
		}
	}
	return nil
}

func subcommand(cmd *cobra.Command, name string) *cobra.Command {
	for _, c := range cmd.Commands() {
		if c.Name() == name || c.HasAlias(name) {
			return c
		}
	}
	return nil
}
