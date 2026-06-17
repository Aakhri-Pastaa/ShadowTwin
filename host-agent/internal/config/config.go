// Package config resolves the host agent's runtime configuration: where to keep
// local state, how the disk-backed event buffer is bounded and drained, the
// platform endpoints and enrollment token, and the mTLS certificate directory.
// Per-collector toggles arrive with their respective slices.
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

	// IngestEndpoint is the platform URL the shipper POSTs event batches to.
	IngestEndpoint string
	// EnrollEndpoint is the platform URL the agent sends its CSR to on first run.
	EnrollEndpoint string
	// EnrollToken is the one-time bootstrap token that authorizes enrollment.
	EnrollToken string
	// CertDir holds the agent's mTLS key/cert and the platform CA. Defaults to
	// <StateDir>/certs.
	CertDir string
	// BackoffBase and BackoffCap bound the shipper's retry backoff.
	BackoffBase time.Duration
	BackoffCap  time.Duration
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
		IngestEndpoint: "https://localhost:8443/ingest",
		EnrollEndpoint: "https://localhost:8443/enroll",
		CertDir:        filepath.Join(state, "certs"),
		BackoffBase:    time.Second,
		BackoffCap:     time.Minute,
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

// EnsureCertDir creates the certificate directory if it doesn't exist. Mode
// 0700 keeps the agent's private key readable only by its user.
func (c Config) EnsureCertDir() error {
	return os.MkdirAll(c.CertDir, 0o700)
}

// Load returns Default() with recognized environment variables applied. This is
// how the agent picks up the platform endpoints and enrollment token from .env
// (sourced into the environment) without parsing files itself.
func Load() Config {
	c := Default()
	if v := os.Getenv("HOST_AGENT_STATE_DIR"); v != "" {
		c.StateDir = v
		c.QueueDir = filepath.Join(v, "queue")
		c.CertDir = filepath.Join(v, "certs")
	}
	if v := os.Getenv("INGEST_ENDPOINT"); v != "" {
		c.IngestEndpoint = v
	}
	if v := os.Getenv("AGENT_ENROLL_ENDPOINT"); v != "" {
		c.EnrollEndpoint = v
	}
	if v := os.Getenv("AGENT_ENROLL_TOKEN"); v != "" {
		c.EnrollToken = v
	}
	return c
}
