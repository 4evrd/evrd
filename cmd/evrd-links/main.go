// Command evrd-links is evrd's connection holder (see
// internal/commands/evrdlinks).
package main

import (
	"context"
	"os"

	"github.com/4evrd/evrd/internal/commands/evrdlinks"
)

// exit is os.Exit; the test stands in for it.
var exit = os.Exit

func main() {
	exit(evrdlinks.Execute(context.Background(), os.Args, os.Stdout, os.Stderr))
}
