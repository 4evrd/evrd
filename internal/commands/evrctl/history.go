package evrctl

import (
	"errors"
	"fmt"
	"math"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/history"
)

const historyUsage = "usage: evrctl history copy <from> <to>"

func (c *ctl) historyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "history",
		Short: "Copy chat history between stores, or import ZNC's logs",
		Args:  cobra.ArbitraryArgs,
		RunE:  func(*cobra.Command, []string) error { return errors.New(historyUsage) },
	}
	cmd.AddCommand(c.historyCopyCmd(), c.importZNCCmd())
	return cmd
}

func (c *ctl) historyCopyCmd() *cobra.Command {
	return noFlags(&cobra.Command{
		Use:   "copy <from> <to>",
		Short: "Copy all chat history between stores (stop evrd first when <from> is a pebble store)",
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) != 2 {
				return errors.New(historyUsage)
			}
			return c.copyHistory(args[0], args[1])
		},
	})
}

func (c *ctl) copyHistory(fromDSN, toDSN string) error {
	from, err := history.Open(fromDSN)
	if err != nil {
		return fmt.Errorf("open %s: %v", fromDSN, err)
	}
	defer from.Close()
	to, err := history.Open(toDSN)
	if err != nil {
		return fmt.Errorf("open %s: %v", toDSN, err)
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
