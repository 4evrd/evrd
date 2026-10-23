package evrctl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/auth"
	"github.com/4evrd/evrd/internal/commands"
	"github.com/4evrd/evrd/internal/sandbox"
	"github.com/4evrd/evrd/internal/store"
)

// users is what the user commands work with: the accounts in the database
// evrd's config names.
type users struct {
	acc      *accounts.Service
	sessions auth.SessionServiceImpl
}

// find is the user called name.
func (us *users) find(name string) (store.User, error) {
	u, err := us.acc.UserByUsername(context.Background(), name)
	if errors.Is(err, store.ErrNotFound) {
		return u, fmt.Errorf("no user %q", name)
	}
	return u, err
}

// withUsers runs a user command: it opens the database, then calls run with
// the command's arguments.
func (c *ctl) withUsers(run func(us *users, args []string) error) func(*cobra.Command, []string) error {
	return func(_ *cobra.Command, args []string) error {
		cfg, err := c.config()
		if err != nil {
			return err
		}
		cur, oldKeys, err := cfg.SecretKeys()
		if err == nil {
			err = store.SetSecretKeys(cur, oldKeys...)
		}
		if err != nil {
			return fmt.Errorf("secret key: %v", err)
		}
		// Run as evrd's user (when started as root), so a database this
		// creates is evrd's to write.
		if err := sandbox.Drop(cfg.User); err != nil {
			return fmt.Errorf("user: %v", err)
		}
		st, err := store.Open(cfg.DB)
		if err != nil {
			return fmt.Errorf("open store: %v", err)
		}
		defer st.Close()
		return run(&users{acc: accounts.New(st, cfg.Limits), sessions: auth.NewSessionService(st)}, args)
	}
}

func (c *ctl) userCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "user",
		Short: "Manage accounts",
		Args:  cobra.ArbitraryArgs,
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				c.usage()
				return commands.ExitStatus(2)
			}
			return c.withUsers(func(*users, []string) error {
				return fmt.Errorf("unknown user subcommand %q", args[0])
			})(nil, args)
		},
	}
	cmd.AddCommand(c.userAddCmd(), c.userListCmd(), c.userPasswdCmd(), c.userRoleCmd(),
		c.userSuspendCmd(), c.userUnsuspendCmd(), c.userDeleteCmd())
	return cmd
}

func (c *ctl) userAddCmd() *cobra.Command {
	var admin bool
	var email string
	cmd := &cobra.Command{
		Use:                   "add [-admin] [-email e] <name> [password]",
		DisableFlagsInUseLine: true,
		Short:                 `Create an account (no password = SSO / client passwords only; "-" reads it from stdin)`,
		RunE: c.withUsers(func(us *users, args []string) error {
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("usage: evrctl user add [-admin] [-email e] <name> [password]")
			}
			in := accounts.CreateInput{Username: args[0], Email: email, Role: store.RoleUser}
			if len(args) == 2 {
				in.Password = args[1]
			}
			if in.Password == "-" { // from stdin, kept out of ps and shell history
				line, err := bufio.NewReader(c.stdin).ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("read password: %v", err)
				}
				in.Password = strings.TrimRight(line, "\r\n")
			}
			if admin {
				in.Role = store.RoleAdmin
			}
			u, err := us.acc.Create(in)
			if err != nil {
				return fmt.Errorf("create user: %v", err)
			}
			fmt.Fprintf(c.stdout, "created %s %q (id %d)\n", u.Role, u.Username, u.ID)
			return nil
		}),
	}
	cmd.Flags().BoolVar(&admin, "admin", false, "make the account an admin")
	cmd.Flags().StringVar(&email, "email", "", "email address")
	return cmd
}

func (c *ctl) userListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List accounts",
		RunE: c.withUsers(func(us *users, _ []string) error {
			all, err := us.acc.ListUsers(context.Background())
			if err != nil {
				return fmt.Errorf("list users: %v", err)
			}
			tw := tabwriter.NewWriter(c.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tUSERNAME\tROLE\tSIGN-IN\tSTATUS\tLAST SIGN-IN")
			for _, u := range all {
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
			return tw.Flush()
		}),
	}
}

