# Changelog

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [Semantic Versioning](https://semver.org/) once we cut
a first tagged release.

## [Unreleased]

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
