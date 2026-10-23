// Command evrctl administers an evrd database: mainly bootstrapping the first
// admin, and user management when the web app isn't reachable.
//
// It writes to the database directly. Suspending or deleting a user here
// signs them out of the web app at once; the node running them notices on its
// next cluster tick (within a few seconds) and drops their IRC clients and
// network connections.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"sort"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/sandbox"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
	"github.com/4evrd/evrd/internal/zncimport"
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

// ctl is one run of evrctl: where it reads and writes.
type ctl struct {
	stdin          io.Reader
	stdout, stderr io.Writer
}

// exitStatus ends evrctl with a status, anything to say already said.
type exitStatus int

func (e exitStatus) Error() string { return fmt.Sprintf("exit status %d", int(e)) }

// flagStatus is how a flag parse error ends evrctl, as flag.ExitOnError
// would: 0 for -h, else 2 (the flag package has printed the error).
func flagStatus(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return exitStatus(0)
	}
	return exitStatus(2)
}

func main() {
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

// run is evrctl: args are its command line (args[0] the program name), and
// the result is the exit status. Errors are printed as "evrctl: <error>",
// with status 1.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &ctl{stdin: stdin, stdout: stdout, stderr: stderr}
	err := c.main(args)
	var st exitStatus
	switch {
	case err == nil:
		return 0
	case errors.As(err, &st):
		return int(st)
	default:
		fmt.Fprintf(stderr, "evrctl: %v\n", err)
		return 1
	}
}

func (c *ctl) main(argv []string) error {
	flags := flag.NewFlagSet(argv[0], flag.ContinueOnError)
	flags.SetOutput(c.stderr)
	cfgPath := flags.String("config", "", "evrd config file to read the db DSN from (default $EVRD_CONFIG)")
	db := flags.String("db", "", "storage DSN (default: from -config, else sqlite:evrd.db)")
	flags.Usage = c.usage
	if err := flags.Parse(argv[1:]); err != nil {
		return flagStatus(err)
	}
	if *cfgPath == "" {
		*cfgPath = os.Getenv("EVRD_CONFIG") // e.g. set in a container image or compose file
	}
	args := flags.Args()
	if len(args) == 0 {
		c.usage()
		return exitStatus(2)
	}

	switch args[0] {
	case "version":
		fmt.Fprintln(c.stdout, "evrctl", version.Version)
		return nil
	case "genkey":
		// A key for secret_key / EVRD_SECRET_KEY.
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			return err
		}
		fmt.Fprintln(c.stdout, base64.StdEncoding.EncodeToString(k))
		return nil
	case "history":
		if len(args) > 1 && args[1] == "import-znc" {
			cfg := config.Default()
			if *cfgPath != "" {
				l, err := config.Load(*cfgPath)
				if err != nil {
					return fmt.Errorf("load config: %v", err)
				}
				cfg = l
			}
			if *db != "" {
				cfg.DB = *db
			}
			return c.importZNC(cfg, args[2:])
		}
		return c.historyCmd(args[1:])
	case "user":
		cfg := config.Default()
		if *cfgPath != "" {
			l, err := config.Load(*cfgPath)
			if err != nil {
				return fmt.Errorf("load config: %v", err)
			}
			cfg = l
		}
		if *db != "" {
			cfg.DB = *db
		}
		return c.userCmd(cfg, args[1:])
	default:
		fmt.Fprintf(c.stderr, "evrctl: unknown command %q\n", args[0])
		c.usage()
		return exitStatus(2)
	}
}

