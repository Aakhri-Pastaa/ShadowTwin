// Command agent is the ShadowTwin host agent: it runs telemetry collectors,
// persists every event to a durable on-disk buffer, and ships batches to the
// platform over mutually authenticated TLS. On first run it enrolls (generates
// a key pair locally and exchanges a CSR + one-time token for a client cert).
// Operational logging goes to stderr. With -dry-run the agent skips enrollment
// and prints events to stdout instead of shipping them — handy for debugging.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/buffer"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors/auth"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/config"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/enroll"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/transport"
)

// deliverFunc ships one batch of events. A nil error means delivered (ack it);
// otherwise the error wraps transport.ErrPermanent or transport.ErrRetryable.
type deliverFunc func(context.Context, []collectors.Event) error

func main() {
	dryRun := flag.Bool("dry-run", false, "print events to stdout instead of shipping them to the platform over mTLS")
	flag.Parse()

	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg := config.Load()
	if err := cfg.EnsureStateDir(); err != nil {
		log.Fatalf("cannot create state dir %q: %v", cfg.StateDir, err)
	}

	q, err := buffer.Open(cfg.QueueDir, buffer.Limits{
		MaxEvents: cfg.MaxQueueEvents,
		MaxBytes:  cfg.MaxQueueBytes,
	})
	if err != nil {
		log.Fatalf("cannot open event buffer: %v", err)
	}

	// Cancelled on SIGINT/SIGTERM. This is the single shutdown signal, and it
	// propagates to the collector and the in-flight shipment through the context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Build the sink before starting collection so an enrollment failure aborts
	// immediately rather than buffering events with nowhere to ship them.
	sinkName, deliver, identity, err := buildSink(ctx, *dryRun, cfg)
	if err != nil {
		log.Fatalf("cannot initialize sink: %v", err)
	}
	if identity != nil {
		log.Printf("client certificate valid until %s", identity.NotAfter().UTC().Format(time.RFC3339))
		go renewalLoop(ctx, identity, cfg)
	}

	collector := auth.New(cfg.StateDir)
	log.Printf("starting collector %q (state %q, queue %q, sink %s)",
		collector.Name(), cfg.StateDir, cfg.QueueDir, sinkName)

	events, err := collector.Start(ctx)
	if err != nil {
		log.Fatalf("collector %q failed to start: %v", collector.Name(), err)
	}

	// Producer: persist every collected event to the durable buffer before it
	// goes anywhere else. Nothing is lost if the process dies or the sink stalls.
	producerDone := make(chan struct{})
	go func() {
		defer close(producerDone)
		for ev := range events {
			if err := q.Write(ev); err != nil {
				log.Printf("buffer: failed to persist event %s: %v", ev.ID, err)
			}
		}
	}()

	// Consumer: drain the buffer to the sink. stopDrain triggers one final flush
	// after the producer has flushed the last events into the buffer.
	stopDrain := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		runSink(ctx, q, cfg, deliver, stopDrain, stop)
	}()

	<-ctx.Done()
	log.Printf("shutdown signal received; stopping collector %q", collector.Name())

	if err := collector.Stop(); err != nil {
		log.Printf("collector %q stopped with error: %v", collector.Name(), err)
	} else {
		log.Printf("collector %q stopped cleanly", collector.Name())
	}

	<-producerDone   // collector channel fully drained into the buffer
	close(stopDrain) // ask the consumer for a final flush
	<-consumerDone

	if n := q.Len(); n > 0 {
		log.Printf("%d event(s) remain buffered in %q; they resume on next start", n, cfg.QueueDir)
	}
}

// buildSink returns the named delivery function the consumer drains into: a
// stdout encoder under -dry-run, or the enrolled mTLS shipper otherwise.
func buildSink(ctx context.Context, dryRun bool, cfg config.Config) (string, deliverFunc, *enroll.Identity, error) {
	if dryRun {
		enc := json.NewEncoder(os.Stdout)
		return "stdout (dry-run)", func(_ context.Context, evs []collectors.Event) error {
			for _, ev := range evs {
				if err := enc.Encode(ev); err != nil {
					return fmt.Errorf("%w: encode to stdout: %v", transport.ErrRetryable, err)
				}
			}
			return nil
		}, nil, nil
	}

	id, err := enroll.EnsureIdentity(ctx, cfg)
	if err != nil {
		return "", nil, nil, err
	}
	host, _ := os.Hostname()
	shipper := transport.New(cfg.IngestEndpoint, host, id.TLSConfig())
	return "mTLS " + cfg.IngestEndpoint, shipper.Send, id, nil
}

