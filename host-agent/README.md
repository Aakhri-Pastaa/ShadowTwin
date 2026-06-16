# host-agent

The ShadowTwin host agent collects security telemetry from a monitored host and
emits it as a single, normalized JSON event stream. It is a single static Go
binary with no third-party dependencies and nothing to compile on the target —
by design, so install is one step (see
`docs/adr/0002-custom-host-agent-not-wazuh-fork.md`).

This is the **foundation slice**: the collector framework plus one collector
(systemd-journald authentication events). The disk-backed buffer, the mTLS
shipper, and certificate enrollment are stubbed packages that land in later
slices.

## What it does today

- Defines the `Collector` interface and the shared `Event` shape every collector
  emits (`internal/collectors`).
- Ships one collector, `auth.journald` (`internal/collectors/auth`), which follows
  `auth`/`authpriv` events (sshd, sudo, su, login, PAM, polkit) by driving
  `journalctl -o json` and normalizing each entry.
- Emits events as newline-delimited JSON on **stdout**; operational logs go to
  **stderr**, so the two never mix.
- Resumes after a restart from journald's cursor, persisted to
  `~/.host-agent/auth.journald.cursor`. No naive file tailing.
- Every event carries a generated UUID so downstream retries can dedupe.

## Architecture (one-way pipeline)

```
journald ──(journalctl -o json)──▶ auth collector ──Event──▶ stdout (JSON)
                                                  └─cursor──▶ ~/.host-agent/
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

## Not in this slice (deliberately deferred)

Windows/Sysmon collectors, the disk-backed buffer, the mTLS shipper, cert
enrollment/renewal/revocation, tamper protection, secure auto-update, and config
integrity validation. The `internal/buffer`, `internal/transport`, and
`internal/enroll` packages are placeholders marking where those land.
