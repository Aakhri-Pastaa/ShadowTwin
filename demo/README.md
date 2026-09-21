# Demo environment

The full pipeline on one machine — no Wazuh installation, no configuration.

```bash
cd demo && docker compose up --build
```

Five containers: a single-node Kafka broker in KRaft mode, a one-shot job
that creates the two topics, a generator writing Wazuh-shaped NDJSON, the
forwarder built from [`../forwarder`](../forwarder), and a consumer that
audits what arrives.

The point is not that alerts flow. Every generated alert carries a monotonic
`demo_seq`, and the consumer reads the whole topic from offset 0 and checks
that every sequence is present — so the delivery guarantee is something you
measure, not something you take on trust. It distinguishes a **gap** (a
sequence that never arrived — forbidden) from a **duplicate** (one that
arrived twice — permitted by at-least-once).

Teardown, including volumes: `docker compose down -v`

Every log excerpt below is captured from a real run of this stack.

## Startup

The forwarder's four checks pass against the real broker:

```
startup check: Kafka       OK   (connected to kafka:9092; 1 broker(s) in cluster)
startup check: Topics      OK   (wazuh-alerts, wazuh-logs exist)
startup check: Files       OK   (/var/ossec/logs/alerts/alerts.json readable; /var/ossec/logs/archives/archives.json readable)
startup check: Permissions OK   (/var/lib/shadowtwin-forwarder writable; /var/log/shadowtwin-forwarder writable)
```

One warning is expected on a cold start — the broker is still loading its
internal state when the producer first asks for an idempotence ID. It
retries on its own:

```
WARNING  librdkafka GETPID [...] Failed to acquire idempotence PID from broker kafka:9092/1: Broker: Coordinator load in progress: retrying
```

Then the consumer prints alerts, and every 10 seconds an audit line:

```
#191    HIGH L10 jump-01  sshd: brute force trying to get access to the system T1110.001
#193    med  L7  jump-01  Successful sudo to ROOT executed.                    T1548.003
#195    CRIT L12 app-02   File added to the system.                            T1105
[audit] received=401 unique=401 highest=401 duplicates=0 | NO GAPS
```

If a sequence ever went missing, the line would name it and the consumer
would exit non-zero:

```
[audit] received=4 unique=4 highest=5 duplicates=0 | !! 1 GAP(S): [3]
```

## Rotation and truncation, live

The generator rename-rotates `alerts.json` every 120 alerts, keeping five
generations like logrotate, and truncates `archives.json` every 300. From the
forwarder's log:

```
INFO     watcher    alerts: closing /var/ossec/logs/alerts/alerts.json: rotated (inode changed)
INFO     watcher    alerts: opened /var/ossec/logs/alerts/alerts.json (inode 89335)
WARNING  watcher    archives: file truncated (size 0 < offset 67077); reading from the start
```

The audit keeps reporting `NO GAPS` through both.

## Break the broker

In a second terminal:

```bash
docker compose stop kafka
```

The forwarder logs the transport errors, then the outage once, and holds
the backlog in memory rather than dropping it. This stats line was taken 30
seconds into the outage — 236 alerts produced, waiting for a broker:

```
WARNING  producer   Kafka error (retried automatically): KafkaError{code=_TRANSPORT,...Connection refused...}
WARNING  producer   all Kafka brokers are down; buffering locally and reconnecting in the background
stats: produced=868 acked=632 failed=0 parse_errors=0 reconnects=0 queued=236 in_flight=236 | ...
```

Bring it back:

```bash
docker compose start kafka
```

```
INFO     producer   Kafka connection restored; deliveries flowing again
[audit] received=808 unique=808 highest=808 duplicates=0 | NO GAPS
```

Every alert written during the outage arrived, and none twice: the idempotent
producer deduplicates its own retries within a session.

## Restart the forwarder

A clean restart flushes before it exits and resumes at the exact byte:

```bash
docker compose restart forwarder
```

```
INFO     app        received SIGTERM; shutting down
INFO     app        shutdown complete: produced=1196 acked=1196 failed=0 parse_errors=0
INFO     state      loaded offsets for 2 file(s) from /var/lib/shadowtwin-forwarder/state.json
INFO     watcher    alerts: resuming inode 89211 at offset 40082
[audit] received=1058 unique=1058 highest=1058 duplicates=0 | NO GAPS
```

