<div align="center">

<img src="docs/assets/hero.svg" alt="ShadowTwin: Wazuh alerts into Kafka, without losing one. The forwarder's at-least-once loop: tail, produce, commit, resume." width="100%">

<br/><br/>

**A pipeline that streams Wazuh's security alerts into Apache Kafka with at-least-once delivery and stores them in PostgreSQL exactly once — surviving log rotation, truncation, broker and database outages, and process crashes.**

[![Forwarder CI](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/forwarder.yml/badge.svg)](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/forwarder.yml)
[![Ingestor CI](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/ingestor.yml/badge.svg)](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/ingestor.yml)
[![Demo CI](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/demo.yml/badge.svg)](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/demo.yml)
[![Release](https://img.shields.io/github/v/release/Aakhri-Pastaa/ShadowTwin?color=1f6feb)](https://github.com/Aakhri-Pastaa/ShadowTwin/releases/latest)
[![License](https://img.shields.io/badge/License-Apache_2.0-1f6feb.svg)](LICENSE)

</div>

> [!NOTE]
> **Status: v1.2.0, scope frozen.** The forwarder runs as a systemd service on my homelab's Wazuh manager. The ingestor is verified in the Docker demo and in CI, but it is not deployed on the homelab, where the pipeline currently ends at Kafka.
> Designed and directed by Kunal Patil; developed with AI coding assistants. See [AI disclosure](#ai-disclosure).

## What it does

Wazuh writes every detection as one line of JSON to `alerts.json`, and the
full event stream to `archives.json`. ShadowTwin tails both files like
`tail -F`, produces each line to a Kafka topic (`wazuh-alerts`,
`wazuh-logs`), and persists its read position **only after the broker
acknowledges the write**. It runs as a systemd service on the Wazuh manager
host.

The **ingestor** reads `wazuh-alerts` from Kafka and stores each alert as a
row in a PostgreSQL `findings` table — typed columns for time, rule, severity,
ATT&CK techniques, agent and source, plus the full alert as `jsonb` — once
each, whatever the forwarder re-sent.

It is transport and storage only — no triage, enrichment, correlation or
alerting — and not the purple-team platform this repository was first
designed as ([why](#why-the-scope-is-frozen)).

<div align="center">
<img src="forwarder/assets/pipeline.svg" alt="alerts.json and archives.json tailed by the ShadowTwin Forwarder systemd service into the Kafka topics wazuh-alerts and wazuh-logs" width="600">
</div>

## Try it

The whole pipeline runs on one machine with no Wazuh installation:

```bash
cd demo && docker compose up --build
```

Kafka, a generator writing Wazuh-shaped NDJSON, the forwarder, the ingestor
and PostgreSQL, and a consumer that prints what arrives. Every alert carries a
monotonic sequence number, so loss is audited rather than asserted:

```text
[audit] received=312 unique=312 highest=312 duplicates=0 | NO GAPS
```

[`demo/README.md`](demo/README.md) walks through breaking it; what those runs
measured is under [Results](#results).

## The problem

Five things make a naive file-to-Kafka forwarder lose data:

- **Rename rotation** — the path gets a new inode while your handle holds the old one. You stop receiving data, silently and permanently.
- **`copytruncate` rotation** — the inode is unchanged but the file is truncated under your byte offset. You stall, or re-read the file and duplicate everything.
- **Non-atomic appends** — Wazuh writes with ordinary buffered I/O, so a poll can land mid-object: `{"timestamp":"2026-09-20T02:14:0`.
- **The crash window** — die between producing a message and its acknowledgement, and the next start has to know. Checkpoint optimistically and you lose in-flight records; checkpoint per record and it is unusably slow.
- **Rotation while you are stopped** — restart after the log rotated, and the checkpoint names a file that is no longer at the path. Read the new file from zero and everything written to the old one after the checkpoint is gone. v1.0.0 did exactly this; the [demo](demo/) caught it.

Broker outages compound all four, and they correlate with incidents: the
moment a host is under attack is a moment infrastructure is unstable.

## How it works

Each watched file has a checkpoint — the inode plus the byte offset after the
last line confirmed delivered. **It advances only on acknowledgement, never
on read.** Restart resumes from it; the worst case is re-sending what was in
flight.

**Contiguous-prefix acking** (`state.AckTracker`) — Kafka acknowledges out of
order, so message 3 can land before 2. Every line gets a sequence number in a
pending queue; the committed position advances only along the unbroken
acknowledged prefix. Acknowledging 3 while 2 is outstanding moves nothing.
The offset of an in-flight line is never skipped past.

**Atomic checkpoints** (`state.OffsetStore`) — write to a temp file, `fsync`,
`os.replace` into position, then `fsync` the directory so the rename survives
power loss. Never a torn state file. The previous checkpoint is kept as
`.bak` and loaded if the main file is corrupt, which is quarantined to
`.corrupt`. Written every 2 s, not per record, which bounds the duplicate
window without slowing the write path.

**Backpressure, not drops** (`producer.KafkaWriter`) — `acks=all`,
`enable.idempotence`, and `message.timeout.ms=0` so librdkafka retries
forever. When the local queue fills, `produce()` raises `BufferError`;
`send()` catches it, polls for callbacks and retries in a loop, which blocks
the readers until the broker catches up. Unread data stays in the log file,
which is the safest place for it.

**The tailer** (`watcher.FileTail`) — at every EOF it compares `os.stat()`
inode and size against the open handle, as GNU `tail` does without inotify.
New inode: drain the old handle to its end *first*, then open the new file at
zero. Size below offset: seek to zero. Deleted: close, wait, re-attach.
Trailing fragment without a newline: withhold until the newline arrives.

If the file rotated while the forwarder was **stopped**, there is no open
handle to drain. On startup the checkpointed inode is looked up among the
rotated generations — `<path>.*`, plus an optional `rotated:` glob per file —
opened at the saved offset, and drained; then every generation rotated after
it is read, oldest first, and only then the live file. Generations are
tracked by inode because names shift on each rotation, candidates must share
the path's filesystem, compressed files are never read, and the saved offset
must fall on a line boundary so a reused inode is not resumed mid-line.

**Guarantee: at-least-once.** Messages delivered but not yet checkpointed
when the process dies are re-sent. Duplicates are possible; gaps are not,
across crashes, outages and rotation — provided a rotated-away file still
exists, uncompressed, where the forwarder searches for it. If it has been
deleted or compressed before restart, the forwarder cannot read it and says
so: an `ERROR` naming the inode and the offset after which data was not
forwarded, never a silent skip. Consumers must deduplicate, typically on the
Wazuh alert ID — the ingestor does exactly that. Exactly-once from file to
Kafka would need two-phase commit across a file and a network boundary; a
duplicate alert is an inconvenience, a missing one is missing evidence.

**The ingestor** ([`ingestor/`](ingestor/)) applies the same rule on the
consuming side — commit the position only with the data — and gets
exactly-once into the table:

- **Offsets live in PostgreSQL**, written in the same transaction as the rows
  they cover. A crash leaves both or neither, so a restart re-reads exactly
  the uncommitted batch.
- **No consumer group.** Partitions are assigned directly at the stored
  offsets; nothing is committed to Kafka. A group's session can expire during
  a broker outage — the demo showed one fail to rejoin — and with offsets in
  PostgreSQL it would add nothing.
- **The Wazuh alert id is the primary key**, so a duplicate the forwarder
  delivered is a no-op (`ON CONFLICT DO NOTHING`), counted but not stored.
- **Database outage → rewind.** The batch in hand rolls back, but the
  consumer has already read past it, so recovery re-assigns every partition
  at the offset PostgreSQL holds rather than trusting its own position.
- **One bad value cannot wedge the pipeline.** A batch PostgreSQL rejects is
  retried row by row in savepoints; the row that fails is logged with its
  exact Kafka position, the rest land, and the offsets advance.

## Results

The generator rotates and truncates the log while it runs. Stop the broker
and the forwarder holds the backlog — 236 alerts in the verification run —
and delivers it on reconnect. `SIGKILL` the forwarder and keep it down across
three rotations, and on restart it drains the checkpointed file from
`alerts.json.3` and walks forward through `.2` and `.1`:

```text
[audit] received=806 unique=803 highest=803 duplicates=3 | NO GAPS
```

Three duplicates — lines delivered just before the kill but not yet
checkpointed — and no gaps. Those duplicates stop at the table. From one run
in which the ingestor was killed, the database stopped for 25 seconds, and the
forwarder killed across three rotations:

| | Messages | Duplicates | Gaps |
|---|---|---|---|
| `wazuh-alerts` topic | 1,676 | 6 | 0 |
| `findings` table | 1,725 rows | 0 | 0 |

Conditions: the Docker demo on one machine — forwarder runs at v1.1.0, the
combined run at v1.2.0, with the forwarder unchanged between them. These are
correctness figures, not throughput. Steps to reproduce:
[`demo/README.md`](demo/README.md).

## Install

| Requirement | Version | Why |
|---|---|---|
| Linux host running the Wazuh manager | Wazuh 4.x | Reads `/var/ossec/logs`; inode-based rotation |
| Python | 3.12+ | Forwarder and ingestor |
| Apache Kafka | tested with 4.0 (KRaft) | Topics `wazuh-alerts`, `wazuh-logs` |
| PostgreSQL | tested with 17 | Ingestor only |
| systemd | — | Runs the forwarder as a service |

On the Wazuh manager host:

```bash
cd forwarder && sudo ./install_service.sh
```

Creates a `nologin` `shadowtwin` user in the `wazuh` group, builds a
virtualenv, installs config to `/etc/shadowtwin-forwarder/config.yaml`
(separate from code, so upgrades never clobber a tuned deployment) and the
systemd unit, then starts and health-checks it. Root to install, not to run.

```bash
systemctl status shadowtwin-forwarder
```

Docker is supported as an alternative; the container needs the host's `wazuh`
GID via `group_add`. Configuration reference, upgrades and troubleshooting:
[`forwarder/README.md`](forwarder/README.md).

**The ingestor** runs anywhere that can reach both Kafka and PostgreSQL — it
ships as a container image, configured through environment variables, and
creates its own tables on first start. See
[`ingestor/README.md`](ingestor/README.md).

## Operation

Four startup checks run before anything is forwarded. Broker and topic
existence are probed with an `AdminClient` metadata request, so the check
cannot itself trigger topic auto-creation:

```text
ShadowTwin Forwarder v1.1.0
Forward:  /var/ossec/logs/alerts/alerts.json -> wazuh-alerts
Forward:  /var/ossec/logs/archives/archives.json -> wazuh-logs
startup check: Kafka       OK   (connected to localhost:9092; 1 broker(s) in cluster)
startup check: Topics      OK   (wazuh-alerts, wazuh-logs exist)
startup check: Files       OK   (alerts.json readable; archives.json readable)
startup check: Permissions OK   (/var/lib/shadowtwin-forwarder writable)
```

Only a permissions failure is fatal. An unreachable broker or missing source
file are warnings — both self-heal, and aborting would mean a forwarder that
refuses to start during the outage it exists to survive. Broker loss and
recovery are logged once each, not per retry. The per-minute stats line and
`--health` for monitoring are documented in
[`forwarder/README.md`](forwarder/README.md).

## Testing

```bash
cd forwarder && python tests/smoke_test.py
```

59 checks for the forwarder, run in CI with `ruff` on every change to
`forwarder/` — plus 30 for the ingestor, run against a real PostgreSQL, and 27
for the demo pipeline.
Fault injection, not happy path — each induces a failure and asserts the
recovery. The forwarder's:

| Induced | Asserted |
|---|---|
| Out-of-order acks | Committed position holds at the gap, settles both when it closes |
| Corrupted state file | Quarantined to `.corrupt`, `.bak` checkpoint loaded |
| Rename rotation | Old inode drained before the new file is read |
| Truncation | Offset resets to zero, reading resumes |
| Delete then recreate | Tailer detaches, waits, re-attaches |
| Half-written line | Withheld, then emitted once complete |
| Malformed JSON, blank lines | Skipped and settled, not retried forever |
| All-brokers-down then delivery | Reconnect counter increments exactly once |
| Shipped producer config | Accepted by the real librdkafka client |
| Kill and restart mid-stream | Resumes at exactly the right byte — no gap, no repeat |
| Rotation while stopped | Checkpointed file found by inode, drained from the saved offset, then the new file |
| Two rotations while stopped | Checkpointed file's tail, then the generation after it, then the live file — in order |
| Owed generation renamed again before opening | Still found, by inode |
| Rotated file deleted before restart | `ERROR` naming the lost offset — not a silent skip |
| Reused inode, offset mid-line | Not resumed mid-line; treated as a different file |
| Rotated file in a dated directory | Found through the configured `rotated:` glob |
| Newer compressed sibling | Ignored |

Linux required: the rotation tests need real inode semantics. The
ingestor's checks — duplicates, atomic offsets, a poison value mid-batch,
restart, database loss mid-stream — are listed in
[`ingestor/README.md`](ingestor/README.md#tests).

## Status and limitations

| Area | Status | Notes |
|---|---|---|
| Forwarder | Works | systemd service on my homelab's Wazuh manager |
| Ingestor | Demo-only | Docker demo and CI (real PostgreSQL); not on the homelab |
| Demo environment | Works | CI runs its 27 checks and validates the compose file |
| Go agent (`go-agent-v0/`) | Archived | Superseded by Wazuh; CI kept green |
| Purple-team platform | Not built | Scope frozen 2026-09-20; design in [`docs/archive/`](docs/archive/) |

- **At-least-once, not exactly-once.** Consumers must deduplicate.
- **Rotation recovery needs the rotated file to still exist, uncompressed.** Wazuh's own daily rotation moves logs into dated directories and compresses them; a forwarder that is down across it can only recover if the uncompressed file is still present. Otherwise the loss is logged, not recovered.
- **Inode-reuse detection is a one-byte heuristic** — the saved offset must follow a newline. An unrelated file passes by chance about once per average line length. A content fingerprint in the checkpoint would close this.
- **No TLS or SASL to the broker.** Plaintext and unauthenticated — trusted network segments only. Wazuh alerts carry hostnames, usernames and command lines; see [SECURITY.md](SECURITY.md).
- **Single broker** in the default config, though `bootstrap_servers` takes a list.
- **Linux only** — inode-based rotation detection, systemd, POSIX `os.replace`.
- **Tested against Wazuh 4.x on one homelab deployment.** Not validated across versions or benchmarked at production rates.
- **One ingestor instance.** Partitions are assigned directly, without a consumer group; a second instance would duplicate the work (though not the rows).
- **No inspection of content.** The forwarder only validates that each line is JSON; the ingestor maps fields to columns and keeps the rest verbatim.

## Why the scope is frozen

I designed ShadowTwin as a closed-loop purple-team lab: Wazuh detects, an
Evaluator triages, an Attacker proves exploitability in a sandbox, a Defender
advises a fix, a human applies it, the Attacker re-runs to prove closure.
**None of it was built.** On 2026-09-20 I froze the scope at the ingestion
layer and archived the design — reasoning in
[`docs/DECISIONS.md`](docs/DECISIONS.md), design in
[`docs/archive/`](docs/archive/).

It was the second time I cut on that principle. The project started with a
custom Go agent — mutual-TLS transport, CSR enrollment with the key never
leaving the host, certificate renewal and revocation, a crash-safe disk
queue. It was complete and tested, and I archived it once Wazuh proved the
better foundation: reuse the mature tool, build only the differentiating
glue. It is still at [`go-agent-v0/`](go-agent-v0/), still green in CI.

The ingestor is the one piece of the original design built after the freeze:
it extends the pipeline rather than reviving the platform, and its schema is
the ingestion slice of the Finding object from the archived
[`architecture-v2.md`](docs/archive/architecture-v2.md).

## Documentation

| Need | Start here |
|---|---|
| What is actually running | [`docs/PROJECT_STATUS.md`](docs/PROJECT_STATUS.md) |
| Deployment topology | [`docs/TOPOLOGY.md`](docs/TOPOLOGY.md) |
| Why things are the way they are | [`docs/DECISIONS.md`](docs/DECISIONS.md) · [ADRs](docs/adr/) |
| Forwarder configuration, operations, troubleshooting | [`forwarder/README.md`](forwarder/README.md) |
| Deployment and troubleshooting deep dives | [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md) · [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md) |
| Running it without Wazuh | [`demo/`](demo/) |
| Findings schema, delivery semantics, example queries | [`ingestor/README.md`](ingestor/README.md) |
| Build history | [`DEVLOG.md`](DEVLOG.md) |
| The original design, never implemented | [`docs/archive/`](docs/archive/) |

## AI disclosure

I designed ShadowTwin and made its decisions: the problem it solves, the
pivot from a custom Go agent to Wazuh, freezing the scope at the ingestion
layer, the delivery guarantees and the trade-offs behind them — at-least-once
into Kafka, exactly-once into PostgreSQL, no consumer group — and when to
release. AI coding assistants (Claude Code) did much of the development under
my direction: writing and refactoring the Python and Go code, drafting tests
and documentation, and running builds, tests and the demo.

| Area | Who |
|---|---|
| Idea, scope, architecture, design decisions | Me |
| Trade-offs, what to cut, when to release | Me |
| Code, tests, documentation drafts, tooling runs | AI assistants, directed by me |
| Review, verification, approval to merge | Me |

AI output is checked, not trusted. Nothing reaches `main` without my
approval, and every number here comes from a reproducible run. Checking has
caught real errors: the demo's sequence-number auditor exposed a data-loss
bug in the released v1.0.0, fixed in v1.1.0, and the v1.0.0 docs claimed 44
test assertions where the suite executed 42.

## Built with

Python 3.12 · [confluent-kafka](https://github.com/confluentinc/confluent-kafka-python) (librdkafka) · Apache Kafka 4 (KRaft) · PostgreSQL 17 · [psycopg 3](https://www.psycopg.org/psycopg3/) · Wazuh · systemd · Docker

Licensed under [Apache-2.0](LICENSE).
