// Package config resolves the host agent's runtime configuration: where to keep
// local state and how the disk-backed event buffer is bounded and drained. Cert
// paths, the platform endpoint, and collector toggles arrive with their
// respective slices.
package config

import (
	"os"
	"path/filepath"
	"time"
)

// Config holds the agent's resolved runtime configuration.
type Config struct {
	// StateDir is where collectors persist resumable state (e.g. the journald
	// cursor). Defaults to ~/.host-agent.
	StateDir string

	// QueueDir is the disk-backed event buffer's spool directory. Defaults to
	// <StateDir>/queue.
	QueueDir string

	// MaxQueueEvents and MaxQueueBytes bound the buffer so a stalled sink can't
	// exhaust the disk; the oldest events are dropped first. 0 = unlimited.
	MaxQueueEvents int
	MaxQueueBytes  int64

	// BatchSize is the maximum number of events drained per flush; FlushInterval
	// is how often the sink drains the buffer.
	BatchSize     int
	FlushInterval time.Duration
}

// Default returns the configuration with StateDir resolved to ~/.host-agent
// (falling back to ./.host-agent if the home directory can't be determined) and
// conservative buffer defaults.
func Default() Config {
	state := defaultStateDir()
	return Config{
		StateDir:       state,
		QueueDir:       filepath.Join(state, "queue"),
		MaxQueueEvents: 100_000,
		MaxQueueBytes:  256 << 20, // 256 MiB
		BatchSize:      256,
		FlushInterval:  time.Second,
	}
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
