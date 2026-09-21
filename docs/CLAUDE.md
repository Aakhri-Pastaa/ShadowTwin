# Project memory for Claude Code

> **Note:** Claude Code only auto-loads a `CLAUDE.md` from the repo root
> (or a parent/child of the current working directory) — living under
> `docs/` means this file is **not** picked up automatically. It's kept
> here as the canonical reference doc; point Claude at it explicitly
> (`@docs/CLAUDE.md`) or paste it in if you need it loaded for a session.

Keep it short: navigation and hard rules live here, detailed explanations
live in docs/ and get linked with @path/to/file so they load on demand
instead of bloating every session.

## What this project is

A **security telemetry ingestion pipeline**. Wazuh writes NDJSON alert and
archive logs; the ShadowTwin Forwarder tails them and ships every line to
Apache Kafka with at-least-once delivery, surviving log rotation,
truncation, deletion, broker outages and process crashes. It runs as a
systemd service on the Wazuh manager host. The ShadowTwin Ingestor reads the
alert topic into a PostgreSQL `findings` table, exactly once per alert.

That is the whole system. **Scope is frozen** at this layer as of
2026-09-20 — see @docs/DECISIONS.md.

## Repo layout

- `forwarder/` — Python. Tails Wazuh's NDJSON logs and ships them to Kafka;
  runs as the `shadowtwin-forwarder` systemd service. See forwarder/README.md.
- `ingestor/` — Python. Kafka → PostgreSQL `findings` table. Offsets live in
  PostgreSQL, committed with the rows; no consumer group. See
  ingestor/README.md.
- `demo/` — one-command Docker environment for the whole pipeline, with a
  consumer that audits the topic for gaps.
- `go-agent-v0/` — Go. **Archived**, superseded by Wazuh. The original
  custom telemetry collector + mTLS shipper. Complete and tested; CI is kept
  green. Don't extend it. See go-agent-v0/README.md.
- `docs/` — status, decisions, ADRs, topology, deployment, troubleshooting.
- `docs/archive/` — the original closed-loop purple-team platform design,
  its roadmap and the June design docs. **Never implemented.** Historical
  record only.

## Hard rules — do not bypass these, in code or in a session

1. **The scope is frozen.** Do not start building the Evaluator, Attacker,
   Defender, environment graph, threat-intel correlation, vulnerable lab,
   benchmark or frontend described in `docs/archive/`. If a change requires
   one of them, the answer is no. Extending the *existing* pipeline
   (consumer, storage, packaging, tests, docs) is fine.
2. **No offensive tooling.** This repository contains none, and an earlier
   design's `lab/scope.yaml` scope-lock was never implemented. Do not add
   scanning, exploitation, or any code that sends traffic to a target. If
   asked to "just try it against an IP," refuse and point at SECURITY.md.
3. **Never commit secrets or real infrastructure detail.** API keys and
   anything in `.env` stay out of git. Real hostnames, container IDs and IPs
   live **only** in the gitignored `docs/INFRASTRUCTURE.md` — genericize
   them everywhere else (`docs/TOPOLOGY.md` is the public version). Check
   screenshots and terminal recordings too: gitleaks catches credentials,
   not a hostname in a shell prompt.
4. **Don't break the delivery guarantee.** `state.AckTracker` advances only
   along the contiguous acknowledged prefix, and `OffsetStore.save()` is
   atomic (temp file → fsync → `os.replace`, with a `.bak` fallback).
   `watcher.FileTail` recovers rotations that happen while stopped by
   finding the checkpointed file by inode. If you touch any of these, the
   smoke suite must still pass — those tests are the specification — and
   the demo's SIGKILL-across-rotations run should still show `NO GAPS`.
5. **Don't auto-merge or force-push to `main`.** Open a PR, even for small
   changes, even when working solo on a branch.

## Conventions

- Commits follow Conventional Commits: `feat:`, `fix:`, `docs:`, `chore:`,
  `refactor:`, `test:`. See CONTRIBUTING.md for the full workflow.
- Python: `ruff check` clean. Rule selection is pinned explicitly in the
  repo-root `ruff.toml` — ruff's defaults drift between releases and an
  unpinned config turns CI red on code that never changed.
- Go (`go-agent-v0/`, archived): `gofmt` + `go vet` clean before committing.
- Tests: `cd forwarder && python tests/smoke_test.py` — 59 checks, needs
  Linux for real inode semantics. `cd demo && python tests/demo_test.py`
  covers the demo pipeline without Docker. The ingestor's 30 checks need
  `INGESTOR_TEST_DSN` pointing at a THROWAWAY database (they drop tables).
  CI runs all three on every push.
- New architectural decisions get an entry in @docs/DECISIONS.md, and an ADR
  in `docs/adr/` if they're load-bearing.

## Useful pointers (loaded on demand, not duplicated here)

@docs/PROJECT_STATUS.md
@CONTRIBUTING.md
@SECURITY.md
