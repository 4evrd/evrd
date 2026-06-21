// Command evrctl administers a running evrd daemon's database.
package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/crypto/bcrypt"

	"code.ircforever.org/ircforever/evrd/internal/store"
	"code.ircforever.org/ircforever/evrd/internal/version"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage: evrctl [-db dsn] <command> [args]

commands:
  version                      print evrctl version
  user add <username> <pass>   create a bouncer account
  user list                    list accounts`)
}

func main() {
	db := flag.String("db", "sqlite:evrd.db", "storage DSN")
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
		return
	case "user":
		userCmd(*db, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "evrctl: unknown command %q\n", args[0])
		usage()
		os.Exit(2)
	}
}

func userCmd(db string, args []string) {
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	st, err := store.Open(db)
	if err != nil {
		fatal("open store: %v", err)
	}
	defer st.Close()

	switch args[0] {
	case "add":
		if len(args) != 3 {
			fatal("usage: evrctl user add <username> <password>")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(args[2]), bcrypt.DefaultCost)
		if err != nil {
			fatal("hash password: %v", err)
		}
		u, err := st.CreateUser(args[1], string(hash))
		if err != nil {
			fatal("create user: %v", err)
		}
		fmt.Printf("created user %q (id %d)\n", u.Username, u.ID)
	case "list":
		users, err := st.ListUsers()
		if err != nil {
			fatal("list users: %v", err)
		}
		for _, u := range users {
			fmt.Printf("%d\t%s\n", u.ID, u.Username)
		}
	default:
		fatal("unknown user subcommand %q", args[0])
	}
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "evrctl: "+format+"\n", a...)
	os.Exit(1)
}