func (c *ctl) userPasswdCmd() *cobra.Command {
	return noFlags(&cobra.Command{
		Use:   "passwd <name> <password>",
		Short: "Set an account's password",
		RunE: c.withUsers(func(us *users, args []string) error {
			if err := need(args, 2, "user passwd <name> <password>"); err != nil {
				return err
			}
			u, err := us.find(args[0])
			if err != nil {
				return err
			}
			if err := us.acc.SetPassword(&u, args[1]); err != nil {
				return err
			}
			_ = us.sessions.DeleteUserSessions(context.Background(), u.ID, "")
			fmt.Fprintf(c.stdout, "password set for %q (web sessions signed out)\n", u.Username)
			return nil
		}),
	})
}

func (c *ctl) userRoleCmd() *cobra.Command {
	return noFlags(&cobra.Command{
		Use:   "role <name> user|admin",
		Short: "Change an account's role",
		RunE: c.withUsers(func(us *users, args []string) error {
			if err := need(args, 2, "user role <name> user|admin"); err != nil {
				return err
			}
			u, err := us.find(args[0])
			if err != nil {
				return err
			}
			if args[1] != store.RoleUser && args[1] != store.RoleAdmin {
				return fmt.Errorf("role must be user or admin")
			}
			if err := us.acc.GuardAdminChange(u, args[1] == store.RoleAdmin); err != nil {
				return err
			}
			u.Role = args[1]
			if err := us.acc.UpdateUser(context.Background(), u); err != nil {
				return err
			}
			fmt.Fprintf(c.stdout, "%q is now %s\n", u.Username, u.Role)
			return nil
		}),
	})
}

func (c *ctl) userSuspendCmd() *cobra.Command {
	return noFlags(&cobra.Command{
		Use:   "suspend <name> [reason]",
		Short: "Suspend an account",
		RunE: c.withUsers(func(us *users, args []string) error {
			if len(args) < 1 || len(args) > 2 {
				return fmt.Errorf("usage: evrctl user suspend <name> [reason]")
			}
			u, err := us.find(args[0])
			if err != nil {
				return err
			}
			if err := us.acc.GuardAdminChange(u, false); err != nil {
				return err
			}
			u.Suspended = true
			if len(args) == 2 {
				u.SuspendedReason = args[1]
			}
			if err := us.acc.UpdateUser(context.Background(), u); err != nil {
				return err
			}
			_ = us.sessions.DeleteUserSessions(context.Background(), u.ID, "")
			fmt.Fprintf(c.stdout, "suspended %q\n", u.Username)
			return nil
		}),
	})
}

func (c *ctl) userUnsuspendCmd() *cobra.Command {
	return noFlags(&cobra.Command{
		Use:   "unsuspend <name>",
		Short: "Lift a suspension",
		RunE: c.withUsers(func(us *users, args []string) error {
			if err := need(args, 1, "user unsuspend <name>"); err != nil {
				return err
			}
			u, err := us.find(args[0])
			if err != nil {
				return err
			}
			u.Suspended, u.SuspendedReason = false, ""
			if err := us.acc.UpdateUser(context.Background(), u); err != nil {
				return err
			}
			fmt.Fprintf(c.stdout, "unsuspended %q\n", u.Username)
			return nil
		}),
	})
}

func (c *ctl) userDeleteCmd() *cobra.Command {
	return noFlags(&cobra.Command{
		Use:   "delete <name>",
		Short: "Delete an account and everything it owns",
		RunE: c.withUsers(func(us *users, args []string) error {
			if err := need(args, 1, "user delete <name>"); err != nil {
				return err
			}
			u, err := us.find(args[0])
			if err != nil {
				return err
			}
			if err := us.acc.GuardAdminChange(u, false); err != nil {
				return err
			}
			if err := us.acc.DeleteUser(context.Background(), u.ID); err != nil {
				return err
			}
			fmt.Fprintf(c.stdout, "deleted %q (id %d)\n", u.Username, u.ID)
			return nil
		}),
	})
}
