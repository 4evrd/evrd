package update

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/4evrd/evrd/internal/config"
)

// Start is Launch for a command line: it finds the update directory from
// args' -config (else $EVRD_CONFIG) and the environment, and runs the
// installed update's prog instead of this program when there is one. With
// clear (evrd, not evrctl), a stale update is removed. It returns when this
// program should carry on.
func Start(args []string, prog string, clear bool, stderr io.Writer) {
	dir := Dir(args)
	stale, err := Launch(dir, prog, args)
	if err != nil {
		fmt.Fprintf(stderr, "%s: can't run the installed update in %s, running %s instead: %v\n", prog, dir, Base(), err)
		return
	}
	if stale && clear {
		if s, err := readState(dir); err == nil {
			fmt.Fprintf(stderr, "%s: the update to %s was installed over %s, but this is %s: clearing it\n", prog, s.Version, s.Base, Base())
		}
		Clear(dir)
	}
}

// Dir is the update directory for a command line: update_dir from its
// config file (-config, else $EVRD_CONFIG) and the environment. "" when
// updates are off or it runs chrooted.
func Dir(args []string) string {
	path := os.Getenv("EVRD_CONFIG")
	for i := 1; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			break
		}
		for _, f := range []string{"-config", "--config"} {
			switch {
			case a == f && i+1 < len(args):
				path = args[i+1]
			case strings.HasPrefix(a, f+"="):
				path = strings.TrimPrefix(a, f+"=")
			}
		}
	}
	cfg := config.Default()
	if path != "" {
		c, err := config.LoadFile(path)
		if err != nil {
			return "" // evrd itself says what's wrong with it
		}
		cfg = c
	}
	if cfg.ApplyEnv() != nil || cfg.Chroot != "" || !cfg.SelfUpdate {
		return ""
	}
	return cfg.UpdateDir
}