func (c *ctl) userCmd(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		c.usage()
		return exitStatus(2)
	}
	cur, oldKeys, err := cfg.SecretKeys()
	if err == nil {
		err = store.SetSecretKeys(cur, oldKeys...)
	}
	if err != nil {
		return fmt.Errorf("secret key: %v", err)
	}
	// Run as evrd's user (when started as root), so a database this creates
	// is evrd's to write.
	if err := sandbox.Drop(cfg.User); err != nil {
		return fmt.Errorf("user: %v", err)
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		return fmt.Errorf("open store: %v", err)
	}
	defer st.Close()
	acc := accounts.New(st, cfg.Limits)

	find := func(name string) (store.User, error) {
		u, err := st.UserByUsername(context.Background(), name)
		if errors.Is(err, store.ErrNotFound) {
			return u, fmt.Errorf("no user %q", name)
		}
		return u, err
	}

	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("user add", flag.ContinueOnError)
		fs.SetOutput(c.stderr)
		admin := fs.Bool("admin", false, "make the account an admin")
		email := fs.String("email", "", "email address")
		if err := fs.Parse(args[1:]); err != nil {
			return flagStatus(err)
		}
		rest := fs.Args()
		if len(rest) < 1 || len(rest) > 2 {
			return fmt.Errorf("usage: evrctl user add [-admin] [-email e] <name> [password]")
		}
		in := accounts.CreateInput{Username: rest[0], Email: *email, Role: store.RoleUser}
		if len(rest) == 2 {
			in.Password = rest[1]
		}
		if in.Password == "-" { // from stdin, kept out of ps and shell history
			line, err := bufio.NewReader(c.stdin).ReadString('\n')
			if err != nil && line == "" {
				return fmt.Errorf("read password: %v", err)
			}
			in.Password = strings.TrimRight(line, "\r\n")
		}
		if *admin {
			in.Role = store.RoleAdmin
		}
		u, err := acc.Create(in)
		if err != nil {
			return fmt.Errorf("create user: %v", err)
		}
		fmt.Fprintf(c.stdout, "created %s %q (id %d)\n", u.Role, u.Username, u.ID)

	case "list":
		users, err := st.ListUsers(context.Background())
		if err != nil {
			return fmt.Errorf("list users: %v", err)
		}
		tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tUSERNAME\tROLE\tSIGN-IN\tSTATUS\tLAST SIGN-IN")
		for _, u := range users {
			signin := "password"
			if u.OIDCSubject != nil {
				signin = "sso"
			}
			status := "active"
			if u.Suspended {
				status = "suspended"
			}
			last := "never"
			if u.LastLoginAt != nil {
				last = u.LastLoginAt.Format(time.DateTime)
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", u.ID, u.Username, u.Role, signin, status, last)
		}
		tw.Flush()

	case "passwd":
		if err := need(args, 3, "user passwd <name> <password>"); err != nil {
			return err
		}
		u, err := find(args[1])
		if err != nil {
			return err
		}
		if err := acc.SetPassword(&u, args[2]); err != nil {
			return err
		}
		_ = st.DeleteUserSessions(context.Background(), u.ID, "")
		fmt.Fprintf(c.stdout, "password set for %q (web sessions signed out)\n", u.Username)

	case "role":
		if err := need(args, 3, "user role <name> user|admin"); err != nil {
			return err
		}
		u, err := find(args[1])
		if err != nil {
			return err
		}
		if args[2] != store.RoleUser && args[2] != store.RoleAdmin {
			return fmt.Errorf("role must be user or admin")
		}
		if err := acc.GuardAdminChange(u, args[2] == store.RoleAdmin); err != nil {
			return err
		}
		u.Role = args[2]
		if err := st.UpdateUser(context.Background(), u); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "%q is now %s\n", u.Username, u.Role)

	case "suspend":
		if len(args) < 2 || len(args) > 3 {
			return fmt.Errorf("usage: evrctl user suspend <name> [reason]")
		}
		u, err := find(args[1])
		if err != nil {
			return err
		}
		if err := acc.GuardAdminChange(u, false); err != nil {
			return err
		}
		u.Suspended = true
		if len(args) == 3 {
			u.SuspendedReason = args[2]
		}
		if err := st.UpdateUser(context.Background(), u); err != nil {
			return err
		}
		_ = st.DeleteUserSessions(context.Background(), u.ID, "")
		fmt.Fprintf(c.stdout, "suspended %q\n", u.Username)

	case "unsuspend":
		if err := need(args, 2, "user unsuspend <name>"); err != nil {
			return err
		}
		u, err := find(args[1])
		if err != nil {
			return err
		}
		u.Suspended, u.SuspendedReason = false, ""
		if err := st.UpdateUser(context.Background(), u); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "unsuspended %q\n", u.Username)

	case "delete":
		if err := need(args, 2, "user delete <name>"); err != nil {
			return err
		}
		u, err := find(args[1])
		if err != nil {
			return err
		}
		if err := acc.GuardAdminChange(u, false); err != nil {
			return err
		}
		if err := st.DeleteUser(context.Background(), u.ID); err != nil {
			return err
		}
		fmt.Fprintf(c.stdout, "deleted %q (id %d)\n", u.Username, u.ID)

	default:
		return fmt.Errorf("unknown user subcommand %q", args[0])
	}
	return nil
}

