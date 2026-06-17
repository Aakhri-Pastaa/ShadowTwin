// Command agent is the ShadowTwin host agent: it runs telemetry collectors and
// emits their events as newline-delimited JSON on stdout. Operational logging
// (startup, errors, shutdown) goes to stderr so it never mixes with the event
// stream. This slice wires a single collector — systemd-journald auth events; the
// buffer, mTLS shipper, and enrollment arrive in later slices.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors/auth"
	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/config"
)

func main() {
	log.SetOutput(os.Stderr)
	log.SetFlags(log.LstdFlags | log.LUTC)

	cfg := config.Default()
	if err := cfg.EnsureStateDir(); err != nil {
		log.Fatalf("cannot create state dir %q: %v", cfg.StateDir, err)
	}

	// Cancelled on SIGINT/SIGTERM. This is the single shutdown signal, and it
	// propagates to the collector through the context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	collector := auth.New(cfg.StateDir)
	log.Printf("starting collector %q (state dir %q)", collector.Name(), cfg.StateDir)

	events, err := collector.Start(ctx)
	if err != nil {
		log.Fatalf("collector %q failed to start: %v", collector.Name(), err)
	}

	// Drain events to stdout as newline-delimited JSON in the background so the
	// main goroutine can wait for the shutdown signal.
	enc := json.NewEncoder(os.Stdout)
	printerDone := make(chan struct{})
	go func() {
		defer close(printerDone)
		for ev := range events {
			if err := enc.Encode(ev); err != nil {
				log.Printf("failed to encode event %s: %v", ev.ID, err)
			}
		}
	}()

	<-ctx.Done()
	log.Printf("shutdown signal received; stopping collector %q", collector.Name())

	if err := collector.Stop(); err != nil {
		log.Printf("collector %q stopped with error: %v", collector.Name(), err)
	} else {
		log.Printf("collector %q stopped cleanly", collector.Name())
	}

	<-printerDone // flush any events emitted during shutdown
}
