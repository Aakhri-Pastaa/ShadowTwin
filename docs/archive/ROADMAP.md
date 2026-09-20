> # ⚠️ ARCHIVED — not a plan
>
> This was the phased plan toward the design in
> [`original-architecture.md`](original-architecture.md). Phase 1
> (infrastructure + forwarder) shipped; **Phases 2–5 were never started and
> are not planned.** Scope was frozen at the ingestion layer on 2026-09-20 —
> see [`../DECISIONS.md`](../DECISIONS.md).
>
> Kept as a record of the original intent, not as future work.

---

# ShadowTwin Roadmap

The master, phase-level plan. For tactical/next-PR items see
`TODO.md` (deleted); for what's actually running right now see
[`../PROJECT_STATUS.md`](../PROJECT_STATUS.md).

## Phase 1 — Infrastructure

- Wazuh
- Kafka
- ShadowTwin Forwarder — production-ready systemd service, source in
  [`forwarder/`](../../forwarder/)

**Status:** ✅ Completed.

---

## Phase 2 — Backend

- Kafka consumer (reads `wazuh-alerts` / `wazuh-logs`)
- API layer (ingestor — `API.md`, deleted)
- PostgreSQL findings store

**Status:** 🔨 In progress — next up.

---

## Phase 3 — AI

- Local model serving
- AI evaluator / triage (`AI.md`, deleted)

**Status:** Pending.

---

## Phase 4 — Dashboard

- Streamlit (see the open Next.js-vs-Streamlit discrepancy noted in
  `PROJECT_STATUS.md`)

**Status:** Pending.

---

## Phase 5 — AI Attacker

- Tool-driven exploit validation (nmap, nuclei, Metasploit, etc.),
  scope-locked to `lab/scope.yaml` per `CLAUDE.md`'s hard rules
- Defender: advisory-only remediation + re-verify loop

**Status:** Pending.
