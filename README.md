<div align="center">

<img src="docs/assets/hero.svg" alt="ShadowTwin — Wazuh alerts into Kafka, without losing one" width="100%">

<br/><br/>

**A security telemetry ingestion pipeline: tails Wazuh's alert logs and streams them into Apache Kafka with at-least-once delivery — through rotation, truncation, broker outages and crashes.**

[![Forwarder CI](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/forwarder.yml/badge.svg)](https://github.com/Aakhri-Pastaa/ShadowTwin/actions/workflows/forwarder.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-1f6feb.svg)](LICENSE)
[![Scope](https://img.shields.io/badge/scope-frozen-8957e5.svg)](docs/DECISIONS.md)

![Wazuh](https://img.shields.io/badge/Wazuh-1a3d6d?logo=wazuh&logoColor=white)
![Kafka](https://img.shields.io/badge/Apache_Kafka-231f20?logo=apachekafka&logoColor=white)
![Python](https://img.shields.io/badge/Python_3.12-3776ab?logo=python&logoColor=white)
![systemd](https://img.shields.io/badge/systemd-30d475?logo=linux&logoColor=black)

</div>

---

## The problem

A SIEM's alert log is a hostile file to read. It rotates under you, gets
truncated by `copytruncate`, is deleted and recreated, and is written to
while you are reading it. Tail it naively and you will silently lose alerts
at every rotation, or re-read the whole file and duplicate them.

Downstream of that, the broker you are shipping to will go away sometimes —
and a forwarder that drops events during the outage leaves a hole in a
security audit trail exactly when something interesting may be happening.

**ShadowTwin Forwarder** is the piece that handles this properly: it follows
Wazuh's `alerts.json` and `archives.json` the way `tail -F` does, ships every
line to Kafka, and only advances its saved position once the broker has
confirmed the write.

## How it works

<div align="center">
<img src="forwarder/assets/pipeline.svg" alt="Wazuh host tails alerts.json and archives.json through the ShadowTwin Forwarder systemd service into the Kafka topics wazuh-alerts and wazuh-logs" width="640">
</div>

The delivery guarantee comes from three things working together:

| | |
|---|---|
| **Contiguous-prefix acking** | Kafka may acknowledge messages out of order. The saved offset advances only along the unbroken run of acknowledged lines, so the position of a line still in flight is never skipped past. |
| **Atomic checkpoints** | Offsets are written to a temp file, `fsync`'d, then `os.replace`'d — never a torn write. The previous checkpoint is kept as `.bak` and loaded automatically if the main file is lost or corrupt. |
| **Backpressure, not drops** | When the local producer queue fills because the broker is unreachable, `send()` blocks and serves delivery callbacks instead of discarding events. `message.timeout.ms=0` means librdkafka retries forever. |

The result is **at-least-once**: a crash re-sends the messages that were
still in flight, and never skips one. Duplicates are possible; gaps are not.

The file tailer survives rotation (new inode), truncation, deletion and
re-creation without a restart, and withholds a partially written last line
until its newline arrives.

## Install

**On a Wazuh manager host** (needs read access to `/var/ossec/logs`):

```bash
cd forwarder
sudo ./install_service.sh
```

That creates an unprivileged `shadowtwin` service user in the `wazuh` group,
builds a virtualenv, installs `/etc/shadowtwin-forwarder/config.yaml` and the
systemd unit, then starts the service and health-checks it.

Day to day it is pure systemd — no venv activation, no manual `python app.py`:

```bash
systemctl status shadowtwin-forwarder
```

```bash
journalctl -u shadowtwin-forwarder -f
```

Docker is an alternative to systemd — see [`forwarder/README.md`](forwarder/README.md).

## Example output

Startup logs every operational check before it forwards anything:

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
stats: produced=1284 acked=1284 failed=0 parse_errors=0 reconnects=0 queued=0 in_flight=0 | offsets: alerts=418223, archives=9912014
```

And when the broker goes away, it says so once and keeps the data:

```
WARNING  producer   all Kafka brokers are down; buffering locally and reconnecting in the background
INFO     producer   Kafka connection restored; deliveries flowing again
```

Health checks are scriptable — exit 0 when healthy:

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

44 assertions, run on every push by [CI](.github/workflows/forwarder.yml).
They are fault-injection tests, not happy-path checks — they cover log
rotation, truncation, deletion and re-creation, a corrupt state file and
recovery from the backup checkpoint, resume-at-offset, malformed JSON,
broker-down and reconnect accounting, and a full restart that must resume
exactly where it stopped.

Linux is required: the rotation tests rely on real inode semantics.

## Limitations

Stated plainly, because they matter if you are thinking of running this:

- **At-least-once, not exactly-once.** A crash can re-deliver in-flight messages. Downstream consumers must tolerate duplicates.
- **No TLS or SASL to Kafka.** The connection is plaintext and unauthenticated — suitable only on a trusted network segment. Wazuh alerts contain sensitive data; see [SECURITY.md](SECURITY.md).
- **Single-broker configuration** as shipped, though `bootstrap_servers` accepts a list.
- **Linux only.** systemd packaging, inode-based rotation detection, journald-oriented operations.
- **Tested against Wazuh 4.x on one homelab deployment.** Not validated across Wazuh versions or at scale.
- **The wider platform was never built.** See below.

## Scope, and why it is frozen

ShadowTwin was originally designed as a closed-loop purple-team lab: Wazuh
detects, an Evaluator triages, an Attacker proves what is exploitable, a
Defender recommends a fix, a human applies it, and the Attacker re-runs to
prove closure.

**None of those components were built.** On 2026-09-20 the scope was frozen
at the ingestion layer and the design was archived rather than carried
indefinitely as aspiration. The reasoning is in
[`docs/DECISIONS.md`](docs/DECISIONS.md); the original design lives in
[`docs/archive/`](docs/archive/).

This is the second time the project has cut something on the same principle.
It began with a hand-built Go telemetry agent — mTLS transport, CSR
enrollment, certificate renewal and revocation, a crash-safe on-disk buffer,
one-command onboarding, all complete and tested. It was archived once Wazuh
proved the better base: *reuse the mature tool, build only the
differentiating glue.* That code is still here, still green in CI, at
[`go-agent-v0/`](go-agent-v0/).

Keeping what earns its place and archiving the rest is the point, not an
apology.

## Documentation

| | |
|---|---|
| 📊 [PROJECT_STATUS](docs/PROJECT_STATUS.md) | What is actually running |
| 🏗️ [TOPOLOGY](docs/TOPOLOGY.md) | Infrastructure diagram |
| 🧭 [DECISIONS](docs/DECISIONS.md) · [ADRs](docs/adr/) | Why things are the way they are |
| 🧰 [forwarder/README](forwarder/README.md) | Configuration, operations, troubleshooting |
| 🧪 [DEPLOYMENT](docs/DEPLOYMENT.md) · [TROUBLESHOOTING](docs/TROUBLESHOOTING.md) | Deep dives |
| 📓 [DEVLOG](DEVLOG.md) | How it was built, in order |
| 🗄️ [archive/](docs/archive/) | The original platform design — never built, kept for the record |

## Technologies

Python 3.12 · [confluent-kafka](https://github.com/confluentinc/confluent-kafka-python) (librdkafka) · Apache Kafka 4 (KRaft) · Wazuh · systemd · Docker

Licensed under [Apache-2.0](LICENSE).
