# project-name-here

> A closed-loop, AI-orchestrated purple-team lab: attack → fix → re-attack to
> prove the fix worked. Runs entirely against a bundled, deliberately
> vulnerable sandbox — never against production or third-party systems.

[![CI](https://github.com/ORG/REPO/actions/workflows/secret-scan.yml/badge.svg)](../../actions)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## What this is

Most AI security tooling stops at "found a vulnerability" or "exploited a
vulnerability." This project closes the loop: an Evaluator agent triages
telemetry, an Attacker agent validates exploitability against a sandboxed
lab, and a Defender agent generates and applies a fix — then re-triggers
the same attack to prove the fix actually closed the hole. See
[`docs/architecture.md`](docs/architecture.md) for the full diagram.

This is a personal/portfolio open-source project built by two people, not
a commercial product. See [SECURITY.md](SECURITY.md) for the responsible-use
policy before running the Attacker component against anything.

## Components

| Component | What it does | Status |
|---|---|---|
| `host-agent/` | Telemetry collector, mTLS shipper | 🚧 in progress |
| `graph/` | Environment/attack graph (Neo4j) | ⬜ planned |
| `threat-intel/` | CVE/KEV/EPSS/ATT&CK ingestion | ⬜ planned |
| `evaluator/` | ML pre-filter + LLM triage | ⬜ planned |
| `attacker/` | LLM-orchestrated exploit validation | ⬜ planned |
| `defender/` | Remediation + compliance mapping + re-verify | ⬜ planned |
| `frontend/` | Dashboard: graph view, agent reasoning feed, reports | ⬜ planned |

## Quickstart

> TODO — fill in once `lab/docker-compose.yml` and `host-agent/` exist.
> Target shape:
> ```
> git clone <repo-url> && cd <repo-name>
> cp .env.example .env   # fill in values
> docker compose -f lab/docker-compose.yml up -d
> cd host-agent && go run ./cmd/agent
> ```

## Why open source

We're building this in the open deliberately — see the responsible-use
note in SECURITY.md, and see CONTRIBUTING.md if you'd like to contribute.

## License

Apache-2.0 — see [LICENSE](LICENSE).
