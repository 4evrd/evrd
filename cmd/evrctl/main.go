// Command evrctl administers a running evrd daemon.
package main

import (
	"flag"
	"fmt"
	"os"

	"code.ircforever.org/ircforever/evrd/internal/version"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: evrctl <command> [args]

commands:
  version              print evrctl version
  status               show daemon status (todo)
  user list|add|del    manage users (todo)
  network ...          manage a user's networks (todo)`)
}

func main() {
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	switch args[0] {
	case "version":
		fmt.Println("evrctl", version.Version)
	case "status", "user", "network":
		fmt.Fprintf(os.Stderr, "evrctl: %q is not implemented yet\n", args[0])
		os.Exit(1)
	default:
		fmt.Fprintf(os.Stderr, "evrctl: unknown command %q\n", args[0])
		usage()
		os.Exit(2)
	}
}
