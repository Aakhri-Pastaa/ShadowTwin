// Package transport ships buffered events to the platform over mutually
// authenticated TLS. The agent only ever sends; it exposes no inbound channel.
// Send classifies failures so the caller knows whether to ack, retry, or
// dead-letter a batch. Stdlib-only.
package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"time"

	"github.com/Aakhri-Pastaa/ShadowTwin/host-agent/internal/collectors"
)

// Delivery outcomes. Errors from Send wrap one of these so the caller can react
// without inspecting HTTP status codes.
var (
	ErrRetryable = errors.New("transport: retryable failure")
	ErrPermanent = errors.New("transport: permanent failure")
	// ErrUnauthorized is the platform rejecting the agent's identity (e.g. the
	// cert was revoked). It is about *who* we are, not the batch content, so the
	// caller must not discard events — it should halt and keep them buffered.
	ErrUnauthorized = errors.New("transport: unauthorized (identity rejected)")
)

const schemaVersion = 1

// Shipper POSTs event batches to the platform's ingest endpoint over mTLS.
type Shipper struct {
	client   *http.Client
	endpoint string
	agentID  string
	gzip     bool
}

// New builds a Shipper that authenticates with the given mTLS config.
func New(endpoint, agentID string, tlsConf *tls.Config) *Shipper {
	return &Shipper{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig:   tlsConf,
				ForceAttemptHTTP2: true,
				IdleConnTimeout:   90 * time.Second,
			},
		},
		endpoint: endpoint,
		agentID:  agentID,
		gzip:     true,
	}
}

type envelope struct {
	AgentID       string             `json:"agent_id"`
	SentAt        time.Time          `json:"sent_at"`
	SchemaVersion int                `json:"schema_version"`
	Events        []collectors.Event `json:"events"`
}

// Send ships a batch. A nil error means the platform acked it. Otherwise the
// error wraps ErrPermanent (poison — dead-letter it) or ErrRetryable (keep it
// and retry after backoff).
func (s *Shipper) Send(ctx context.Context, events []collectors.Event) error {
	if len(events) == 0 {
		return nil
	}
	body, err := json.Marshal(envelope{
		AgentID:       s.agentID,
		SentAt:        time.Now().UTC(),
		SchemaVersion: schemaVersion,
		Events:        events,
	})
	if err != nil {
		// A batch we can't even marshal will never succeed.
		return fmt.Errorf("%w: marshal batch: %v", ErrPermanent, err)
	}

	reader := io.Reader(bytes.NewReader(body))
	gzipped := false
	if s.gzip {
		if buf, gzErr := gzipBytes(body); gzErr == nil {
			reader = bytes.NewReader(buf)
			gzipped = true
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, reader)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrPermanent, err)
	}
	req.Header.Set("Content-Type", "application/json")
	if gzipped {
		req.Header.Set("Content-Encoding", "gzip")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		// Network error, timeout, or TLS failure — all worth retrying.
		return fmt.Errorf("%w: %v", ErrRetryable, err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusUnauthorized,
		resp.StatusCode == http.StatusForbidden:
		return fmt.Errorf("%w: ingest returned %s", ErrUnauthorized, resp.Status)
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		return fmt.Errorf("%w: ingest returned %s", ErrRetryable, resp.Status)
	default:
		return fmt.Errorf("%w: ingest returned %s", ErrPermanent, resp.Status)
	}
}

func gzipBytes(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Backoff returns the delay before retry attempt n (1-based): capped exponential
// growth with full jitter, so a fleet of agents doesn't reconnect in lockstep
// when the platform recovers.
func Backoff(attempt int, base, max time.Duration) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	d := base << (attempt - 1)
	if d <= 0 || d > max {
		d = max
	}
	return time.Duration(rand.Int63n(int64(d) + 1))
}
