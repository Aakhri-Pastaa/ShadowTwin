# Demo environment

The full pipeline on one machine, with no Wazuh installation and no
configuration.

```bash
cd demo && docker compose up --build
```

Five containers start: a single-node Kafka broker in KRaft mode, a one-shot
job that creates the two topics, a generator writing Wazuh-shaped NDJSON, the
forwarder built from [`../forwarder`](../forwarder), and a consumer that
prints what arrives.

Teardown, including volumes: `docker compose down -v`

## What you should see

The forwarder's four startup checks pass, then alerts begin arriving at the
consumer:

```
forwarder  | startup check: Kafka       OK   (connected to kafka:9092; 1 broker(s) in cluster)
forwarder  | startup check: Topics      OK   (wazuh-alerts, wazuh-logs exist)
forwarder  | startup check: Files       OK   (alerts.json readable; archives.json readable)
forwarder  | startup check: Permissions OK   (/var/lib/shadowtwin-forwarder writable)
consumer   | #1      low  L4  jump-01  syslog: User authentication failure.                 T1110
consumer   | #2      low  L4  db-01    syslog: User authentication failure.                 T1110
consumer   | #3      med  L5  jump-01  sshd: authentication failed.                         T1110.001
```

Every 10 seconds the consumer prints an audit line. Each alert carries a
monotonic `demo_seq`, so this is a real measurement rather than a status
message:

```
[audit] received=312 unique=312 highest=312 duplicates=0 | NO GAPS
```

If a record ever did go missing, the same line says so and names the
sequences, and the consumer exits non-zero:

```
[audit] received=4 unique=4 highest=5 duplicates=0 | !! 1 GAP(S): [3]
```

## The part worth watching

The generator rotates the log every 120 alerts and truncates `archives.json`
every 300 — the two cases that break naive forwarders, happening live:

```
generator  | [generator] rotated /var/ossec/logs/alerts/alerts.json -> …json.1 (new inode)
forwarder  | alerts: file truncated (size 0 < offset 13537); reading from the start
```

The audit line keeps reporting `NO GAPS` through both.

**Then break the broker.** In a second terminal:

```bash
docker compose stop kafka
```

The forwarder logs the outage once and holds the data rather than discarding
it. The consumer stops receiving, because there is nothing to receive:

```
forwarder  | WARNING  producer   all Kafka brokers are down; buffering locally and reconnecting in the background
```

Wait twenty seconds, then bring it back:

```bash
docker compose start kafka
```

```
forwarder  | INFO     producer   Kafka connection restored; deliveries flowing again
[audit] received=468 unique=468 highest=468 duplicates=0 | NO GAPS
```

`highest` has advanced past the outage and `NO GAPS` still holds: nothing
written during the outage was lost. A `duplicates` count above zero is
expected and correct — at-least-once re-sends in-flight messages, and the
audit distinguishes that from loss.

Killing the forwarder itself (`docker compose restart forwarder`) produces
the same result by a different route: it resumes from its last acknowledged
checkpoint.

## Configuration

Defaults are in the compose file and can be overridden per run:

| Variable | Default | Effect |
|---|---|---|
| `DEMO_RATE` | `5` | Alerts generated per second |
| `DEMO_ROTATE_EVERY` | `120` | Rename-rotate after N alerts; `0` disables |
| `DEMO_TRUNCATE_EVERY` | `300` | Truncate `archives.json` after N alerts; `0` disables |
| `DEMO_QUIET` | `0` | `1` prints only the audit line, not every alert |
| `SHADOWTWIN_LOG_LEVEL` | `INFO` | Forwarder log verbosity |

```bash
DEMO_RATE=50 DEMO_ROTATE_EVERY=500 docker compose up --build
```

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
audit and asserts zero gaps. 27 assertions, no container runtime required.
This runs in CI on every push; it covers the pipeline logic but not the
compose file, the images, or genuine broker behaviour.