func (c *ctl) historyCmd(args []string) error {
	if len(args) != 3 || args[0] != "copy" {
		return fmt.Errorf("usage: evrctl history copy <from> <to>")
	}
	from, err := history.Open(args[1])
	if err != nil {
		return fmt.Errorf("open %s: %v", args[1], err)
	}
	defer from.Close()
	to, err := history.Open(args[2])
	if err != nil {
		return fmt.Errorf("open %s: %v", args[2], err)
	}
	nets, err := from.Networks()
	if err != nil {
		return fmt.Errorf("list networks: %v", err)
	}
	total := 0
	for _, id := range nets {
		bufs, err := from.Buffers(id)
		if err != nil {
			return fmt.Errorf("network %d: %v", id, err)
		}
		for _, b := range bufs {
			// Oldest first, so the destination indexes each buffer from its
			// first line.
			msgs, err := from.Recent(id, b, math.MaxInt32)
			if err != nil {
				return fmt.Errorf("network %d %s: %v", id, b, err)
			}
			for _, m := range msgs {
				if err := to.Add(m); err != nil {
					return fmt.Errorf("write: %v", err)
				}
			}
			total += len(msgs)
		}
		fmt.Fprintf(c.stdout, "network %d: %d buffers\n", id, len(bufs))
	}
	if err := to.Close(); err != nil { // waits for queued writes
		return fmt.Errorf("finish writing: %v", err)
	}
	fmt.Fprintf(c.stdout, "copied %d lines from %d networks\n", total, len(nets))
	return nil
}

func need(args []string, n int, use string) error {
	if len(args) != n {
		return fmt.Errorf("usage: evrctl %s", use)
	}
	return nil
}

// importZNC loads a ZNC user's logs into an evrd user's history.
func (c *ctl) importZNC(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("history import-znc", flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	dry := fs.Bool("dry-run", false, "count what would be imported, write nothing")
	tz := fs.String("tz", "UTC", "the timezone ZNC wrote its logs in (its user's Timezone setting)")
	if err := fs.Parse(args); err != nil {
		return flagStatus(err)
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("usage: evrctl history import-znc [-dry-run] [-tz zone] <user> <znc-user-dir>")
	}
	loc, err := time.LoadLocation(*tz)
	if err != nil {
		return fmt.Errorf("timezone: %v", err)
	}
	cur, oldKeys, err := cfg.SecretKeys()
	if err == nil {
		err = store.SetSecretKeys(cur, oldKeys...)
	}
	if err != nil {
		return fmt.Errorf("secret key: %v", err)
	}
	if err := sandbox.Drop(cfg.User); err != nil {
		return fmt.Errorf("user: %v", err)
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		return fmt.Errorf("open store: %v", err)
	}
	defer st.Close()
	u, err := st.UserByUsername(context.Background(), fs.Arg(0))
	if err != nil {
		return fmt.Errorf("user %q: %v", fs.Arg(0), err)
	}
	nets, err := st.ListNetworks(context.Background(), u.ID)
	if err != nil {
		return fmt.Errorf("networks: %v", err)
	}
	byName := map[string]store.Network{}
	for _, n := range nets {
		byName[strings.ToLower(n.Name)] = n
	}
	files, err := zncimport.Find(fs.Arg(1), loc)
	if err != nil {
		return fmt.Errorf("find logs: %v", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no ZNC logs under %s (want …/moddata/log/<network>/<buffer>/<date>.log)", fs.Arg(1))
	}

	var hist history.Store
	if !*dry {
		if hist, err = history.Open(cfg.History); err != nil {
			return fmt.Errorf("open history (is evrd still running?): %v", err)
		}
	}
	lines, skipped := map[string]int{}, map[string]int{}
	total := 0
	for _, f := range files {
		n, ok := byName[strings.ToLower(f.Network)]
		if !ok {
			skipped[f.Network]++
			continue
		}
		c, err := zncimport.Read(f, func(m history.Message) error {
			if *dry {
				return nil
			}
			m.NetworkID = n.ID
			return hist.Add(m)
		})
		if err != nil {
			return fmt.Errorf("%s: %v", f.Path, err)
		}
		lines[n.Name] += c
		total += c
	}
	if hist != nil {
		if err := hist.Close(); err != nil {
			return fmt.Errorf("finish writing: %v", err)
		}
	}
	names := make([]string, 0, len(lines))
	for k := range lines {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		fmt.Fprintf(c.stdout, "%-20s %9d lines\n", k, lines[k])
	}
	for k, n := range skipped {
		fmt.Fprintf(c.stdout, "%-20s skipped: %s has no network by that name (%d days of logs)\n", k, u.Username, n)
	}
	verb := "imported"
	if *dry {
		verb = "would import"
	}
	fmt.Fprintf(c.stdout, "%s %d lines into %s\n", verb, total, u.Username)
	if !*dry && (u.BacklogLines == nil || *u.BacklogLines != 0) {
		// keep it: the prune would cut every buffer back to the limit
		zero := 0
		u.BacklogLines = &zero
		if err := st.UpdateUser(context.Background(), u); err != nil {
			return fmt.Errorf("set %s's backlog to unlimited: %v", u.Username, err)
		}
		fmt.Fprintf(c.stdout, "%s now keeps all history (backlog unlimited; change it under Admin → Users)\n", u.Username)
	}
	return nil
}
