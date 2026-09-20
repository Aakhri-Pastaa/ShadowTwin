<div align="center">

<img src="docs/assets/hero.svg" alt="ShadowTwin — Wazuh alerts into Kafka, without losing one" width="100%">

<br/><br/>

**Security software watches your computers and writes down anything suspicious. ShadowTwin makes sure none of those notes go missing on the way to wherever you keep them.**

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

## What this is

If you run security monitoring on a company's computers, something has to be
watching for trouble: a failed login at 3am, a file changing that shouldn't,
a program behaving oddly. The tool doing that watching here is **Wazuh**, and
every time it notices something, it writes a line into a file.

That file is not where the story ends. Those records need to travel somewhere
they can be searched, alerted on, and kept as evidence. Moving them is the
job of a **forwarder** — and it sounds like the most boring problem in
security right up until it loses something.

**ShadowTwin is that forwarder, built so it doesn't.**

<details>
<summary><b>In one technical sentence</b></summary>

<br>

A Python 3.12 service that tails Wazuh's NDJSON `alerts.json` and
`archives.json` and produces each line to Apache Kafka with at-least-once
delivery, using contiguous-prefix acknowledgement tracking over atomically
checkpointed file offsets.

</details>

## Why this is harder than it sounds

Copying lines out of a file sounds trivial. Here is what actually goes wrong.

**The file moves while you read it.** Log files don't grow forever — the
system periodically renames the current one and starts a fresh one, or empties
it in place. Both happen without warning, while you're mid-read. Handle it
carelessly and you either skip everything written during the switch, or start
the new file from the beginning and send thousands of records twice.

**The destination isn't always there.** Networks drop, brokers restart. A
forwarder that discards records while it can't connect leaves a gap in the
log — and gaps appear exactly when something interesting is happening, because
that's when systems are under stress.

**Crashes land mid-sentence.** If the process dies after reading a line but
before confirming it was delivered, the next start has to know that. Guess
optimistically and the record is gone forever; guess pessimistically for
everything and you drown downstream systems in duplicates.

The difference between a forwarder that handles these and one that doesn't
only shows up on the day it matters — when someone asks what happened at
02:14, and the answer needs to be *"here"*, not *"we may have dropped it."*

## How it works

<div align="center">
<img src="forwarder/assets/pipeline.svg" alt="On the Wazuh host, alerts.json and archives.json are tailed by the ShadowTwin Forwarder systemd service and streamed into the Kafka topics wazuh-alerts and wazuh-logs" width="640">
</div>

In plain terms: it follows both Wazuh log files the way the Unix `tail -F`
command does, sends every new line onward, and **only writes down its place in
the file once the destination has confirmed it received that line.** Bookmark
after delivery, never before. If it crashes, it restarts from the last
confirmed bookmark.

That single ordering rule is what makes losing a record impossible. Three
mechanisms make it hold in practice:

| Mechanism | What it does |
|---|---|
| **Contiguous-prefix acking** | Kafka may confirm messages out of order — #3 can land before #2. The saved position advances only along the unbroken run of confirmed lines, so the position of a line still in flight is never skipped past. Confirming #3 while #2 is pending moves nothing. |
| **Atomic checkpoints** | The bookmark is written to a temporary file, flushed to disk with `fsync`, then swapped into place with `os.replace`. A power cut leaves either the old bookmark or the new one, never a half-written one. The previous checkpoint is kept as `.bak` and restored automatically if the main file is corrupt or missing. |
| **Backpressure, not drops** | When the outbound queue fills because the broker is unreachable, `send()` blocks and keeps servicing delivery callbacks instead of discarding events — which naturally pauses the file readers until the broker catches up. `message.timeout.ms=0` tells librdkafka to retry forever. |

The guarantee this produces is **at-least-once**: after a crash, messages
that were in flight get sent again. Duplicates are possible. **Gaps are not.**
That trade is deliberate — a duplicate is a downstream inconvenience, a gap
is missing evidence.

<details>
<summary><b>How the file tailer survives rotation, truncation and deletion</b></summary>

<br>

At every end-of-file it compares the inode and size from `os.stat(path)`
against the handle it is currently reading — the same strategy GNU `tail`
uses when inotify isn't available.

- **Rotation** (path now points at a new inode): drain the old handle to its
  end first, then open the new file from the beginning. Nothing written
  during the switch is skipped.
- **Truncation** (`copytruncate`-style, size < current offset): seek to 0 and
  re-read.
- **Deletion**: close the handle, keep waiting, re-attach when the path
  reappears.
- **Partial writes**: a final line without a newline is withheld until its
  newline arrives, so half-written JSON is never emitted.

Data already read is never re-scanned, and between polls the process sleeps.

</details>

## Install

**On a Wazuh manager host** — it needs to read `/var/ossec/logs`:

```bash
cd forwarder
sudo ./install_service.sh
```

