package evrctl

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/accounts"
	"github.com/4evrd/evrd/internal/config"
	"github.com/4evrd/evrd/internal/history"
	"github.com/4evrd/evrd/internal/sandbox"
	"github.com/4evrd/evrd/internal/store"
	"github.com/4evrd/evrd/internal/zncimport"
)

func (c *ctl) importZNCCmd() *cobra.Command {
	var o zncOptions
	cmd := &cobra.Command{
		Use:                   "import-znc [-dry-run] [-tz zone] <user> <znc-user-dir>",
		DisableFlagsInUseLine: true,
		Short: "Import a ZNC user's logs (e.g. ~/.znc/users/admin) into <user>'s networks of the same names; " +
			"keeps everything from then on (backlog unlimited). Stop evrd first with a pebble store, and run it once.",
		RunE: func(_ *cobra.Command, args []string) error {
			cfg, err := c.config()
			if err != nil {
				return err
			}
			return c.importZNC(cfg, o, args)
		},
	}
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "count what would be imported, write nothing")
	cmd.Flags().StringVar(&o.tz, "tz", "UTC", "the timezone ZNC wrote its logs in (its user's Timezone setting)")
	return cmd
}

// zncOptions are history import-znc's flags.
type zncOptions struct {
	dryRun bool
	tz     string
}

// importZNC loads a ZNC user's logs into an evrd user's history.
func (c *ctl) importZNC(cfg *config.Config, o zncOptions, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: evrctl history import-znc [-dry-run] [-tz zone] <user> <znc-user-dir>")
	}
	loc, err := time.LoadLocation(o.tz)
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
	acc := accounts.New(st, cfg.Limits)
	u, err := acc.UserByUsername(context.Background(), args[0])
	if err != nil {
		return fmt.Errorf("user %q: %v", args[0], err)
	}
	nets, err := acc.ListNetworks(context.Background(), u.ID)
	if err != nil {
		return fmt.Errorf("networks: %v", err)
	}
	byName := map[string]store.Network{}
	for _, n := range nets {
		byName[strings.ToLower(n.Name)] = n
	}
	files, err := zncimport.Find(args[1], loc)
	if err != nil {
		return fmt.Errorf("find logs: %v", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("no ZNC logs under %s (want …/moddata/log/<network>/<buffer>/<date>.log)", args[1])
	}

	var hist history.Store
	if !o.dryRun {
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
			if o.dryRun {
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
	if o.dryRun {
		verb = "would import"
	}
	fmt.Fprintf(c.stdout, "%s %d lines into %s\n", verb, total, u.Username)
	if !o.dryRun && (u.BacklogLines == nil || *u.BacklogLines != 0) {
		// keep it: the prune would cut every buffer back to the limit
		zero := 0
		u.BacklogLines = &zero
		if err := acc.UpdateUser(context.Background(), u); err != nil {
			return fmt.Errorf("set %s's backlog to unlimited: %v", u.Username, err)
		}
		fmt.Fprintf(c.stdout, "%s now keeps all history (backlog unlimited; change it under Admin → Users)\n", u.Username)
	}
	return nil
}
