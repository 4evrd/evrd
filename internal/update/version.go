package update

import (
	"strconv"
	"strings"
)

// semver is a parsed X.Y.Z[-pre] version.
type semver struct {
	n   [3]int
	pre string
}

// parse reads "v1.2.3", "1.2.3" or "1.2.3-rc.1" (build metadata after "+"
// is ignored). ok is false for anything else, such as "dev".
func parse(v string) (s semver, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	v, s.pre, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 || (len(p) > 1 && p[0] == '0') {
			return semver{}, false
		}
		s.n[i] = n
	}
	return s, true
}

// Newer reports whether version a is later than b. Versions that don't
// parse (a development build) are never newer, and nothing is newer than
// them.
func Newer(a, b string) bool {
	x, ok1 := parse(a)
	y, ok2 := parse(b)
	if !ok1 || !ok2 {
		return false
	}
	for i := range x.n {
		if x.n[i] != y.n[i] {
			return x.n[i] > y.n[i]
		}
	}
	// 1.2.3 is later than 1.2.3-rc.1
	switch {
	case x.pre == y.pre:
		return false
	case x.pre == "":
		return true
	case y.pre == "":
		return false
	}
	return x.pre > y.pre
}

// Valid reports whether v is a release version.
func Valid(v string) bool {
	_, ok := parse(v)
	return ok
}

// clean is v without a leading "v".
func clean(v string) string { return strings.TrimPrefix(strings.TrimSpace(v), "v") }
