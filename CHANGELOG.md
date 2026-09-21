# Changelog

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [Semantic Versioning](https://semver.org/).

## [1.1.0] - 2026-09-21

### Fixed
- **Data loss when a log rotated while the forwarder was stopped.** On
  restart v1.0.0 saw a new inode at the path and read that file from byte 0,
  losing everything written to the old one after the last checkpoint —
  silently. It applied to clean stops as well as crashes. Found by the demo
  environment: a `SIGKILL` across one rotation lost 30 alerts. The
  checkpointed file is now found by inode among its rotated generations
  (`<path>.*` plus an optional `rotated:` glob), drained from the saved
  offset, then every later generation in order, then the live file. Verified
  in the demo by a `SIGKILL` held across three rotations: 0 gaps.
- A same-inode resume no longer continues mid-line when the inode has been
  reused by a different file: the saved offset must follow a newline.
- The suite count. v1.0.0's docs said 44 assertions; it executed 42 (the
  number counted `check(` call sites, two of which sit in `try/except`
  branches).

### Added
- `rotated:` config section — per-file glob for rotated generations.
- An `ERROR` naming the inode and lost offset when a rotated file cannot be
  found, instead of an `INFO` line.
- `demo/` — one-command Docker environment: Kafka, a Wazuh-shaped generator
  that rotates and truncates live, the forwarder, and a consumer that audits
  the topic for gaps by sequence number. Plus a Docker-free integration test.
- 17 checks (59 total), including the demo's reproduction as a regression
  test.

### Changed
- Lint configuration moved to a repo-root `ruff.toml` with explicit rule
  selection and `src` roots.

## [1.0.0] - 2026-09-20

Scope frozen at the ingestion layer. What ships is the **ShadowTwin
Forwarder**: a Python service that tails Wazuh's NDJSON alert logs and
streams them to Apache Kafka with at-least-once delivery.

### Added
- `.github/workflows/forwarder.yml` — ruff + the 42-check smoke suite on
  every push. Until now the only component with real code had no CI at all.
- `docs/archive/` — the original closed-loop platform design
  (`original-architecture.md`) and its roadmap, each headed with a note that
  they were never implemented.
- Explicit ruff rule selection in `forwarder/pyproject.toml`. The previous
  config inherited ruff's defaults, which drift between releases and
  reported 11 errors on code that had not changed.

### Changed
- **Rewrote `README.md`** around what the repository actually contains. It
  previously described a closed-loop purple-team platform of which no
  component existed.
- **Rewrote `SECURITY.md`.** It described an offensive "Attacker agent" and
  a `lab/scope.yaml` scope-lock — a safety control that was never
  implemented — and carried a `<maintainer-email-here>` placeholder. It now
  covers the forwarder only, and states plainly that the repository contains
  no offensive tooling.
- Rewrote `docs/CLAUDE.md`: it listed a repo layout of twelve directories,
  nine of which do not exist.
- `docs/PROJECT_STATUS.md` → v1.0.0; progress rows for unbuilt components
  removed rather than left at 0%.
- Retargeted the hero banner and updated `CONTRIBUTING.md` and the PR
  template to match the frozen scope.
- Two ruff autofixes in `forwarder/`: deprecated `typing.Callable` →
  `collections.abc.Callable`, and whitespace. No behaviour change.

### Removed
- `.github/workflows/frontend.yml` and `.github/workflows/python-services.yml`
  — both watched paths that do not exist (`frontend/`, `graph/`,
  `threat-intel/`, `evaluator/`, `attacker/`, `defender/`), so neither had
  ever run.
- `docs/API.md`, `docs/AI.md`, `docs/TODO.md` — placeholders for components
  that were never started.

### Fixed
- Every internal documentation link. A link check now reports zero broken
  targets, including a `docs/images/architecture.png` reference to an image
  that was never committed.

## [Unreleased]

Nothing. The project is scope-frozen.

<details>
<summary>Pre-1.0 history</summary>

### Added
- **`forwarder/`** — the ShadowTwin Forwarder source is now in the monorepo:
  a production-ready Python service that tails Wazuh's NDJSON logs and ships
  them to Kafka, run as the `shadowtwin-forwarder` systemd unit (dedicated
  service user, `/etc` config, health checks, state persistence,
  auto-restart on crash/reboot). Broker addresses/hostnames genericized for
  the public repo.
- `docs/` engineering wiki: `PROJECT_STATUS.md` (living as-built status),
  `ROADMAP.md`, `DECISIONS.md`, `DEPLOYMENT.md`, `AI.md`, `API.md`,
  `TROUBLESHOOTING.md`, `TODO.md`. Plus a local-only, gitignored
  `INFRASTRUCTURE.md` for real hostnames/IPs (never committed).
- `docs/TOPOLOGY.md` — public, genericized infrastructure diagram
  (Mermaid), no real hostnames/IPs/container IDs.
- Repo scaffolding: CI, pre-commit, issue/PR templates, ADR log.

### Changed
- **Visual refresh:** added self-hosted animated SVGs — a hero banner and a
  closed-loop workflow (`docs/assets/`) and a forwarder pipeline
  (`forwarder/assets/`) — embedded in the READMEs in place of the plain
  Mermaid diagrams; colored the `TOPOLOGY.md` Mermaid; added emoji to the
  forwarder README section headers.
- Docs updated for the forwarder milestone: `PROJECT_STATUS.md` (→ v0.2.0,
  ingestion layer production-ready), `ROADMAP.md`, `TODO.md`, `DECISIONS.md`,
  `DEPLOYMENT.md`, `TROUBLESHOOTING.md`, and the repo layout in `docs/CLAUDE.md`.
- **Pivoted architecture to Wazuh-based telemetry/detection**, tools-first
  agents, and an advisory-only (human-in-the-loop) Defender — no
  auto-remediation. Archived the custom Go host agent as `go-agent-v0/`
  (complete, tested, preserved as reference). Updated README,
  `docs/architecture.md`, `docs/CLAUDE.md`, and `DEVLOG.md` to match.
- Split `DEVLOG.md`: the root log now covers only the current project;
  the archived agent's full history moved to `go-agent-v0/DEVLOG.md`.
- Moved `CLAUDE.md` to `docs/CLAUDE.md` (no longer auto-loaded by Claude
  Code — see the note at the top of that file).
- Rewrote the root `README.md` as a modern landing page.

</details>
