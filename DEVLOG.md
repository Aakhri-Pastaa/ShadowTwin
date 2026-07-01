# ShadowTwin — Development Log

> **Pivot note (2026):** the project pivoted from the custom Go host agent
> documented in the entries below to a **Wazuh-based** telemetry/detection
> source, tools-first agents, and an advisory-only (human-in-the-loop)
> Defender. The Go agent is not deleted — it's archived at
> [`go-agent-v0/`](go-agent-v0/) as a complete, tested reference and as the
> project's starting point. Everything below is accurate **history**, not
> the current path; see the root [README](README.md) and
> [`docs/architecture.md`](docs/architecture.md) for the current design.

A running, human-readable record of how this project was built: what we did, why
we did it, how it works, and how each step was verified. It exists so either of
us (or a future contributor) can **backtrack in detail** without reconstructing
intent from diffs and chat logs.

This complements, but does not replace:

- **`docs/adr/`** — the formal *decision* records (why a non-obvious choice was
  made). The devlog links to them rather than repeating them.
- **Component READMEs** (e.g. `host-agent/README.md`) — how to build/run/test a
  component as it stands *now*. The devlog is the *history*; the README is the
  *current state*.

## How this log is maintained

- One dated entry per meaningful chunk of work (typically one merged PR).
- Each entry follows the same shape: **What · Why · How · Verification · Refs**.
- The **Current state** section near the top is always kept accurate; the
  **Timeline** below is append-only narrative, oldest first.
- Dates are absolute (`YYYY-MM-DD`).

---

## Project at a glance

**ShadowTwin** is a two-person, open-source, closed-loop purple-team lab: a host
agent collects telemetry from a deliberately vulnerable sandbox; an Evaluator
triages it; an Attacker validates exploitability *inside the sandbox only*; a
Defender remediates and re-triggers the Attacker to prove the fix. See
`docs/architecture.md` for the full picture.

**Where we started:** an empty scaffold — repo conventions, CI skeleton,
pre-commit hooks, ADRs 0001–0002, and component directories, but no working
component.

**Where we are now:** the **host agent** has a complete, end-to-end **telemetry
shipping path**: it collects authentication events from `systemd-journald`,
persists them to a durable on-disk buffer, and ships them to the platform over
mutually authenticated TLS, enrolling for its client certificate on first run.
It is a single static Go binary with **zero third-party dependencies**. The
other components (graph, threat-intel, evaluator, attacker, defender, frontend)
are not started yet.

---

## Current state — what works today

| Area | Status | Notes |
|---|---|---|
| Host agent: collector framework | ✅ | `Collector` interface + shared `Event` shape (UUID per event) |
| Host agent: `auth.journald` collector | ✅ | Drives `journalctl -o json`, cursor-resumes, log-and-skip on bad input |
| Host agent: durable buffer | ✅ | Crash-safe spool, at-least-once, bounded, dead-letters poison |
| Host agent: mTLS shipper | ✅ | Batched, gzip, TLS 1.3, pinned CA, retry+backoff |
| Host agent: enrollment + renewal | ✅ | Token+CSR bootstrap; auto-renew over mTLS before expiry; key never leaves the host |
| Host agent: revocation handling | ✅ | Platform `403` → agent halts and keeps events buffered (nothing discarded) |
| Host agent: direct addressing | ✅ | `mock-platform -host` SANs → agents connect to the platform's real address (no tunnel) |
| Host agent: one-command onboarding | ✅ | `agent install` (+ `uninstall`/`status`/`doctor`): unprivileged user, hardened systemd unit, enroll, verify |
| Dev mock platform | ✅ | `cmd/mock-platform`: `/ca`, `/enroll`, `/renew`, `/ingest`, `/revoke` (dev only) |
| Real platform / ingest server | ⛔ | Not built; mock stands in. Contract in ADR-0003 |
| Other components | ⛔ | graph / threat-intel / evaluator / attacker / defender / frontend |

### How to run and verify it (host agent)

