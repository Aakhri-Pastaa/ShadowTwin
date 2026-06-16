// Package collectors defines the contract every telemetry source in the host
// agent implements, and the shared event shape they all emit. The agent core
// (cmd/agent) depends only on this package, so new collectors plug in without
// changes to the wiring — the interface is defined before a second collector
// exists, not retrofitted after.
package collectors

import "context"

// Collector is the contract every telemetry source implements.
type Collector interface {
	// Name is a stable identifier for the collector, e.g. "auth.journald". It is
	// used as Event.Source and to identify the collector in operational logs.
	Name() string

	// Start begins collection and returns a channel of events. It is
	// non-blocking: collection runs in its own goroutine. The channel is closed
	// when collection stops, either because ctx was cancelled or because the
	// underlying source ended. A non-nil error means the collector could not
	// start at all (e.g. a required tool is missing, or state can't be read); in
	// that case the returned channel is nil.
	Start(ctx context.Context) (<-chan Event, error)

	// Stop blocks until the collector has fully shut down and persisted any final
	// state (such as a resume cursor). It is safe to call after ctx cancellation
	// and is idempotent. It returns the final shutdown error, if any.
	Stop() error
}
