<div align="center">

<img src="docs/assets/hero.svg" alt="ShadowTwin — Wazuh alerts into Kafka, without losing one" width="100%">

<br/><br/>

**A Python service that streams Wazuh's security alerts into Apache Kafka with at-least-once delivery, surviving log rotation, truncation, broker outages and process crashes.**

[![Forwarder CI](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/forwarder.yml/badge.svg)](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/forwarder.yml)
[![Release](https://img.shields.io/github/v/release/Aakhri-Pastaa/ShadowTwin?color=1f6feb)](https://github.com/Aakhri-Pastaa/ShadowTwin/releases/latest)
[![License](https://img.shields.io/badge/License-Apache_2.0-1f6feb.svg)](LICENSE)
[![Scope](https://img.shields.io/badge/scope-frozen-8957e5.svg)](docs/DECISIONS.md)

![Wazuh](https://img.shields.io/badge/Wazuh-1a3d6d?logo=wazuh&logoColor=white)
![Kafka](https://img.shields.io/badge/Apache_Kafka-231f20?logo=apachekafka&logoColor=white)
![Python](https://img.shields.io/badge/Python_3.12-3776ab?logo=python&logoColor=white)
![systemd](https://img.shields.io/badge/systemd-30d475?logo=linux&logoColor=black)

</div>

---

## What it does

**Wazuh** is an open-source security monitoring platform. Agents installed on
monitored hosts report events — authentication attempts, file integrity
changes, process activity — to a central Wazuh manager, which evaluates them
against a decoder and rule engine and writes every resulting detection as a
single line of JSON into `/var/ossec/logs/alerts/alerts.json`. A second file,
`archives.json`, holds the full unfiltered event stream. Both are NDJSON:
newline-delimited JSON, one complete object per line, appended continuously.

**Apache Kafka** is a distributed, partitioned, replicated commit log.
Producers append records to named topics; consumers read them independently
at their own pace, and records persist for a configured retention period
rather than being consumed destructively. It is the standard transport for
security event pipelines because several downstream systems — storage,
correlation, alerting — can each read the same stream without competing.

**ShadowTwin Forwarder is the link between the two.** It follows both Wazuh
log files the way `tail -F` does, produces each complete line to a Kafka
topic (`alerts.json` → `wazuh-alerts`, `archives.json` → `wazuh-logs`), and
persists its read position only after the broker has acknowledged the write.
It runs as a systemd service on the Wazuh manager host.

Its entire design goal is that the link is **lossless**: every alert Wazuh
writes reaches Kafka, exactly once in the common case and at least once in
the presence of failure.

## Why this is a non-trivial problem

Reading lines from a file and sending them over a network appears trivial.
Four properties of the environment make a naive implementation lose data.

**Log files are rewritten underneath the reader.** Wazuh's logs are rotated
on a schedule. Rotation takes one of two forms, and both are hostile to a
reader holding an open file handle. In *rename rotation*, the path is renamed
(`alerts.json` → `alerts.json.1`) and a new file is created at the original
path, which means the path now resolves to a different inode while the open
handle still points at the old one. In *`copytruncate` rotation*, the file's
contents are copied elsewhere and the original is truncated to zero bytes in
place, which leaves the inode unchanged but the reader's byte offset pointing
far beyond the new end of file. A reader that ignores the first case
silently stops receiving data forever; one that ignores the second either
stalls or re-reads the entire file and duplicates everything in it.

**Records are written non-atomically.** Wazuh appends to the log with
ordinary buffered writes. A reader polling at the wrong moment sees a line
that ends mid-object — `{"timestamp":"2026-09-20T02:14:0` — which is not
parseable JSON and must not be emitted. The reader has to distinguish "end of
file" from "end of a partially written line" and withhold the latter until
its newline arrives.

**The broker is not continuously available.** Kafka brokers restart for
upgrades, networks partition, and DNS fails. A forwarder that drops records
while it cannot connect produces a gap in the security log. Gaps are worst
precisely when they matter most, because outages and incidents correlate:
the moment a host is under attack is also a moment infrastructure is likely
to be unstable.

**Crashes occur between reading a record and confirming its delivery.** If
the process is killed after producing a message but before the broker
acknowledges it, the next start must decide whether that record was
delivered. Recording the read position optimistically — before
acknowledgement — loses every in-flight record on every crash. Recording it
only after a full flush of every message would mean a checkpoint per record,
which is unusably slow.

Handling all four correctly is the actual content of this project.

## How it works

<div align="center">
<img src="forwarder/assets/pipeline.svg" alt="On the Wazuh host, alerts.json and archives.json are tailed by the ShadowTwin Forwarder systemd service and streamed into the Kafka topics wazuh-alerts and wazuh-logs" width="640">
</div>

The forwarder maintains, for each watched file, a checkpoint consisting of
the inode it was reading and the byte offset immediately after the last line
it is certain was delivered. That checkpoint advances **only after Kafka
acknowledges the write** — never before. On restart, reading resumes from the
checkpoint, so the worst case is re-sending records that were in flight when
the process died.

Three mechanisms make that ordering rule hold under real conditions.

### Contiguous-prefix acknowledgement tracking

Kafka acknowledges asynchronously and out of order: the delivery callback for
message 3 can fire before that of message 2. If the checkpoint simply
advanced to the offset of whichever line was most recently acknowledged, an
acknowledgement for line 3 while line 2 was still in flight would move the
checkpoint past line 2 — and a crash at that instant would lose it
permanently.

`state.AckTracker` prevents this. Every line read is assigned a monotonically
increasing sequence number and appended to a pending queue with its inode and
end offset. When an acknowledgement arrives, the sequence is marked, and the
committed position advances only along the **unbroken prefix** of acknowledged
sequences, popping from the front of the queue. Acknowledging 3 while 2 is
outstanding therefore moves nothing; when 2 lands, both are settled in one
step. The offset of a line still in flight is never skipped past.

### Atomic, crash-safe checkpoints

A checkpoint written in place can be torn by a crash mid-write, leaving a
state file that is neither the old value nor the new one. `state.OffsetStore`
writes to a temporary file in the same directory, calls `fsync` on it to
force the data to physical storage, then moves it into place with
`os.replace`, which is atomic on POSIX. The containing directory is then
`fsync`'d so the rename itself survives power loss.

The previous checkpoint is retained as `state.json.bak`. If the main file is
missing or unparseable at startup, the backup is loaded instead and the
corrupt file is moved aside to `state.json.corrupt` for inspection. Recovering
from the backup means replaying from a slightly older position — a few
duplicate records, never a gap.

Checkpoints are written on an interval (2 seconds by default) rather than per
record, which bounds the duplicate window without making the write path slow.

### Backpressure instead of dropping

The Kafka producer is configured with `acks=all`, so a message is
acknowledged only once every in-sync replica holds it; `enable.idempotence`,
which prevents duplication and reordering within a producer session; and
`message.timeout.ms=0`, which instructs librdkafka to retry indefinitely
rather than expiring messages.

When the broker is unreachable, messages accumulate in the local producer
queue. Once that queue is full, `producer.produce()` raises `BufferError`.
Rather than discarding the record, `KafkaWriter.send()` catches it, polls for
delivery callbacks, and retries in a loop — which blocks the caller and
therefore naturally pauses the file readers until the broker catches up. The
data stays on disk in the log file, unread, which is the safest place for it.
An all-brokers-down transition is logged once rather than on every retry, and
recovery is counted and reported in the periodic statistics line.

### The file tailer

`watcher.FileTail` follows a single file. Whenever a read reaches end of file,
it compares the inode and size reported by `os.stat()` on the path against the
handle it is currently reading — the same strategy GNU `tail` uses when
inotify is unavailable.

- **Rename rotation** (`st_ino` differs from the open handle's inode): the old
  handle is drained to its true end *first*, so nothing written between the
  last poll and the rename is lost, and only then is the new file opened and
  read from the beginning.
- **Truncation** (`st_size` is less than the current offset): the handle is
  seeked back to zero and reading restarts, with a warning.
- **Deletion**: the final partial line is drained, the handle closed, and the
  tailer waits, re-attaching automatically when the path reappears.
- **Partial lines**: a trailing fragment without a newline is buffered and
  withheld until the newline arrives, so malformed JSON is never produced.

Data already read is never re-scanned, and the process sleeps between polls
rather than spinning.

### The resulting guarantee

**At-least-once delivery.** After a crash or an unclean shutdown, records that
were in flight are sent again. Duplicates are possible; gaps are not.
Consumers of `wazuh-alerts` and `wazuh-logs` must therefore be idempotent —
typically by deduplicating on the Wazuh alert ID.

That trade-off is deliberate. Exactly-once across a file boundary and a
network boundary would require a transactional sink and two-phase commit. A
duplicate alert is an inconvenience downstream; a missing alert is missing
evidence.

## Installation

The forwarder runs on the Wazuh manager host, because it requires local read
access to `/var/ossec/logs`.

```bash
cd forwarder
sudo ./install_service.sh
```

The installer creates an unprivileged `shadowtwin` system user with no login
shell, adds it to the `wazuh` group so it can read the mode-0640 log files,
builds a virtualenv, installs the configuration to
`/etc/shadowtwin-forwarder/config.yaml` — separate from the code, so an
upgrade never overwrites a tuned deployment — installs the systemd unit, then
starts the service and verifies it came up. Root is required to install, not
to run.

Thereafter it is managed entirely through systemd, with `Restart=always` and
boot persistence:

```bash
systemctl status shadowtwin-forwarder
```

```bash
journalctl -u shadowtwin-forwarder -f
```

A Dockerfile and Compose file are provided as an alternative to systemd; the
container needs the host's `wazuh` GID supplied via `group_add` to read the
bind-mounted logs. See [`forwarder/README.md`](forwarder/README.md) for
configuration reference, upgrade procedure and troubleshooting.

## Operation

Before forwarding anything, the service runs four startup checks and logs the
result of each. Broker reachability and topic existence are probed with a
Kafka `AdminClient` metadata request rather than a producer request, so the
check cannot itself trigger broker-side topic auto-creation:

```
========================================================
ShadowTwin Forwarder v1.0.0
Python:   3.12.3
Kafka:    localhost:9092
Forward:  /var/ossec/logs/alerts/alerts.json -> wazuh-alerts
Forward:  /var/ossec/logs/archives/archives.json -> wazuh-logs
========================================================
startup check: Kafka       OK   (connected to localhost:9092; 1 broker(s) in cluster)
startup check: Topics      OK   (wazuh-alerts, wazuh-logs exist)
startup check: Files       OK   (alerts.json readable; archives.json readable)
startup check: Permissions OK   (/var/lib/shadowtwin-forwarder writable)
```

Only a permissions failure is fatal. An unreachable broker or a missing source
file are logged as warnings and the service starts anyway, because both
conditions heal themselves: the producer buffers and reconnects, and the
watcher attaches as soon as the file appears. Aborting on them would mean a
forwarder that refuses to start during the outage it exists to survive.

A statistics line is emitted every 60 seconds. `produced` counts messages
handed to the producer, `acked` counts broker confirmations, `in_flight` is
the number of lines read but not yet settled, and `offsets` reports the
committed byte position per file:

```
stats: produced=1284 acked=1284 failed=0 parse_errors=0 reconnects=0 queued=0 in_flight=0 | offsets: alerts=418223, archives=9912014
```

Broker loss and recovery are each logged once, not per retry:

```
WARNING  producer   all Kafka brokers are down; buffering locally and reconnecting in the background
INFO     producer   Kafka connection restored; deliveries flowing again
```

Two check modes exit 0 when healthy and are safe to run from monitoring or
cron. Both are print-only and will not create or chown the service's log
files, so running them interactively as root cannot break the service:

```bash
python app.py --health
```

```bash
python app.py --validate-config
```

## Testing

```bash
cd forwarder && python tests/smoke_test.py
```

44 assertions, executed in CI on every push alongside `ruff`. They are
fault-injection tests rather than happy-path checks — each one induces a
specific failure and asserts the correct recovery:

- **Acknowledgement ordering** — acknowledging out of order must not advance the committed position past a pending line; acknowledging the gap must settle both at once.
- **Checkpoint durability** — a round-trip through the state file; a deliberately corrupted state file must be quarantined and the `.bak` checkpoint loaded in its place.
- **Rotation** — renaming the file mid-read must drain the old inode before reading the new one.
- **Truncation** — shrinking the file must reset the offset to zero and resume.
- **Deletion and recreation** — the tailer must detach, wait, and re-attach.
- **Partial lines** — a fragment without a newline must be withheld, then emitted once completed.
- **Malformed input** — unparseable JSON and blank lines must be skipped and their offsets settled, not retried forever.
- **Broker failure accounting** — an all-brokers-down transition followed by a successful delivery must increment the reconnect counter exactly once.
- **Producer configuration** — the real librdkafka client must accept the shipped producer settings.
- **End-to-end restart** — a full `app.run()` against a fake producer, killed and restarted, must resume at exactly the right byte and re-send neither more nor less than the unacknowledged remainder.

Linux is required: the rotation and truncation tests depend on real inode
semantics.

## Limitations

- **At-least-once, not exactly-once.** In-flight messages are re-delivered after a crash. Consumers must deduplicate.
- **No TLS or SASL to the broker.** The shipped configuration is plaintext and unauthenticated, which is appropriate only on a trusted network segment. Wazuh alerts contain hostnames, usernames, file paths and command lines — see [SECURITY.md](SECURITY.md).
- **Single broker in the default configuration**, though `kafka.bootstrap_servers` accepts a list.
- **Linux only.** Inode-based rotation detection, systemd packaging, POSIX `os.replace` atomicity.
- **Tested against Wazuh 4.x on a single homelab deployment.** Not validated across Wazuh versions, nor benchmarked at production event rates.
- **Transport only.** The forwarder does not parse, enrich, correlate, alert on or store alerts beyond producing them to Kafka. It deliberately does not inspect alert contents past validating that each line is syntactically valid JSON.

## Scope, and why it is frozen

ShadowTwin was originally designed as a closed-loop purple-team lab: Wazuh
detects, an Evaluator agent triages findings that rules cannot resolve, an
Attacker agent validates exploitability inside a sandbox and attaches proof,
a Defender agent produces an advisory-only remediation, a human applies it,
and the Attacker re-runs to prove the finding is closed.

**None of those components were built.** On 2026-09-20 the scope was frozen
at the ingestion layer, and the design was archived rather than carried
indefinitely as aspiration. The reasoning is recorded in
[`docs/DECISIONS.md`](docs/DECISIONS.md); the original architecture is
preserved in [`docs/archive/`](docs/archive/).

This is the second time the project has cut something on the same principle.
It began with a custom Go telemetry agent — mutual-TLS transport, one-time
token and CSR enrollment with the private key never leaving the host,
certificate renewal and revocation, a crash-safe bounded on-disk queue with
dead-lettering, and one-command onboarding that installs a hardened systemd
unit. It was complete and tested. It was archived once Wazuh proved the better
foundation, under the principle *reuse the mature tool, build only the
differentiating glue*. That code remains at
[`go-agent-v0/`](go-agent-v0/) and is still kept green in CI.

## Documentation

| | |
|---|---|
| 📊 [PROJECT_STATUS](docs/PROJECT_STATUS.md) | What is actually running |
| 🏗️ [TOPOLOGY](docs/TOPOLOGY.md) | Deployment topology |
| 🧭 [DECISIONS](docs/DECISIONS.md) · [ADRs](docs/adr/) | Why things are the way they are |
| 🧰 [forwarder/README](forwarder/README.md) | Configuration reference, operations, troubleshooting |
| 🧪 [DEPLOYMENT](docs/DEPLOYMENT.md) · [TROUBLESHOOTING](docs/TROUBLESHOOTING.md) | Deep dives |
| 📓 [DEVLOG](DEVLOG.md) | Build history, in order |
| 🗄️ [archive/](docs/archive/) | The original platform design — never implemented, kept for the record |

## Built with

Python 3.12 · [confluent-kafka](https://github.com/confluentinc/confluent-kafka-python) (librdkafka) · Apache Kafka 4 (KRaft) · Wazuh · systemd · Docker

Licensed under [Apache-2.0](LICENSE).
