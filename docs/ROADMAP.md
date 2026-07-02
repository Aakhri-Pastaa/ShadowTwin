# ShadowTwin Roadmap

The master, phase-level plan. For tactical/next-PR items see
[`TODO.md`](TODO.md); for what's actually running right now see
[`PROJECT_STATUS.md`](PROJECT_STATUS.md).

## Phase 1 — Infrastructure

- Wazuh
- Kafka
- ShadowTwin Forwarder

**Status:** Completed.

---

## Phase 2 — Backend

- Kafka consumer
- API layer (ingestor — see [`API.md`](API.md))
- PostgreSQL findings store

**Status:** Pending.

---

## Phase 3 — AI

- Local model serving
- AI evaluator / triage (see [`AI.md`](AI.md))

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
