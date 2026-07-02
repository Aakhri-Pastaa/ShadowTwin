# Changelog

All notable changes to this project are documented here.
Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
versioning follows [Semantic Versioning](https://semver.org/) once we cut
a first tagged release.

## [Unreleased]

### Changed
- **Pivoted architecture to Wazuh-based telemetry/detection**, tools-first
  agents, and an advisory-only (human-in-the-loop) Defender — no
  auto-remediation. Archived the custom Go host agent as `go-agent-v0/`
  (complete, tested, preserved as reference). Updated README,
  `docs/architecture.md`, `docs/CLAUDE.md`, and `DEVLOG.md` to match.
- Split `DEVLOG.md`: the root log now covers only the current project;
  the archived agent's full history moved to `go-agent-v0/DEVLOG.md`.
- Moved `CLAUDE.md` to `docs/CLAUDE.md` (no longer auto-loaded by Claude
  Code — see the note at the top of that file).

### Added
- Repo scaffolding: CI, pre-commit, issue/PR templates, ADR log.
- `docs/` engineering wiki: `PROJECT_STATUS.md` (living as-built status),
  `ROADMAP.md`, `DECISIONS.md`, `DEPLOYMENT.md`, `AI.md`, `API.md`,
  `TROUBLESHOOTING.md`, `TODO.md`. Plus a local-only, gitignored
  `INFRASTRUCTURE.md` for real hostnames/IPs (never committed).
- `docs/TOPOLOGY.md` — public, genericized infrastructure diagram
  (Mermaid), no real hostnames/IPs/container IDs.
