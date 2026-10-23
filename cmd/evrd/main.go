// Command evrd is the IRCForever IRC bouncer daemon (see
// internal/commands/evrd).
package main

import (
	"context"
	"os"

	"github.com/4evrd/evrd/internal/commands/evrd"
)

// exit is os.Exit; the test stands in for it.
var exit = os.Exit

func main() {
	exit(evrd.Execute(context.Background(), os.Args, os.Stdout, os.Stderr))
}
