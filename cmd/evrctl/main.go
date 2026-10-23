// Command evrctl administers an evrd database (see
// internal/commands/evrctl).
package main

import (
	"os"

	"github.com/4evrd/evrd/internal/commands/evrctl"
)

// exit is os.Exit; the test stands in for it.
var exit = os.Exit

func main() {
	exit(evrctl.Execute(os.Args, os.Stdin, os.Stdout, os.Stderr))
}
