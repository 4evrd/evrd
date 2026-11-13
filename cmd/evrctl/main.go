// Command evrctl administers an evrd database (see
// internal/commands/evrctl).
package main

import (
	"os"

	"github.com/4evrd/evrd/internal/update"

	"github.com/4evrd/evrd/internal/commands/evrctl"
)

// exit is os.Exit; the test stands in for it.
var exit = os.Exit

func main() {
	// an update installed from the web app runs instead (internal/update)
	update.Start(os.Args, "evrctl", false, os.Stderr)
	exit(evrctl.Execute(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
