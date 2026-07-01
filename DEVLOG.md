# ShadowTwin — Development Log

A running, human-readable record of how this project was built: what we did, why
we did it, how it works, and how each step was verified. It exists so either of
us (or a future contributor) can **backtrack in detail** without reconstructing
intent from diffs and chat logs.

This complements, but does not replace:

- **`docs/adr/`** — the formal *decision* records (why a non-obvious choice was
  made). The devlog links to them rather than repeating them.
- **Component READMEs** — how to build/run/test a component as it stands *now*.
  The devlog is the *history*; the README is the *current state*.
- **[`go-agent-v0/DEVLOG.md`](go-agent-v0/DEVLOG.md)** — the full history of
  the archived Go telemetry agent, the project's first design. This file only
  covers the current, Wazuh-based project going forward.

## How this log is maintained

- One dated entry per meaningful chunk of work (typically one merged PR).
- Each entry follows the same shape: **What · Why · How · Verification · Refs**.
- The **Current state** section near the top is always kept accurate; the
  **Timeline** below is append-only narrative, oldest first.
- Dates are absolute (`YYYY-MM-DD`).

---

## Project at a glance

**ShadowTwin** is a two-person, open-source, closed-loop purple-team lab,
**tools-first**: **Wazuh** is the primary telemetry + detection source for a
deliberately vulnerable sandbox. An ingestor normalizes Wazuh alerts into a
shared PostgreSQL findings store. An Evaluator agent triages what rules
can't resolve. An Attacker agent validates exploitability inside the
sandbox only and attaches proof. A Defender agent produces an
**advisory-only** fix recommendation; a human applies it; the Attacker
re-triggers to prove closure. There is **no auto-remediation**. See
`docs/architecture.md` for the full picture.

**Where we started:** the project's first design used a custom Go host
agent as the telemetry source (see [`go-agent-v0/DEVLOG.md`](go-agent-v0/DEVLOG.md)
for that history in full). That agent is complete and tested, and is now
archived at [`go-agent-v0/`](go-agent-v0/) as reference and as the
project's starting point.

**Where we are now:** the project has pivoted to a Wazuh-based architecture.
Wazuh is deployed as the telemetry/detection source; the docs (README,
`docs/architecture.md`, `CLAUDE.md`) describe the target design. The
ingestor, PostgreSQL findings store, Evaluator, Attacker, Defender, and
frontend are not built yet.

---

## Current state — what works today

| Area | Status | Notes |
|---|---|---|
| Wazuh deployment | 🚧 | Deployed for the lab; native collection + rule engine + ATT&CK/CIS/vuln modules |
| `ingestor/` (Wazuh alerts → findings store) | ⬜ | Not started |
| PostgreSQL findings store | ⬜ | Not started |
| `evaluator/` | ⬜ | Not started |
| `attacker/` | ⬜ | Not started |
| `defender/` | ⬜ | Not started |
| `frontend/` | ⬜ | Not started |
| `go-agent-v0/` | ⏸ | Archived — superseded by Wazuh, see its own devlog |

---

## Timeline

### 2026-07-02 — Pivot to Wazuh-based architecture (PR #7)

**What.** Replaced the custom Go host agent as the project's telemetry
source with **Wazuh**, and re-pointed the docs at the new design: Wazuh →
ingestor → PostgreSQL findings store → Evaluator → Attacker+proof →
Defender (advisory only) → human → re-verify. Archived the Go agent as
`go-agent-v0/` (renamed from `host-agent/`, unchanged code, archive note
added) with its own devlog splitting off the prior history. Updated
`README.md`, `docs/architecture.md`, `CLAUDE.md`, `CHANGELOG.md`, plus the
CI workflow, pre-commit hook, `.gitignore`, and issue template so paths
match the renamed directory.

**Why.** Wazuh already provides mature multi-platform collection, a
decoder/rule engine, MITRE ATT&CK mapping, vulnerability detection, and CIS
assessment — reusing it instead of maintaining a custom collector lets the
project focus effort on the differentiating agents (Evaluator, Attacker,
Defender) instead of re-solving telemetry shipping. It also motivated the
broader **tools-first** principle: deterministic tools (Wazuh's rule engine
included) do the mechanical work, and AI is reserved for the ambiguous
residue rules can't resolve. The Defender was locked to **advisory-only**
(human applies the fix, Attacker re-verifies) rather than auto-applying
remediations, for the same reason a scope-locked Attacker exists — this is
a sandbox research project, not an autonomous-remediation product.

**How.** Docs-only change; no agent code touched. See PR #7 for the full
diff.

**Verification.** Manual review against the scrub checklist (no IPs,
hostnames, personal domain, or secrets in the diff); cross-checked README,
`docs/architecture.md`, and `CLAUDE.md` for consistency so no doc still
described the old Go-agent-as-primary design.

**Refs.** PR #7.

## Upcoming / backlog

Rough priority order for the current, Wazuh-based direction:

1. **Wazuh → ingestor** — normalize Wazuh alerts into the PostgreSQL
   findings store; define the `status` field lifecycle agents coordinate on.
2. **Findings store schema** — the shared coordination point between
   Evaluator, Attacker, and Defender.
3. **Evaluator** — triage for findings Wazuh's rules can't resolve on their
   own (ambiguous severity/business impact, cross-signal correlation).
4. **Attacker** — tool-driven exploit validation against `lab/scope.yaml`,
   attaching proof to a finding.
5. **Defender** — advisory-only remediation + compliance mapping, plus the
   re-verify trigger back to the Attacker.
6. **Frontend** — findings feed, agent reasoning, reports.

## Decision index (ADRs)

- ADR-0001 — Record architecture decisions.
- ADR-0002 — Build a custom Go host agent instead of forking Wazuh
  (superseded by the pivot above; see [`go-agent-v0/DEVLOG.md`](go-agent-v0/DEVLOG.md)).
