// Package evrctl is the evrctl command, which administers an evrd database:
// mainly bootstrapping the first admin, and user management when the web
// app isn't reachable.
//
// It writes to the database directly. Suspending or deleting a user here
// signs them out of the web app at once; the node running them notices on its
// next cluster tick (within a few seconds) and drops their IRC clients and
// network connections.
package evrctl

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/commands"
	"github.com/4evrd/evrd/internal/config"
)

func (c *ctl) usage() {
	fmt.Fprintln(c.stderr, `usage: evrctl [-config file | -db dsn] <command> [args]

commands:
  version                                  print evrctl version
  genkey                                   print a new secret_key (EVRD_SECRET_KEY) for encrypting saved passwords
  user add [-admin] [-email e] <name> [password]
                                           create an account (no password = SSO / client passwords only;
                                           "-" reads it from stdin)
  user list                                list accounts
  user passwd <name> <password>            set an account's password
  user role <name> user|admin              change an account's role
  user suspend <name> [reason]             suspend an account
  user unsuspend <name>                    lift a suspension
  user delete <name>                       delete an account and everything it owns
  history copy <from> <to>                 copy all chat history between stores, e.g.
                                           pebble:/var/lib/evrd/history scylla://db1,db2/evrd
                                           (stop evrd first when <from> is a pebble store)
  history import-znc [-dry-run] [-tz zone] <user> <znc-user-dir>
                                           import a ZNC user's logs (e.g. ~/.znc/users/admin) into
                                           <user>'s networks of the same names; keeps everything
                                           from then on (backlog unlimited). Stop evrd first with
                                           a pebble store, and run it once.`)
}

// ctl is one run of evrctl: where it reads and writes, and its flags.
type ctl struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	configPath, db string
}

// Execute is evrctl: args are its command line (args[0] the program name),
// and the result is the exit status. Errors are printed as
// "evrctl: <error>", with status 1.
func Execute(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &ctl{stdin: stdin, stdout: stdout, stderr: stderr}
	return commands.Execute(context.Background(), c.root(), args)
}

func (c *ctl) root() *cobra.Command {
	root := &cobra.Command{
		Use:   "evrctl",
		Short: "Administer an evrd database",
		Args:  commands.Unknown,
		RunE: func(*cobra.Command, []string) error {
			c.usage()
			return commands.ExitStatus(2)
		},
	}
	root.SetOut(c.stderr) // help and usage, where the flag package printed them
	root.SetErr(c.stderr)
	root.PersistentFlags().StringVar(&c.configPath, "config", "", "evrd config file to read the db DSN from (default $EVRD_CONFIG)")
	root.PersistentFlags().StringVar(&c.db, "db", "", "storage DSN (default: from -config, else sqlite:evrd.db)")
	// evrctl's own usage lists every command; a command's -h is Cobra's
	usage, help := root.UsageFunc(), root.HelpFunc()
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		if cmd == root {
			c.usage()
			return nil
		}
		return usage(cmd)
	})
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if cmd == root {
			c.usage()
			return
		}
		help(cmd, args)
	})
	root.AddCommand(c.versionCmd(), c.genkeyCmd(), c.userCmd(), c.historyCmd())
	return root
}

// config is evrd's config, from -config (else $EVRD_CONFIG, e.g. set in a
// container image or compose file; else the defaults), with -db over its
// db.
func (c *ctl) config() (*config.Config, error) {
	path := c.configPath
	if path == "" {
		path = os.Getenv("EVRD_CONFIG")
	}
	cfg := config.Default()
	if path != "" {
		l, err := config.Load(path)
		if err != nil {
			return nil, fmt.Errorf("load config: %v", err)
		}
		cfg = l
	}
	if c.db != "" {
		cfg.DB = c.db
	}
	return cfg, nil
}

// noFlags is a command whose arguments are all its own, even ones starting
// with "-" (a password, say), as before Cobra.
func noFlags(cmd *cobra.Command) *cobra.Command {
	cmd.DisableFlagParsing = true
	return cmd
}

func need(args []string, n int, use string) error {
	if len(args) != n {
		return fmt.Errorf("usage: evrctl %s", use)
	}
	return nil
}
