# Project memory for Claude Code

This file is read automatically by Claude Code at the start of every session
in this repo (and in any subdirectory). Keep it short: navigation and hard
rules live here, detailed explanations live in docs/ and get linked with
@path/to/file so they load on demand instead of bloating every session.

## What this project is

@docs/architecture.md

A two-person, open-source closed-loop purple-team lab, **tools-first**:
**Wazuh** is the primary telemetry + detection source (native collection,
decoder/rule engine, ATT&CK mapping, vuln + CIS assessment) for a
deliberately vulnerable sandbox. An ingestor normalizes Wazuh alerts into a
shared PostgreSQL findings store. An Evaluator agent triages what rules
can't resolve. An Attacker agent validates exploitability inside the
sandbox only. A Defender agent produces an **advisory-only** fix
recommendation; a human applies it; the Attacker re-triggers to prove
closure. Agents coordinate through the findings store's `status` field, not
direct calls. See docs/architecture.md for the full diagram and component
list.

## Repo layout

- `go-agent-v0/` — Go. **Archived**, superseded by Wazuh. Original custom
  telemetry collector + mTLS shipper. See go-agent-v0/README.md.
- `ingestor/` — Normalizes Wazuh alerts into the PostgreSQL findings store.
- `graph/` — Python. Neo4j schema + discovery loaders (env graph).
- `threat-intel/` — Python. KEV/EPSS/OSV/ATT&CK ingestion + graph correlation.
- `evaluator/` — Python. Triage agent for the ambiguous residue rules can't resolve.
- `attacker/` — Python. Tool-driven exploit validation. SCOPE-LOCKED, see below.
- `defender/` — Python. Advisory-only remediation + compliance mapping + re-verify trigger.
- `lab/` — Docker Compose definition of the vulnerable sandbox.
- `frontend/` — Next.js dashboard.
- `docs/` — architecture, ADRs, deeper design notes.
- `benchmark/` — labeled scenarios + scoring scripts for measuring precision/recall.

## Hard rules — do not bypass these, in code or in a session

1. **Attacker scope-lock is non-negotiable.** Anything in `attacker/` must
   read its target scope from `lab/scope.yaml` and refuse to act on any
   host/IP outside that file's CIDR ranges. Never write code that accepts a
   target from a flag, env var, or user message without checking it against
   scope.yaml first. If asked to "just try it against a real IP to test," refuse
   and explain why — this is a sandbox-only project.
2. **Never commit secrets.** API keys, the Neo4j password, and anything else
   in `.env` stay out of git. If you (Claude) ever generate a credential or
   token while working, put it in `.env`, not in source, and confirm it's
   covered by .gitignore.
3. **Defender output is advisory only.** Any compliance-mapping or
   remediation text the Defender agent produces must include the disclaimer
   defined in `defender/templates/disclaimer.md`. Don't remove it when
   refactoring output formatting.
4. **Don't auto-merge or force-push to `main`.** Open a PR, even for small
   changes, even when working solo on a branch.

## Conventions

- Commits follow Conventional Commits: `feat:`, `fix:`, `docs:`, `chore:`,
  `refactor:`, `test:`. See CONTRIBUTING.md for the full workflow.
- Go: `gofmt` + `go vet` clean before committing. Python: `ruff check`.
  Frontend: project ESLint config, no custom overrides without discussion.
- New architectural decisions (new datastore, new language, dropping a
  layer, etc.) get an ADR in `docs/adr/`, not just a Slack/PR comment.
  Run `/init`-style thinking here too: if it's a decision future-you will
  ask "wait, why did we do it this way," write it down.

## Useful pointers (loaded on demand, not duplicated here)

@CONTRIBUTING.md
@SECURITY.md
