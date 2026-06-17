package collectors

import (
	"os"
	"time"
)

// Event is the normalized shape every collector emits. Collectors translate
// their source-specific data into this struct so everything downstream — the
// buffer, the shipper, the platform — speaks one format.
type Event struct {
	// ID is a per-event UUIDv4. It exists so retries are idempotent: if an ack is
	// lost and the agent re-sends, downstream can dedupe on ID instead of
	// recording the same event twice.
	ID string `json:"id"`
	// Source is the emitting collector's Name(), e.g. "auth.journald".
	Source string `json:"source"`
	// Timestamp is when the event actually happened, as reported by the source.
	Timestamp time.Time `json:"timestamp"`
	// CollectedAt is when the agent read the event. It can lag Timestamp when the
	// agent is catching up after a restart.
	CollectedAt time.Time `json:"collected_at"`
	// Host is the hostname of the machine the agent runs on.
	Host string `json:"host"`
	// Payload holds the normalized, collector-specific fields.
	Payload map[string]any `json:"payload"`
}

// hostname is resolved once: os.Hostname is a syscall and the value does not
// change over a process's lifetime.
var hostname = resolveHostname()

func resolveHostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "unknown"
	}
	return h
}

// NewEvent builds an Event with the cross-cutting fields filled in: a fresh
// UUID, CollectedAt set to now, and the agent's hostname. Centralizing this is
// what guarantees every event has an ID — collectors only supply the parts they
// know about (source, when it happened, and the payload).
func NewEvent(source string, ts time.Time, payload map[string]any) Event {
	return Event{
		ID:          newUUID(),
		Source:      source,
		Timestamp:   ts,
		CollectedAt: time.Now().UTC(),
		Host:        hostname,
		Payload:     payload,
	}
}
