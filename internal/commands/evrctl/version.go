package evrctl

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/version"
)

func (c *ctl) versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print evrctl version",
		Run: func(*cobra.Command, []string) {
			fmt.Fprintln(c.stdout, "evrctl", version.Version)
		},
	}
}