```bash
cd host-agent
gofmt -l . && go vet ./... && go build ./... && go test ./...   # all clean
go test -race ./internal/transport/ ./internal/enroll/ ./internal/buffer/

# Simplest run — no certs needed; events print to stdout:
go run ./cmd/agent -dry-run
#   in another shell, generate one real auth event:
sudo -k; echo 'wrong-password' | sudo -S true

# Full mTLS path against the dev mock platform:
go run ./cmd/mock-platform -token demo-token        # terminal 1 (writes ./mock-ca.crt)
mkdir -p ~/.host-agent/certs && cp mock-ca.crt ~/.host-agent/certs/ca.crt
export AGENT_ENROLL_ENDPOINT=https://localhost:8443/enroll
export INGEST_ENDPOINT=https://localhost:8443/ingest
export AGENT_ENROLL_TOKEN=demo-token
go run ./cmd/agent                                  # terminal 2: enrolls, then ships
#   trigger a failed sudo; the mock logs "/ingest: accepted N event(s) (client CN=...)"
```

---

## Timeline

### 2026-06-17 — Repository baseline

**What.** Project conventions and scaffolding: `CLAUDE.md` (hard rules + repo
map), `CONTRIBUTING.md`, `SECURITY.md`, `docs/architecture.md`, ADR-0001 (record
decisions) and ADR-0002 (custom Go agent, not a Wazuh fork), `.pre-commit-config.yaml`,
per-component CI workflows, `.env.example`, and `.gitignore`.

**Why.** Lock the workflow and the non-negotiables (attacker scope-lock, no
secrets in git, advisory-only defender output, PR-per-change) before any code, so
the project scales cleanly and decisions are traceable.

**Refs.** commits `0c1d382`, `57e3569`, `4c968b8`.

### 2026-06-17 — Host agent, Slice 1: foundation + journald auth collector (PR #1)

**What.** The agent's skeleton and its first collector. The `Collector` interface
(`Name`/`Start`/`Stop`), the shared `Event` shape (every event gets a
`crypto/rand` UUIDv4), the `auth.journald` collector, signal-driven lifecycle,
and a minimal `config`. Events were emitted to stdout in this slice.

**Why.**

- *Single static Go binary, zero deps* (per ADR-0002): strongest fit for a
  privileged agent we want to fully own and audit, and for one-command install.
- *journald via `journalctl -o json`, not cgo `libsystemd`*: keeps the binary
  static and cross-compilable, matching the "drive proven tools" philosophy.
  Resumption uses journald's **cursor** API, not naive file tailing.
- *UUID per event*: the foundation for idempotent, at-least-once delivery later.
- *Define the `Collector` contract before a second collector*: so the buffer and
  shipper plug into a stable seam.

**How.** `cmd/agent` wires `config → collector.Start(ctx) → stdout`, with clean
SIGINT/SIGTERM shutdown. The collector reads `journalctl --output=json --follow`
filtered to the `auth`/`authpriv` facilities, normalizes each entry, persists the
`__CURSOR` atomically (temp-write + rename, `0600`), and logs-and-skips malformed
lines instead of crashing.

**Verification.** `gofmt`/`vet`/`build`/`test` clean; table-driven parser tests
and an ID-uniqueness test (no root, no live journald). Live: a real failed `sudo`
produced normalized JSON events with unique UUIDs and a persisted cursor.

**Refs.** PR #1 → `20e690c`. README: `host-agent/README.md`.

### 2026-06-17 — Tooling: CI Go 1.26 + module-aware `go vet` hook

**What.** Bumped the host-agent CI workflow to Go 1.26 (to match the module's
`go` directive) and fixed the `go vet` pre-commit hook.

**Why.** The stock dnephin `go-vet` hook ran `go vet` from the repo root, which
has no `go.mod` (the module lives in `host-agent/`), so commits failed with
"cannot find main module". The `go-vet-mod` variant belongs to a *different* fork
(TekWizely), not the one we pin — so instead of adding a third-party fork we used
a `local` hook that runs `go vet ./...` inside `host-agent/`, mirroring CI's
`working-directory: host-agent`.

**Refs.** part of PR #1 (`2f7b4a5`, `378b5fd`).

### 2026-06-17 — Host agent, Slice 2 / PR-A: durable on-disk buffer (PR #2)

**What.** A crash-safe, FIFO, on-disk event buffer (`internal/buffer`) inserted
between the collector and the sink: collector → **buffer** → drain. The stdout
sink became a consumer that drains the buffer and acks each batch.

