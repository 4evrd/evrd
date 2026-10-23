package evrctl

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"

	"github.com/spf13/cobra"
)

// genkeyCmd prints a key for secret_key / EVRD_SECRET_KEY.
func (c *ctl) genkeyCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "genkey",
		Short: "Print a new secret_key (EVRD_SECRET_KEY) for encrypting saved passwords",
		RunE: func(*cobra.Command, []string) error {
			k := make([]byte, 32)
			if _, err := rand.Read(k); err != nil {
				return err
			}
			fmt.Fprintln(c.stdout, base64.StdEncoding.EncodeToString(k))
			return nil
		},
	}
}
