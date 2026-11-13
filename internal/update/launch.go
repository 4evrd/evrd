package update

import (
	"os"
	"path/filepath"
	"syscall"

	"github.com/4evrd/evrd/internal/version"
)

// The environment an installed update runs with: which update it is, and
// the program it was installed over. They also stop it launching itself.
const (
	envRunning = "EVRD_UPDATE_RUNNING"
	envBase    = "EVRD_UPDATE_BASE"
)

// Base is the version of the program the running evrd took over from: its
// own version, unless it is an installed update.
func Base() string {
	if b := os.Getenv(envBase); os.Getenv(envRunning) != "" && b != "" {
		return b
	}
	return version.Version
}

// Running reports whether this process is an installed update.
func Running() bool { return os.Getenv(envRunning) == version.Version }

// execve is syscall.Exec; tests stand in for it.
var execve = syscall.Exec

// Launch runs the installed update's prog ("evrd" or "evrctl") in this
// process's place, with the same arguments, when one was installed over
// this program. Call it first thing. It returns when there's nothing to run
// (or exec failed): stale is then true when the update was installed over
// another version (the image was upgraded or pinned since), and no longer
// applies.
func Launch(dir, prog string, args []string) (stale bool, err error) {
	if dir == "" || os.Getenv(envRunning) != "" {
		return false, nil
	}
	s, err := readState(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if s.Base != version.Version {
		return true, nil
	}
	bin, err := filepath.Abs(filepath.Join(dir, s.Version, prog))
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(bin); err != nil {
		return false, err
	}
	return false, Exec(bin, args, s.Version, s.Base)
}

// Exec replaces this process with the update's program bin (version v,
// installed over base). It returns only on failure.
func Exec(bin string, args []string, v, base string) error {
	env := append(withoutUpdateEnv(os.Environ()), envRunning+"="+v, envBase+"="+base)
	argv := append([]string{bin}, args[1:]...)
	return execve(bin, argv, env)
}

func withoutUpdateEnv(env []string) []string {
	out := env[:0:0]
	for _, e := range env {
		if len(e) >= len(envRunning)+1 && e[:len(envRunning)+1] == envRunning+"=" {
			continue
		}
		if len(e) >= len(envBase)+1 && e[:len(envBase)+1] == envBase+"=" {
			continue
		}
		out = append(out, e)
	}
	return out
}