// runSink drains the buffer into deliver on a fixed cadence, acking delivered
// batches, dead-lettering poison ones, and backing off on retryable failures.
func runSink(ctx context.Context, q *buffer.Queue, cfg config.Config, deliver deliverFunc, stopDrain <-chan struct{}, halt func()) {
	ticker := time.NewTicker(cfg.FlushInterval)
	defer ticker.Stop()
	attempt := 0

	for {
		select {
		case <-stopDrain:
			finalFlush(q, cfg, deliver)
			return
		case <-ctx.Done():
			// Shutting down: the context is cancelled, so stop normal delivery,
			// wait for the producer to finish, then do one bounded final flush.
			<-stopDrain
			finalFlush(q, cfg, deliver)
			return
		case <-ticker.C:
		}

	drainPass:
		for {
			batch, err := q.ReadBatch(cfg.BatchSize, 0)
			if err != nil {
				log.Printf("sink: read batch failed: %v", err)
				break drainPass
			}
			if batch.Len() == 0 {
				attempt = 0
				break drainPass
			}

			switch err = deliver(ctx, batch.Events); {
			case err == nil:
				if e := q.Ack(batch); e != nil {
					log.Printf("sink: ack failed: %v", e)
				}
				attempt = 0
			case errors.Is(err, transport.ErrUnauthorized):
				log.Printf("sink: platform rejected our identity (%v) — halting shipping; %d event(s) kept buffered, re-onboard to resume", err, q.Len())
				halt()
				return
			case errors.Is(err, transport.ErrPermanent):
				log.Printf("sink: dead-lettering %d poison event(s): %v", batch.Len(), err)
				if e := q.DeadLetter(batch); e != nil {
					log.Printf("sink: dead-letter failed: %v", e)
				}
				attempt = 0
			default:
				attempt++
				wait := transport.Backoff(attempt, cfg.BackoffBase, cfg.BackoffCap)
				log.Printf("sink: delivery failed (attempt %d), backing off %s: %v",
					attempt, wait.Round(time.Millisecond), err)
				waitOrStop(ctx, stopDrain, wait)
				break drainPass // outer select handles shutdown; next tick retries
			}
		}
	}
}

// finalFlush makes one bounded, best-effort pass over the buffer at shutdown.
// Anything not delivered stays on disk and resumes on the next run.
func finalFlush(q *buffer.Queue, cfg config.Config, deliver deliverFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		batch, err := q.ReadBatch(cfg.BatchSize, 0)
		if err != nil {
			log.Printf("sink: final read failed: %v", err)
			return
		}
		if batch.Len() == 0 {
			return
		}
		switch err = deliver(ctx, batch.Events); {
		case err == nil:
			if e := q.Ack(batch); e != nil {
				log.Printf("sink: final ack failed: %v", e)
			}
		case errors.Is(err, transport.ErrPermanent):
			_ = q.DeadLetter(batch)
		default:
			log.Printf("sink: %d event(s) left buffered for next run: %v", batch.Len(), err)
			return
		}
		if ctx.Err() != nil {
			return
		}
	}
}

// waitOrStop sleeps for d, returning early if shutdown begins.
func waitOrStop(ctx context.Context, stopDrain <-chan struct{}, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-stopDrain:
	case <-ctx.Done():
	}
}

// renewalLoop renews the client certificate before it expires: once at startup,
// then on a fixed interval. Failures are logged and retried on the next tick;
// the cert stays valid until then.
func renewalLoop(ctx context.Context, id *enroll.Identity, cfg config.Config) {
	check := func() {
		if !id.NeedsRenewal(cfg.RenewBefore) {
			return
		}
		if err := id.Renew(ctx, cfg); err != nil {
			log.Printf("cert: renewal failed (will retry): %v", err)
			return
		}
		log.Printf("cert: renewed; valid until %s", id.NotAfter().UTC().Format(time.RFC3339))
	}
	check()
	ticker := time.NewTicker(cfg.RenewCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
