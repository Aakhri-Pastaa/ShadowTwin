# 3. Host-agent telemetry transport: mTLS HTTP, token+CSR enrollment

Date: 2026-06-17

## Status

Accepted

## Context

The host agent must get the telemetry it collects to the platform reliably and
securely — from a host that is itself part of a deliberately vulnerable lab. No
platform ingest endpoint exists yet, so this decision also defines the wire
contract the future platform must implement.

Constraints:

- Single static Go binary, ideally zero third-party dependencies (ADR-0002).
- The agent runs on a host we are deliberately attacking; it must not become a
  new attack surface or a pivot.
- Telemetry must not be silently lost across process crashes or platform/network
  outages, and the platform must be able to trust that telemetry came from a
  real, enrolled agent rather than an attacker injecting noise.

## Decision

**Transport: HTTP/JSON over mutual TLS (mTLS)**, using only the Go standard
library (`net/http`, `crypto/tls`, `crypto/x509`). Not gRPC — it would pull in a
large dependency tree and a codegen toolchain against the zero-dependency goal,
for no benefit at this event volume.

- TLS 1.3 minimum. The agent **pins the platform CA** (`RootCAs`), not the system
  trust store, so a compromised public CA cannot MITM the stream.
- The agent only ever dials out. It exposes no listening socket and no inbound
  control channel.
- Events ship in **gzip-compressed batches**. Delivery is **at-least-once**: the
  durable buffer (PR-A) deletes a batch only after a 2xx ack, and every event
  carries a UUID so the platform deduplicates re-sends.
- Response classification: `2xx` = ack; `4xx` except `408`/`429` = permanent
  (poison → dead-letter); `408`/`429`/`5xx`/timeout/network = retryable (kept and
  retried with exponential backoff + full jitter).

**Identity / PKI: token + CSR enrollment** ("TLS bootstrap", as kubelet/step-ca
do).

- The agent generates its key pair locally; the **private key never leaves the
  host**. Only a CSR is sent.
- A one-time, single-use enrollment token (`AGENT_ENROLL_TOKEN`, from `.env`)
  authorizes the CSR. The platform CA cert is **non-secret** and provided
  out-of-band (placed at `<state>/certs/ca.crt`) so the agent can trust the
  enrollment endpoint without trust-on-first-use.
- On success the platform returns the signed client cert; the agent stores
  `client.key` (0600), `client.crt`, and `ca.crt` under `<state>/certs` and
  reuses them on later runs.

**Wire contract (v1):**

```
POST {enroll}   Authorization: Bearer <token>
  -> { "csr_pem", "agent_id" }
  <- 200 { "cert_pem", "ca_pem" }

POST {ingest}   (mTLS; client cert required)   [Content-Encoding: gzip]
  -> { "agent_id", "sent_at", "schema_version", "events": [ <event>, ... ] }
  <- 202 (dedupe by event.id) | 400 poison | 408/429/5xx retry
```

Until the real platform exists, a dev-only `cmd/mock-platform` implements this
contract for local and test use.

## Consequences

Easier: zero new dependencies; a small, auditable security surface; secure by
default (mutual auth, pinned CA, key stays on host, send-only); no telemetry lost
across crashes/outages; the contract is written down for whoever builds the
platform.

Harder: we own the PKI lifecycle. The platform must implement this exact contract
and dedupe by event id. A single TLS listener serving both enroll (no client cert
yet) and ingest (client cert required) needs per-route enforcement — the mock
uses `VerifyClientCertIfGiven` plus a handler check; a production platform may
instead split them across ports.

Explicitly not doing (this slice): certificate renewal and revocation, gRPC or
streaming, any inbound control channel, and a segmented-log/DB buffer (the PR-A
spool is sufficient).
