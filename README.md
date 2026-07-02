# ShadowTwin

> A closed-loop purple-team lab: detect → advise → (human) fix → re-attack to
> prove the fix worked. Tools-first, AI only where deterministic rules can't
> do the job. Runs entirely against a bundled, deliberately vulnerable
> sandbox — never against production or third-party systems.

[![CI](https://github.com/ORG/REPO/actions/workflows/secret-scan.yml/badge.svg)](../../actions)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

## What this is

Most AI security tooling stops at "found a vulnerability" or "exploited a
vulnerability." This project closes the loop, and does it **tools-first**:
deterministic tools do the mechanical work (collection, detection,
correlation, exploitation, scanning), and AI is reserved only for what rules
can't do — ambiguous triage, cross-signal correlation, and explaining a
finding in plain language.

**Wazuh** is the telemetry and detection source: native multi-platform
collection, its decoder/rule engine, MITRE ATT&CK mapping, vulnerability
detection, and CIS benchmark assessment. An ingestor normalizes Wazuh alerts
into a shared **PostgreSQL findings store**. An Evaluator agent triages
findings that rules alone can't resolve. An Attacker agent validates
exploitability against the bundled sandbox and attaches proof. A Defender
agent produces a **recommended fix — advisory only**. A human reviews and
applies it. The Attacker then re-runs to prove closure. There is **no
auto-remediation**.

Agents don't call each other directly — they coordinate through the shared
findings store and a `status` field on each finding (e.g. `new` →
`triaged` → `exploit_confirmed` → `fix_recommended` → `fix_applied` →
`verified`). See [`docs/architecture.md`](docs/architecture.md) for the full
diagram.

This is a personal/portfolio open-source project built by two people, not
a commercial product. See [SECURITY.md](SECURITY.md) for the responsible-use
policy before running the Attacker component against anything.

## Components

| Component | What it does | Status |
|---|---|---|
| Wazuh | Telemetry + detection: collection, decoder/rule engine, ATT&CK mapping, vuln + CIS assessment | 🚧 deployed |
| `ingestor/` | Normalizes Wazuh alerts into the shared findings store | ⬜ planned |
| PostgreSQL findings store | Shared state; agents coordinate via `status`, not direct calls | ⬜ planned |
| `evaluator/` | Triage for the ambiguous residue rules can't resolve | ⬜ planned |
| `attacker/` | Tool-driven exploit validation + proof, scope-locked to the sandbox | ⬜ planned |
| `defender/` | Advisory-only remediation + compliance mapping; re-verify trigger | ⬜ planned |
| `frontend/` | Dashboard: findings feed, agent reasoning, reports | ⬜ planned |
| [`go-agent-v0/`](go-agent-v0/) | Original custom Go telemetry agent | ⏸ archived — superseded by Wazuh |

## Documentation

This project keeps a small internal engineering wiki under `docs/`,
updated as the project evolves:

- [`docs/PROJECT_STATUS.md`](docs/PROJECT_STATUS.md) — living doc, what's
  actually running right now
- [`docs/ROADMAP.md`](docs/ROADMAP.md) — phased forward plan
- [`docs/architecture.md`](docs/architecture.md) — target architecture
- [`docs/DECISIONS.md`](docs/DECISIONS.md) — lightweight running decision
  log (see also the formal [`docs/adr/`](docs/adr/) records)
- [`docs/DEPLOYMENT.md`](docs/DEPLOYMENT.md), [`docs/AI.md`](docs/AI.md),
  [`docs/API.md`](docs/API.md), [`docs/TROUBLESHOOTING.md`](docs/TROUBLESHOOTING.md),
  [`docs/TODO.md`](docs/TODO.md)
- [`DEVLOG.md`](DEVLOG.md) — narrative build history
- [`CHANGELOG.md`](CHANGELOG.md) — notable changes

## Quickstart

> TODO — fill in once `lab/docker-compose.yml`, the Wazuh deployment, and
> `ingestor/` exist.
> Target shape:
> ```
> git clone <repo-url> && cd <repo-name>
> cp .env.example .env   # fill in values
> docker compose -f lab/docker-compose.yml up -d   # sandbox + Wazuh + Postgres
> cd ingestor && <run the ingestor>
> ```

## Why open source

We're building this in the open deliberately — see the responsible-use
note in SECURITY.md, and see CONTRIBUTING.md if you'd like to contribute.

## License

Apache-2.0 — see [LICENSE](LICENSE).
