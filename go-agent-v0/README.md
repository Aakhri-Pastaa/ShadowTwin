# go-agent-v0

> **⏸ ARCHIVED — superseded by Wazuh.**
> This is `go-agent-v0`, the original custom Go telemetry agent (single static
> binary, mTLS, durable buffer, enrollment, cert lifecycle). It is complete and
> tested, preserved here as reference and as the project's starting point. The
> project pivoted to **Wazuh** as the primary telemetry + detection source
> (native multi-platform collection, decoder/rule engine, MITRE ATT&CK mapping,
> vulnerability detection, CIS assessment) under the principle "reuse the mature
> tool, build only the differentiating glue." May be revisited for a lightweight
> custom-collector use case Wazuh doesn't cover. See the root README and
> [`DEVLOG.md`](DEVLOG.md) for this component's full build history.

The ShadowTwin host agent collects security telemetry from a monitored host and
emits it as a single, normalized JSON event stream. It is a single static Go
binary with no third-party dependencies and nothing to compile on the target —
by design, so install is one step (see
`docs/adr/0002-custom-host-agent-not-wazuh-fork.md`).

The agent is built in slices, each landing as its own PR — see
**Status & roadmap** below. Today it runs the collector framework plus one
collector: systemd-journald authentication events.

## Status & roadmap

- ✅ **Slice 1 — Foundation + auth collector**: the `Collector` / `Event`
  contract, UUID-stamped events, the `auth.journald` collector, signal-driven
  lifecycle, and config (events went straight to stdout).
- ✅ **Slice 2 — Shipping spine** — events now reach the platform:
  - ✅ **PR-A — durable disk buffer**: a crash-safe on-disk queue between the
    collector and the sink (collector → buffer → drain), so events survive
    restarts and platform outages.
  - ✅ **PR-B — mTLS transport + enrollment**: a batched, retrying mTLS shipper
    plus one-time token+CSR certificate enrollment (the private key never leaves
    the host), with a dev-only mock platform to test against. Wire contract:
    ADR-0003.
- ✅ **Slice 3 — Onboarding + certificate lifecycle**:
  - ✅ **PR-1 — direct addressing + cert lifecycle**: `mock-platform -host` for
    no-tunnel mTLS; certificate **renewal** (mTLS `/renew` before expiry) and
    **revocation** (platform denylist; a revoked agent halts and keeps its
    buffered events). See ADR-0004.
  - ✅ **PR-2 — one-command onboarding**: an `agent install` subcommand that copies
    the binary, creates an unprivileged service user, writes a hardened systemd
    unit, enrolls, and starts — plus `uninstall`, `status`, and `doctor`. No
    external dependencies.
- ⬜ **Later** — more collectors (osquery process/network, auditd,
  Windows/Sysmon) and hardening (tamper protection, secure auto-update).

## What it does today

- Defines the `Collector` interface and the shared `Event` shape every collector
  emits (`internal/collectors`).
- Ships one collector, `auth.journald` (`internal/collectors/auth`), which follows
  `auth`/`authpriv` events (sshd, sudo, su, login, PAM, polkit) by driving
  `journalctl -o json` and normalizing each entry.
- Persists every event to a crash-safe, FIFO on-disk **buffer**
  (`internal/buffer`, spooled under `~/.host-agent/queue`) before anything reads
  it, so a process crash or a stalled sink never drops telemetry. The buffer is
  bounded (oldest dropped when full) and dead-letters events it can't parse.
- Ships batches from the buffer to the platform over **mutual TLS**
  (`internal/transport`): TLS 1.3, the platform CA pinned (not the system trust
  store), gzip-compressed. A batch is deleted only after the platform acks it
  (at-least-once); `400` is dead-lettered, `5xx`/network are retried with
  exponential backoff + jitter, and `401/403` (e.g. a **revoked** agent) halts
  shipping while keeping events buffered. Operational logs go to **stderr**.
- **Enrolls & renews** (`internal/enroll`): on first run it generates an ECDSA
  key locally (the private key never leaves the host) and trades a CSR + one-time
  token for a client certificate under `~/.host-agent/certs`. It then renews the
  cert before expiry over its current mTLS identity (no token), and re-enrolls if
  it finds an expired cert. `-dry-run` skips all of this and prints to stdout.
- Resumes after a restart from journald's cursor, persisted to
  `~/.host-agent/auth.journald.cursor`. No naive file tailing.
- Every event carries a generated UUID so downstream retries can dedupe.

## Architecture (one-way pipeline)

```
journald ─▶ auth collector ─▶ [ disk buffer ] ─▶ [ mTLS shipper ] ─▶ platform /ingest
                 └─cursor─▶ ~/.host-agent/       FIFO, acked after    batched, gzip,
                                                 platform ack          retry + backoff

enrollment: generate key on host ─▶ CSR + one-time token ─▶ platform /enroll ─▶ client cert
```

The agent only ever **sends** — no inbound control channel, no remote commands,
so it never becomes an attack surface on a monitored host. Mutual TLS means the
platform trusts only enrolled agents, and the agent (pinning the platform CA)
ships only to the real platform.

## Build