The installer creates an unprivileged `shadowtwin` system user in the `wazuh`
group, builds a virtualenv, installs `/etc/shadowtwin-forwarder/config.yaml`
and a systemd unit, then starts the service and health-checks it. Root is
needed for the install, not to run.

After that it's a normal system service — it starts on boot and restarts
itself if it crashes:

```bash
systemctl status shadowtwin-forwarder
```

```bash
journalctl -u shadowtwin-forwarder -f
```

Docker is supported as an alternative to systemd — see
[`forwarder/README.md`](forwarder/README.md).

## What it looks like running

It checks everything it depends on *before* forwarding anything, and says so:

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

Once a minute it reports what it has done. `produced` is lines sent,
`acked` is lines the broker confirmed, and `in_flight=0` means nothing is
unaccounted for:

```
stats: produced=1284 acked=1284 failed=0 parse_errors=0 reconnects=0 queued=0 in_flight=0 | offsets: alerts=418223, archives=9912014
```

And when the destination disappears, it says so once, holds the data, and
picks up where it left off — rather than filling the log with errors or
quietly dropping records:

```
WARNING  producer   all Kafka brokers are down; buffering locally and reconnecting in the background
INFO     producer   Kafka connection restored; deliveries flowing again
```

Health checks exit 0 when healthy, so monitoring can use them directly:

```bash
python app.py --health
```

## Testing

```bash
cd forwarder && python tests/smoke_test.py
```

**44 assertions, run automatically on every push.** They aren't happy-path
checks — nearly all of them deliberately break something and verify the
forwarder survives it:

- rotate, truncate, delete and recreate the log mid-read
- corrupt the bookmark file, then recover from the backup checkpoint
- feed it malformed JSON and blank lines
- simulate the broker vanishing and returning, checking the reconnect count
- kill a run and restart it, asserting it resumes at exactly the right line —
  no gap, no repeat

Linux is required: the rotation tests depend on real inode behaviour.

## What it doesn't do

Stated plainly, because they matter if you're thinking of running this:

- **At-least-once, not exactly-once.** A crash can re-deliver in-flight messages. Whatever consumes the topics must tolerate duplicates.
- **No TLS or SASL to Kafka.** The connection is plaintext and unauthenticated — fine on a trusted network segment, not across an untrusted one. Wazuh alerts contain hostnames, usernames and command lines; see [SECURITY.md](SECURITY.md).
- **Single broker** in the shipped config, though `bootstrap_servers` takes a list.
- **Linux only** — systemd packaging, inode-based rotation detection.
- **Tested against Wazuh 4.x on one homelab deployment.** Not validated across Wazuh versions or at scale.
- **It only moves the data.** It doesn't analyse, alert on, or store it.

## Scope, and why it's frozen

ShadowTwin was originally designed as something much larger: a self-checking
security lab where Wazuh detects a problem, an AI agent triages it, another
one proves whether it's genuinely exploitable, a third recommends a fix, a
human applies it, and the attack is re-run to prove the hole is actually
closed.

**None of that was built.** On 2026-09-20 the scope was frozen at the part
that was finished and working, and the rest was archived rather than carried
forever as an intention. The reasoning is in
[`docs/DECISIONS.md`](docs/DECISIONS.md); the original design is preserved in
[`docs/archive/`](docs/archive/).

This is the second time the project cut something on the same principle. It
began with a hand-built Go telemetry agent — mutual-TLS transport, certificate
enrollment, renewal and revocation, a crash-safe on-disk queue, one-command
onboarding — all complete and tested. It was archived once Wazuh proved the
better foundation: *reuse the mature tool, build only the part that's
genuinely yours.* That code is still here and still passing its tests, at
[`go-agent-v0/`](go-agent-v0/).

Finishing one thing properly beat carrying five unfinished ones. That's the
point, not an apology.

## Documentation

| | |
|---|---|
| 📊 [PROJECT_STATUS](docs/PROJECT_STATUS.md) | What is actually running |
| 🏗️ [TOPOLOGY](docs/TOPOLOGY.md) | How the pieces are deployed |
| 🧭 [DECISIONS](docs/DECISIONS.md) · [ADRs](docs/adr/) | Why things are the way they are |
| 🧰 [forwarder/README](forwarder/README.md) | Configuration, operations, troubleshooting |
| 🧪 [DEPLOYMENT](docs/DEPLOYMENT.md) · [TROUBLESHOOTING](docs/TROUBLESHOOTING.md) | Deep dives |
| 📓 [DEVLOG](DEVLOG.md) | How it was built, in order |
| 🗄️ [archive/](docs/archive/) | The original platform design — never built, kept for the record |

## Built with

Python 3.12 · [confluent-kafka](https://github.com/confluentinc/confluent-kafka-python) (librdkafka) · Apache Kafka 4 (KRaft) · Wazuh · systemd · Docker

Licensed under [Apache-2.0](LICENSE).
