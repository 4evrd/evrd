// Package evrd is the evrd command: the IRCForever IRC bouncer daemon.
//
//	evrd [-config file]   serve (as does evrd serve)
//	evrd -version         print the version (as does evrd version)
package evrd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/commands"
	"github.com/4evrd/evrd/internal/version"
)

// Execute is evrd: args are its command line (args[0] the program name),
// and the result is the exit status.
func Execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return commands.Execute(ctx, newRoot(stdout, stderr), args)
}

// options are evrd's flags.
type options struct {
	config  string
	version bool
}

func newRoot(stdout, stderr io.Writer) *cobra.Command {
	var o options
	root := &cobra.Command{
		Use:   "evrd",
		Short: "The IRCForever IRC bouncer daemon",
		Args:  commands.Unknown,
		RunE: func(c *cobra.Command, _ []string) error {
			if o.version {
				printVersion(stdout)
				return nil
			}
			return commands.Status(serve(c.Context(), o.configPath(), stderr))
		},
	}
	root.SetOut(stderr) // help and usage, where the flag package printed them
	root.SetErr(stderr)
	root.PersistentFlags().StringVar(&o.config, "config", "", "path to config file (optional; default $EVRD_CONFIG)")
	root.Flags().BoolVar(&o.version, "version", false, "print version and exit")
	root.AddCommand(newServe(&o, stderr), newVersion(stdout))
	return root
}

// configPath is -config, else $EVRD_CONFIG (e.g. set in a container image
// or compose file).
func (o *options) configPath() string {
	if o.config != "" {
		return o.config
	}
	return os.Getenv("EVRD_CONFIG")
}

func printVersion(w io.Writer) { fmt.Fprintln(w, "evrd", version.Version) }