Requires Go (the module targets the version in `go.mod`). The build is pure Go,
`CGO_ENABLED=0` — no `libsystemd-dev` or other headers needed.

```
cd host-agent
go build -o bin/agent ./cmd/agent
```

## Run

The auth collector needs permission to read the system journal. That is **not**
root — it's membership in the `systemd-journal` group (or `adm`/`wheel` on
Debian/Ubuntu, which systemd grants journal read access via ACLs). Confirm with:

```
journalctl -n1 SYSLOG_FACILITY=10 >/dev/null && echo "can read authpriv"
```

The simplest run is `-dry-run`, which skips enrollment and prints events to
stdout (logs go to stderr, so the two never mix):

```
go run ./cmd/agent -dry-run             # buffer -> stdout, no certs needed
go run ./cmd/agent -dry-run 2>/dev/null # watch only the event stream
```

### Ship over mTLS (against the dev mock platform)

The default (no `-dry-run`) enrolls and ships over mTLS, which needs a platform.
Until the real one exists, `cmd/mock-platform` stands in (dev only). In one
terminal:

```
go run ./cmd/mock-platform -token demo-token   # writes ./mock-ca.crt, listens on :8443
```

In another, trust its CA, point the agent at it, and run:

```
mkdir -p ~/.host-agent/certs
cp mock-ca.crt ~/.host-agent/certs/ca.crt
export AGENT_ENROLL_ENDPOINT=https://localhost:8443/enroll
export INGEST_ENDPOINT=https://localhost:8443/ingest
export AGENT_ENROLL_TOKEN=demo-token
go run ./cmd/agent
```

The agent generates its key locally, enrolls (CSR + token → client cert under
`~/.host-agent/certs`), then ships batches over mTLS; the mock logs each
`/ingest`. It renews the cert before expiry automatically, and if the platform
revokes it (`curl -X POST .../revoke -d '{"agent_id":"<host>"}'`) the agent halts
and keeps its buffered events for re-onboarding.

**Across two machines (no tunnel):** start the mock with `-host <server-ip-or-dns>`
(so its TLS cert is valid for that address), copy its `mock-ca.crt` to the
agent's `certs/ca.crt` (or fetch `GET /ca`), and set the agent's endpoints to
`https://<server>:8443/...`. The agent connects directly — no SSH tunnel.
Contract: `docs/adr/0003` + `docs/adr/0004`.

## One-command install (systemd)

In production the agent is a single static binary onboarded with one command. It
uses only base-OS tools (`systemctl`, `useradd`/`usermod`) — never a package
manager, and nothing to compile on the target:

```
sudo shadowtwin-agent install --server https://<platform>:8443 --token <enroll-token>
```

This creates an unprivileged `shadowtwin` user (in the `systemd-journal` group so
it reads the journal without root), installs the binary to `/usr/local/bin`,
fetches the platform CA, writes a **hardened** systemd unit plus
`/etc/shadowtwin-agent/agent.env` (token at `0600`), enrolls, starts the service,
and **verifies connectivity + enrollment before returning**. Pass `--ca <file>`
to pin an out-of-band CA, or `--dry-run` to print the plan and the unit without
changing anything.

Manage it with:

```
shadowtwin-agent status                     # binary / service / cert expiry / queue depth
shadowtwin-agent doctor                      # diagnostic checks (exits non-zero on failure)
sudo shadowtwin-agent uninstall [--purge]    # stop & remove (--purge also drops state + user)
```

## Trigger a real auth event

In another terminal, generate a failed authentication. A failed `sudo` is the
simplest and is guaranteed to log to authpriv:

```
sudo -k                                 # forget cached credentials
echo 'wrong-password' | sudo -S true    # one guaranteed auth failure
```

Within about a second the agent captures it. Under `-dry-run` it prints the JSON
event on stdout (pretty-printed here); when shipping over mTLS it appears in the
platform's `/ingest` log instead:

```json
{
  "id": "5f1c9e2a-...-4b1e-...",
  "source": "auth.journald",
  "timestamp": "2026-06-16T19:20:00Z",
  "collected_at": "2026-06-16T19:20:00.12Z",
  "host": "your-host",
  "payload": {
    "message": "pam_unix(sudo:auth): authentication failure; ...",
    "syslog_identifier": "sudo",
    "priority": "5",
    "syslog_facility": "10",
    "comm": "sudo",
    "uid": "1000"
  }
}
```

A failed SSH login works too, if `sshd` is running:

```
ssh -o BatchMode=yes -o StrictHostKeyChecking=no nosuchuser@localhost true
```

Stop the agent with Ctrl-C; it shuts the collector down cleanly and persists the
cursor, so the next run resumes where it left off.

## Test

```
cd host-agent
go test ./...
```

Tests cover the parser, the buffer, the mTLS transport (against an in-process TLS
server), and enrollment — no root, no live journald, no network beyond loopback.

## Beyond the current slice

See **Status & roadmap** above for what's next and what's deferred — notably
certificate renewal/revocation, more collectors, and packaging. The real
platform ingest endpoint doesn't exist yet; `cmd/mock-platform` stands in for it.