**Why.** Decouple collection from delivery (the classic store-and-forward pattern
used by Filebeat/Fluent Bit/Vector). A platform or network outage must queue
events on disk rather than drop them, and bursts (e.g. a brute-force flood) must
be absorbed. Delivery is **at-least-once**: an event is deleted only after it is
acked, and the per-event UUID lets the platform dedupe re-sends.

**How.** A *Maildir-style spool* — one event per file, written atomically
(temp + `os.Rename`, `0600`), named so a lexical sort is FIFO. Chosen over a
segmented log or embedded DB because it is zero-dependency, trivially auditable,
and reuses the atomic-write discipline already proven by the journald cursor.
The queue is **bounded** by event count and bytes (oldest dropped first, with a
warning) so a stalled sink can't exhaust the disk; unparseable files are
**dead-lettered** so a poison record can't wedge the queue; orphaned temp files
are cleaned and pending events recovered on reopen.

**Verification.** Unit tests (incl. `-race`): FIFO/ack, batch count + byte caps,
dead-lettering, drop-oldest bound, crash recovery on reopen, private (`0600`)
file modes, concurrent write/drain. Live: a failed `sudo` flowed
collector → buffer → stdout and the queue drained to empty.

**Refs.** PR #2 → `5440442`.

### 2026-06-17 — Host agent, Slice 2 / PR-B: mTLS shipper + enrollment (PR #3)

**What.** Completed the shipping spine. The agent now **enrolls** for an mTLS
client certificate and **ships** buffered events to the platform over mutual TLS,
replacing the stdout sink (still available via `-dry-run`). Added `internal/pki`
(shared cert primitives) and a dev-only `cmd/mock-platform` implementing the
`/enroll` + `/ingest` contract. Decision + wire contract recorded in **ADR-0003**.

**Why.**

- *HTTP/JSON over mTLS, not gRPC*: keeps zero dependencies and a static binary;
  `curl`-debuggable. gRPC's dependency/codegen weight buys nothing at this volume.
- *Mutual TLS, TLS 1.3, pinned platform CA (not system roots)*: the platform must
  trust only enrolled agents (an attacker must not be able to inject telemetry to
  mislead the Evaluator), and the agent must reach only our platform even if a
  public CA is compromised or DNS is tampered.
- *Token + CSR enrollment, key generated on-host*: proven TLS-bootstrap pattern
  (kubelet/step-ca). The private key is never transmitted; the one-time token is
  the only bootstrap secret; the CA is non-secret and provided out-of-band, so no
  trust-on-first-use.
- *Send-only, no inbound socket*: on a deliberately vulnerable host, the agent
  must never become a new attack surface or pivot.
- *Bounded retry with backoff + jitter; 4xx → dead-letter, 5xx/network → retry*:
  no infinite poison loops, no thundering herd on platform recovery.

**How.** `internal/transport` POSTs gzip-compressed batches and classifies the
response (`2xx` ack / `4xx` poison / `5xx`+network retryable). `internal/enroll`
generates an ECDSA P-256 key, sends a CSR + bearer token to `/enroll`, and stores
the returned cert (key `0600`) under `<state>/certs`, reusing it on later runs.
`cmd/agent` runs an enroll → drain loop that acks, dead-letters, or backs off per
batch, with a coordinated, bounded final flush at shutdown. Because no real
platform exists yet, `cmd/mock-platform` mints a throwaway CA and serves both
endpoints over TLS so the whole path is exercisable locally and in tests.

**Verification.** Unit tests (incl. `-race`) with real in-process certs: mTLS
round-trip, **client cert required**, **CA pinning** (untrusted server rejected),
response classification, backoff bounds, and enrollment asserting the request
carries a **CSR and not the private key**, writes the key `0600`, and reuses on a
second run. Live: agent enrolled, a real failed `sudo` flowed
collector → buffer → mTLS → mock `/ingest` (server logged the verified client
CN), the queue drained to empty, clean shutdown.

**Refs.** PR #3 → `d4f6ff8`. ADR: `docs/adr/0003-host-agent-telemetry-transport.md`.

---

### 2026-06-17 — Host agent, Slice 3 / PR-1: certificate lifecycle + direct addressing (PR #5)

**What.** No-tunnel direct addressing plus the certificate lifecycle deferred
from PR-B: automatic renewal and revocation. Added `pki.ParseKeyPEM`, the
`ErrUnauthorized` transport outcome, an `Identity` type that renews itself, and
`/ca`, `/renew`, `/revoke` on the mock platform.

