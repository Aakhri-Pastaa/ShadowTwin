// Package config resolves the host agent's runtime configuration. Today that is
// just where to keep local state; cert paths, the platform endpoint, and
// collector toggles arrive with their respective slices.
package config

import (
	"os"
	"path/filepath"
)

// Config holds the agent's resolved runtime configuration.
type Config struct {
	// StateDir is where collectors persist resumable state (e.g. the journald
	// cursor). Defaults to ~/.host-agent.
	StateDir string
}

// Default returns the configuration with StateDir resolved to ~/.host-agent,
// falling back to ./.host-agent if the home directory can't be determined.
func Default() Config {
	return Config{StateDir: defaultStateDir()}
}

func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".host-agent"
	}
	return filepath.Join(home, ".host-agent")
}

// EnsureStateDir creates the state directory if it doesn't exist. Mode 0700
// keeps cursor and other state files readable only by the agent's user.
func (c Config) EnsureStateDir() error {
	return os.MkdirAll(c.StateDir, 0o700)
}
