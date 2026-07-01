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
  `docs/architecture.md`, `CLAUDE.md`, and `DEVLOG.md` to match.

### Added
- Repo scaffolding: CI, pre-commit, issue/PR templates, ADR log.