`produced` equals `acked` at shutdown, so there is nothing to re-send and no
duplicates.

## Kill it, and let the log rotate while it is down

The hard case. `SIGKILL` gives the forwarder no chance to flush or
checkpoint, and the generator keeps writing — and rotating — while it is
gone:

```bash
docker compose kill -s SIGKILL forwarder
```

Wait about a minute — long enough for two or three rotations — then:

```bash
docker compose start forwarder
```

The checkpoint names a file that is now `alerts.json.3`. The forwarder finds
it by inode, drains it from the saved offset, then walks forward through
every generation rotated after it before reading the live file:

```
WARNING  watcher    alerts: file was rotated while stopped; draining /var/ossec/logs/alerts/alerts.json.3 (inode 89130) from offset 76052, then 2 later generation(s)
WARNING  watcher    alerts: draining /var/ossec/logs/alerts/alerts.json.2 (inode 89138), rotated while stopped
WARNING  watcher    alerts: draining /var/ossec/logs/alerts/alerts.json.1 (inode 89167), rotated while stopped
[audit] received=806 unique=803 highest=803 duplicates=3 | NO GAPS
```

Three duplicates and no gaps. An independent read of the whole topic from
the broker agreed: 901 messages, sequences 1–898, 0 gaps, duplicates at
sequences 353, 354 and 355 — the lines delivered just before the kill but
not yet checkpointed. That is at-least-once exactly as specified.

**This scenario found a bug.** v1.0.0 read the new file from zero on restart
and lost the old file's tail; the same kill across a single rotation left 30
gaps. It is fixed in v1.1.0 — see the [changelog](../CHANGELOG.md).

With five generations kept and a rotation every ~24 seconds, the forwarder
can be down for about two minutes and still find everything. Longer, and the
oldest generation is overwritten: the forwarder then logs an `ERROR` naming
the inode and offset that were lost, and the audit shows the gap.

## Configuration

Defaults are in the compose file and can be overridden per run:

| Variable | Default | Effect |
|---|---|---|
| `DEMO_RATE` | `5` | Alerts generated per second |
| `DEMO_ROTATE_EVERY` | `120` | Rename-rotate after N alerts; `0` disables |
| `DEMO_ROTATE_KEEP` | `5` | Rotated generations kept, like logrotate's `rotate N` |
| `DEMO_TRUNCATE_EVERY` | `300` | Truncate `archives.json` after N alerts; `0` disables |
| `DEMO_QUIET` | `0` | `1` prints only the audit line, not every alert |
| `SHADOWTWIN_LOG_LEVEL` | `INFO` | Forwarder log verbosity |

```bash
DEMO_RATE=50 DEMO_ROTATE_EVERY=500 docker compose up --build
```

## Notes

- **The consumer re-reads the whole topic on every start**, from offset 0 and
  without joining a consumer group, so restarting it re-audits everything.
  It avoids consumer groups deliberately: during a broker outage a group
  member's session times out, and on a single-node cluster it may not
  rejoin.
- **The generator's sequence survives restarts** — it is persisted in the
  shared volume, so a restarted generator continues numbering instead of
  starting again at 1.
- **On WSL, keep a terminal open.** WSL stops the Linux VM — and Docker with
  it — shortly after the last session closes, which kills every container
  and looks like a crash.

## What this is not

The generator produces synthetic alerts modelled on Wazuh's schema — rule
id, level, description, decoder, MITRE mapping, agent, `full_log` and `data`.
It is not a Wazuh installation, and the alerts describe events that never
happened. For a real deployment see the
[install instructions](../README.md#install).

The broker is single-node with `replication-factor 1` and no authentication.
`acks=all` on one replica is a weaker guarantee than on three; the delivery
logic is identical, but do not read the demo as a statement about production
durability.

## Testing it without Docker

```bash
cd demo && python tests/demo_test.py
```

Runs the real forwarder against the real generator through a rotation and a
truncation with a fake producer, then feeds the result into the consumer's
audit and asserts zero gaps. 27 checks, no container runtime required, run
in CI on every push. It covers the pipeline logic, not the compose file, the
images, or real broker behaviour — those are what the run above verified.