**Why.**

- *Direct addressing (`mock-platform -host`)*: real agents reach the platform at
  its actual address, not `localhost`; the dev cert needs that name in its SAN so
  TLS verification passes without an SSH tunnel.
- *Renewal*: 90-day certs must rotate unattended.
- *Revocation*: a decommissioned/compromised host must be cut off — and a revoked
  agent must **not discard** the telemetry it already holds.

**How.** `Identity` (in `internal/enroll`) holds the key + current cert and serves
it via `GetClientCertificate`, so renewal swaps the cert with no restart. Renewal
uses `POST /renew` authenticated by the *current* mTLS cert (no token), checked at
startup and every 6h, triggering within 30 days of expiry; an already-expired cert
falls back to token re-enrollment. Revocation is a platform-side denylist
returning `403`; the agent maps `401/403` to a distinct `ErrUnauthorized` and
**halts shipping while keeping events buffered** (vs `400` poison → dead-letter,
`5xx`/network → retry).

**Verification.** Unit tests (incl. `-race`): key PEM round-trip, CSR→client-cert
signing, multi-SAN server certs, transport classification (`401/403`→unauthorized),
renewal reissues a new serial, `NeedsRenewal` windows, enroll-then-reuse. Live, no
tunnel: agent connected to `https://127.0.0.2:8443` (non-localhost SAN), fetched
`GET /ca`, enrolled, shipped a real failed `sudo`; after `POST /revoke` the next
ship got `403` and the agent halted with 2 events **kept buffered**.

**Refs.** PR #5. ADR: `docs/adr/0004-host-agent-certificate-lifecycle.md`
(extends ADR-0003 with `/ca`, `/renew`, `/revoke`).

### 2026-06-17 — Host agent, Slice 3 / PR-2: one-command onboarding (PR #6)

**What.** `agent install` plus `uninstall`, `status`, and `doctor` — the binary
onboards itself as a hardened systemd service.

**Why.** ADR-0002's promise: a single static binary, installed in one command,
with nothing to compile and no libraries to add on the target. We had the binary;
this is the install UX.

**How.** `main` becomes a subcommand dispatcher (`run` stays the default and the
ExecStart). `install` (`cmd/agent/onboard.go`) creates an unprivileged
`shadowtwin` user in the `systemd-journal` group (journal read without root),
copies the running binary to `/usr/local/bin`, fetches the platform CA (a file or
`GET /ca`), writes `/etc/shadowtwin-agent/agent.env` (token `0600`) and a hardened
unit (`NoNewPrivileges`, `ProtectSystem=strict`, empty `CapabilityBoundingSet`,
`ReadWritePaths` = state dir only, …), enables+starts the service, then verifies
connectivity (CA-pinned TLS to the platform) and enrollment before returning. It
uses only base-OS tools (`systemctl`, `useradd`, `usermod`) — no package manager.
`uninstall` (+`--purge`), `status`, and `doctor` round it out; `--dry-run`
previews the plan and unit.

**Verification.** `gofmt`/`vet`/`build`/`test` clean. Unit tests cover the pure
parts (unit + env rendering, endpoint derivation, CA fetch, cert parsing,
env-file parsing). Demonstrated safely with `install --dry-run` (full plan +
hardened unit, no changes), `status`, and `doctor`. A real `sudo install` needs
root + systemd on the target; the side-effecting steps shell out to standard
tools.

**Refs.** PR #6.

## Upcoming / backlog

Rough priority order for the host agent and the wider platform:

1. **A second collector** — e.g. process/network via osquery, or auditd — to
   prove the `Collector` contract generalizes beyond journald.
2. **The real platform ingest service** — implement the ADR-0003/0004 contract
   (replacing `cmd/mock-platform`), then the environment graph (Neo4j) it feeds.
3. **Hardening** — tamper protection, secure auto-update, config integrity.

## Decision index (ADRs)

- ADR-0001 — Record architecture decisions.
- ADR-0002 — Build a custom Go host agent instead of forking Wazuh.
- ADR-0003 — Host-agent telemetry transport: mTLS HTTP + token/CSR enrollment.
- ADR-0004 — Host-agent certificate lifecycle: renewal and revocation.
