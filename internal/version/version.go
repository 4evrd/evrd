// Package version holds the build version, set via -ldflags at build time:
//
//	-ldflags "-X code.ircforever.org/ircforever/evrd/internal/version.Version=v1.2.3"
package version

// Version is the running build's version ("dev" for un-stamped local builds).
var Version = "dev"
