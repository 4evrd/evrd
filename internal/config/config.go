// Package config loads evrd's runtime configuration.
package config

import (
	"bufio"
	"os"
	"strings"
)

// Config is evrd's runtime configuration.
type Config struct {
	// Listen holds listener URLs, e.g. "ircs://:6697",
	// "irc+insecure://127.0.0.1:6667", "wss://:8097", "unix:///run/evrd.sock".
	Listen []string
	// DB is the storage DSN, e.g. "sqlite:/var/lib/evrd/evrd.db".
	DB string
}

// Default returns a config with sensible defaults.
func Default() *Config {
	return &Config{
		Listen: []string{"irc+insecure://127.0.0.1:6667"},
		DB:     "sqlite:evrd.db",
	}
}

// Load reads a simple "directive value" config file. Blank lines and lines
// starting with '#' are ignored. Recognised directives: listen, db.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	cfg := Default()
	cfg.Listen = nil

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, _ := strings.Cut(line, " ")
		val = strings.TrimSpace(val)
		switch key {
		case "listen":
			cfg.Listen = append(cfg.Listen, val)
		case "db":
			cfg.DB = val
		}
	}
	if len(cfg.Listen) == 0 {
		cfg.Listen = Default().Listen
	}
	return cfg, sc.Err()
}
