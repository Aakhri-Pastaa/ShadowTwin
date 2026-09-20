# Security policy

## Scope of this project

ShadowTwin is a **security telemetry ingestion pipeline**: it tails Wazuh's
NDJSON alert logs and forwards them to Apache Kafka. That is the whole of
the running code, in [`forwarder/`](forwarder/).

An earlier design described an offensive "Attacker" agent that would perform
real exploit attempts against a bundled lab, scope-locked to a
`lab/scope.yaml` file. **That agent was never built, and neither was the
scope-lock.** The design is archived at
[`docs/archive/original-architecture.md`](docs/archive/original-architecture.md)
and the decision to stop is recorded in
[`docs/DECISIONS.md`](docs/DECISIONS.md). This repository contains **no
offensive tooling** and performs no scanning, exploitation, or network
activity against any target.

The only network behaviour in the codebase is an outbound Kafka producer
connection to a broker you configure.

## Reporting a vulnerability

If you find a security issue in the forwarder — the file tailer, the Kafka
producer, the state persistence, the installer, or the systemd unit — please
report it privately rather than opening a public issue:

- Open a [GitHub private security advisory](../../security/advisories/new)
  on this repository.

This is a small open-source portfolio project, not a company with an SLA —
please be patient. Reporters are credited in the fix unless they'd rather
stay anonymous.

## Operational notes for anyone running it

The forwarder is designed to run on the Wazuh manager host, where it needs
read access to `/var/ossec/logs`. A few things are worth knowing before you
deploy it:

- It runs as a dedicated unprivileged system user (`shadowtwin`), added to
  the `wazuh` group for read access. It does not need root at runtime — only
  the installer does.
- Wazuh alert logs contain security-sensitive data (hostnames, usernames,
  file paths, command lines). The forwarder ships them verbatim to Kafka.
  **Secure the broker accordingly** — the shipped configuration uses a
  plaintext connection with no authentication, which is appropriate only on
  a trusted network segment.
- TLS and SASL to Kafka are **not** implemented. See the limitations section
  of the [README](README.md).
- Configuration lives in `/etc/shadowtwin-forwarder/config.yaml` and is not
  overwritten on upgrade.

## Supported versions

Pre-1.0 there were no maintained release branches. From v1.0.0 onward, fixes
land on `main`. This project is scope-frozen: expect maintenance fixes, not
new features.

## No warranty

This is a research and portfolio project. It carries no warranty of any
kind — see [LICENSE](LICENSE).
