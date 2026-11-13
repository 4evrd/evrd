// Command evrd is the IRCForever IRC bouncer daemon (see
// internal/commands/evrd).
package main

import (
	"context"
	"os"

	"github.com/4evrd/evrd/internal/commands/evrd"
	"github.com/4evrd/evrd/internal/update"
)

// exit is os.Exit; the test stands in for it.
var exit = os.Exit

func main() {
	// an update installed from the web app runs instead (internal/update)
	update.Start(os.Args, "evrd", true, os.Stderr)
	exit(evrd.Execute(context.Background(), os.Args, os.Stdout, os.Stderr))
}
