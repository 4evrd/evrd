package evrd

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/4evrd/evrd/internal/commands"
)

// newServe is evrd serve: what evrd does with no command.
func newServe(o *options, stderr io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the bouncer (the default)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return commands.Status(serve(c.Context(), o.configPath(), stderr))
		},
	}
}
