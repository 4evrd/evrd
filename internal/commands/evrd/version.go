package evrd

import (
	"io"

	"github.com/spf13/cobra"
)

// newVersion is evrd version, as evrd -version.
func newVersion(stdout io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		Run:   func(*cobra.Command, []string) { printVersion(stdout) },
	}
}
