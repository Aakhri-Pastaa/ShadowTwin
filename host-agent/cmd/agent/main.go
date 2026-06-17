// Command agent is the ShadowTwin host agent: it runs telemetry collectors,
// persists every event to a durable on-disk buffer, and drains that buffer to a
// sink. This slice's sink is stdout (newline-delimited JSON); the next slice
// swaps it for the mTLS shipper without changing the buffer contract.
// Operational logging (startup, errors, shutdown) goes to stderr so it never
// mixes with the event stream.
package main

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/buffer"
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

	q, err := buffer.Open(cfg.QueueDir, buffer.Limits{
		MaxEvents: cfg.MaxQueueEvents,
		MaxBytes:  cfg.MaxQueueBytes,
	})
	if err != nil {
		log.Fatalf("cannot open event buffer: %v", err)
	}

	// Cancelled on SIGINT/SIGTERM. This is the single shutdown signal, and it
	// propagates to the collector through the context.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	collector := auth.New(cfg.StateDir)
	log.Printf("starting collector %q (state dir %q, queue %q)",
		collector.Name(), cfg.StateDir, cfg.QueueDir)

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

	// Consumer: drain the buffer to stdout on a fixed cadence, acking (deleting)
	// each batch only after it is emitted. stopDrain triggers one last drain
	// after the producer has flushed the final events into the buffer.
	stopDrain := make(chan struct{})
	consumerDone := make(chan struct{})
	go func() {
		defer close(consumerDone)
		enc := json.NewEncoder(os.Stdout)
		ticker := time.NewTicker(cfg.FlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopDrain:
				drain(enc, q, cfg.BatchSize)
				return
			case <-ticker.C:
				drain(enc, q, cfg.BatchSize)
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

	<-producerDone   // collector channel fully drained into the buffer
	close(stopDrain) // ask the consumer for a final drain
	<-consumerDone   // final drain finished

	if n := q.Len(); n > 0 {
		log.Printf("%d event(s) remain buffered in %q; they resume on next start", n, cfg.QueueDir)
	}
}

// drain emits whole batches from the buffer to the encoder until the buffer is
// empty or a batch can't be written. A batch is acked only after every event in
// it is encoded; on a write error the batch stays buffered and is retried on the
// next pass.
func drain(enc *json.Encoder, q *buffer.Queue, batchSize int) {
	for {
		batch, err := q.ReadBatch(batchSize, 0)
		if err != nil {
			log.Printf("buffer: read batch failed: %v", err)
			return
		}
		if batch.Len() == 0 {
			return
		}
		emitted := true
		for _, ev := range batch.Events {
			if err := enc.Encode(ev); err != nil {
				log.Printf("failed to encode event %s: %v", ev.ID, err)
				emitted = false
				break
			}
		}
		if !emitted {
			return // leave the batch buffered; retry next pass
		}
		if err := q.Ack(batch); err != nil {
			log.Printf("buffer: ack failed: %v", err)
			return
		}
	}
}
