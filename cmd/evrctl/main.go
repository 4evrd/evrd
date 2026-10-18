// Command evrctl administers an evrd database: mainly bootstrapping the first
// admin, and user management when the web app isn't reachable.
//
// It writes to the database directly. Suspending or deleting a user here
// signs them out of the web app at once; the node running them notices on its
// next cluster tick (within a few seconds) and drops their IRC clients and
// network connections.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"text/tabwriter"
	"time"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/version"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: evrctl [-config file | -db dsn] <command> [args]

commands:
  version                                  print evrctl version
  genkey                                   print a new secret_key (EVRD_SECRET_KEY) for encrypting saved passwords
  user add [-admin] [-email e] <name> [password]
                                           create an account (no password = SSO / client passwords only)
  user list                                list accounts
  user passwd <name> <password>            set an account's password
  user role <name> user|admin              change an account's role
  user suspend <name> [reason]             suspend an account
  user unsuspend <name>                    lift a suspension
  user delete <name>                       delete an account and everything it owns
  history copy <from> <to>                 copy all chat history between stores, e.g.
                                           pebble:/var/lib/evrd/history scylla://db1,db2/evrd
                                           (stop evrd first when <from> is a pebble store)`)
}

func main() {
	cfgPath := flag.String("config", "", "evrd config file to read the db DSN from (default $EVRD_CONFIG)")
	db := flag.String("db", "", "storage DSN (default: from -config, else sqlite:evrd.db)")
	flag.Usage = usage
	flag.Parse()
	if *cfgPath == "" {
		*cfgPath = os.Getenv("EVRD_CONFIG") // e.g. set in a container image or compose file
	}
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "version":
		fmt.Println("evrctl", version.Version)
		return
	case "genkey":
		// A key for secret_key / EVRD_SECRET_KEY.
		k := make([]byte, 32)
		if _, err := rand.Read(k); err != nil {
			fatal("%v", err)
		}
		fmt.Println(base64.StdEncoding.EncodeToString(k))
		return
	case "history":
		historyCmd(args[1:])
	case "user":
		cfg := config.Default()
		if *cfgPath != "" {
			c, err := config.Load(*cfgPath)
			if err != nil {
				fatal("load config: %v", err)
			}
			cfg = c
		}
		if *db != "" {
			cfg.DB = *db
		}
		userCmd(cfg, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "evrctl: unknown command %q\n", args[0])
		usage()
		os.Exit(2)
	}
}

func userCmd(cfg *config.Config, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	cur, oldKeys, err := cfg.SecretKeys()
	if err == nil {
		err = store.SetSecretKeys(cur, oldKeys...)
	}
	if err != nil {
		fatal("secret key: %v", err)
	}
	st, err := store.Open(cfg.DB)
	if err != nil {
		fatal("open store: %v", err)
	}
	defer st.Close()
	acc := accounts.New(st, cfg.Limits)

	find := func(name string) store.User {
		u, err := st.UserByUsername(name)
		if errors.Is(err, store.ErrNotFound) {
			fatal("no user %q", name)
		} else if err != nil {
			fatal("%v", err)
		}
		return u
	}

	switch args[0] {
	case "add":
		fs := flag.NewFlagSet("user add", flag.ExitOnError)
		admin := fs.Bool("admin", false, "make the account an admin")
		email := fs.String("email", "", "email address")
		fs.Parse(args[1:])
		rest := fs.Args()
		if len(rest) < 1 || len(rest) > 2 {
			fatal("usage: evrctl user add [-admin] [-email e] <name> [password]")
		}
		in := accounts.CreateInput{Username: rest[0], Email: *email, Role: store.RoleUser}
		if len(rest) == 2 {
			in.Password = rest[1]
		}
		if *admin {
			in.Role = store.RoleAdmin
		}
		u, err := acc.Create(in)
		if err != nil {
			fatal("create user: %v", err)
		}
		fmt.Printf("created %s %q (id %d)\n", u.Role, u.Username, u.ID)

	case "list":
		users, err := st.ListUsers()
		if err != nil {
			fatal("list users: %v", err)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
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
		need(args, 3, "user passwd <name> <password>")
		u := find(args[1])
		if err := acc.SetPassword(&u, args[2]); err != nil {
			fatal("%v", err)
		}
		_ = st.DeleteUserSessions(u.ID, "")
		fmt.Printf("password set for %q (web sessions signed out)\n", u.Username)

	case "role":
		need(args, 3, "user role <name> user|admin")
		u := find(args[1])
		if args[2] != store.RoleUser && args[2] != store.RoleAdmin {
			fatal("role must be user or admin")
		}
		if err := acc.GuardAdminChange(u, args[2] == store.RoleAdmin); err != nil {
			fatal("%v", err)
		}
		u.Role = args[2]
		if err := st.UpdateUser(u); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("%q is now %s\n", u.Username, u.Role)

	case "suspend":
		if len(args) < 2 || len(args) > 3 {
			fatal("usage: evrctl user suspend <name> [reason]")
		}
		u := find(args[1])
		if err := acc.GuardAdminChange(u, false); err != nil {
			fatal("%v", err)
		}
		u.Suspended = true
		if len(args) == 3 {
			u.SuspendedReason = args[2]
		}
		if err := st.UpdateUser(u); err != nil {
			fatal("%v", err)
		}
		_ = st.DeleteUserSessions(u.ID, "")
		fmt.Printf("suspended %q\n", u.Username)

	case "unsuspend":
		need(args, 2, "user unsuspend <name>")
		u := find(args[1])
		u.Suspended, u.SuspendedReason = false, ""
		if err := st.UpdateUser(u); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("unsuspended %q\n", u.Username)

	case "delete":
		need(args, 2, "user delete <name>")
		u := find(args[1])
		if err := acc.GuardAdminChange(u, false); err != nil {
			fatal("%v", err)
		}
		if err := st.DeleteUser(u.ID); err != nil {
			fatal("%v", err)
		}
		fmt.Printf("deleted %q (id %d)\n", u.Username, u.ID)

	default:
		fatal("unknown user subcommand %q", args[0])
	}
}

func historyCmd(args []string) {
	if len(args) != 3 || args[0] != "copy" {
		fatal("usage: evrctl history copy <from> <to>")
	}
	from, err := history.Open(args[1])
	if err != nil {
		fatal("open %s: %v", args[1], err)
	}
	defer from.Close()
	to, err := history.Open(args[2])
	if err != nil {
		fatal("open %s: %v", args[2], err)
	}
	nets, err := from.Networks()
	if err != nil {
		fatal("list networks: %v", err)
	}
	total := 0
	for _, id := range nets {
		bufs, err := from.Buffers(id)
		if err != nil {
			fatal("network %d: %v", id, err)
		}
		for _, b := range bufs {
			// Oldest first, so the destination indexes each buffer from its
			// first line.
			msgs, err := from.Recent(id, b, math.MaxInt32)
			if err != nil {
				fatal("network %d %s: %v", id, b, err)
			}
			for _, m := range msgs {
				if err := to.Add(m); err != nil {
					fatal("write: %v", err)
				}
			}
			total += len(msgs)
		}
		fmt.Printf("network %d: %d buffers\n", id, len(bufs))
	}
	if err := to.Close(); err != nil { // waits for queued writes
		fatal("finish writing: %v", err)
	}
	fmt.Printf("copied %d lines from %d networks\n", total, len(nets))
}

func need(args []string, n int, use string) {
	if len(args) != n {
		fatal("usage: evrctl %s", use)
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "evrctl: "+format+"\n", a...)
	os.Exit(1)
}
