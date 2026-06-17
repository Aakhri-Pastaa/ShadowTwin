# host-agent

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
- 🚧 **Slice 2 — Shipping spine** — make events actually reach the platform:
  - ✅ **PR-A — durable disk buffer**: a crash-safe on-disk queue between the
    collector and the sink (collector → buffer → drain), so events survive
    restarts and platform outages.
  - **PR-B — mTLS transport** (batched, retrying shipper) + one-time certificate
    **enrollment** (token + CSR; the private key never leaves the host), with a
    dev-only mock platform to test against. Wire contract: `docs/adr/0003-*`.
- ⬜ **Later** — more collectors (osquery process/network, auditd,
  Windows/Sysmon), packaging (systemd unit, dedicated service user,
  single-binary install), and hardening (tamper protection, secure auto-update).

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
- Drains the buffer to **stdout** as newline-delimited JSON, deleting each batch
  only after it is emitted (at-least-once). Operational logs go to **stderr**, so
  the two never mix. The next slice swaps this stdout sink for the mTLS shipper.
- Resumes after a restart from journald's cursor, persisted to
  `~/.host-agent/auth.journald.cursor`. No naive file tailing.
- Every event carries a generated UUID so downstream retries can dedupe.

## Architecture (one-way pipeline)

```
journald ─(journalctl -o json)─▶ auth collector ─Event─▶ [ disk buffer ] ─▶ stdout (JSON)
                                              └─cursor─▶ ~/.host-agent/    (FIFO; drained in
                                                                           batches, acked after emit)
```

The agent only ever **sends**. It has no inbound control channel and accepts no
remote commands — a deliberate security choice.

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

Then run the agent (events on stdout, logs on stderr):

```
go run ./cmd/agent          # or ./bin/agent after building
go run ./cmd/agent 2>/dev/null   # to watch only the event stream
```

## Trigger a real auth event

In another terminal, generate a failed authentication. A failed `sudo` is the
simplest and is guaranteed to log to authpriv:

```
sudo -k                                 # forget cached credentials
echo 'wrong-password' | sudo -S true    # one guaranteed auth failure
```

Within about a second the agent prints a JSON event on stdout, e.g.
(pretty-printed):

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

Tests use journal fixtures and a fake reader — no root and no live journald
required.

## Beyond the current slice

See **Status & roadmap** above for what's next and what's deferred. The
`internal/buffer`, `internal/transport`, and `internal/enroll` packages are
placeholders marking where the shipping spine lands.
