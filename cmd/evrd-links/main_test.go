package main

import (
	"os"
	"strings"
	"testing"
)

// TestMainExits runs evrd-links -version as the program would, and exits
// with its status.
func TestMainExits(t *testing.T) {
	if code, out := runMain(t, "evrd-links", "-version"); code != 0 || !strings.HasPrefix(out, "evrd-links protocol ") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

// runMain runs main with args as its command line, and returns its exit
// status and what it wrote to stdout.
func runMain(t *testing.T, args ...string) (code int, stdout string) {
	f, err := os.Create(t.TempDir() + "/stdout")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	oldArgs, oldOut, oldExit := os.Args, os.Stdout, exit
	defer func() { os.Args, os.Stdout, exit = oldArgs, oldOut, oldExit }()
	os.Args, os.Stdout, code = args, f, -1
	exit = func(c int) { code = c }
	main()
	b, _ := os.ReadFile(f.Name())
	return code, string(b)
}
