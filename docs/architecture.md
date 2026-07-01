# Architecture

> **Pivoted 2026** — this doc describes the current, Wazuh-based design. The
> project's first design used a custom Go host agent as the telemetry
> source; that agent is archived at [`go-agent-v0/`](../go-agent-v0/) and
> its rationale is preserved in `docs/adr/0002-custom-host-agent-not-wazuh-fork.md`.
> See `DEVLOG.md` for the pivot history.

One-paragraph version: **Wazuh** ships telemetry and detections (native
collection, decoder/rule engine, ATT&CK mapping, vulnerability detection,
CIS assessment) from the bundled vulnerable lab. An ingestor normalizes
Wazuh alerts into a shared **PostgreSQL findings store**. This is
**tools-first**: deterministic tools and Wazuh's rule engine do the
mechanical work, and AI is reserved for what rules can't do. An Evaluator
agent triages the ambiguous residue against the environment graph and
threat intel. High-confidence findings pass a human review gate, then an
Attacker agent validates exploitability inside the lab using real tools
(nmap, sqlmap, Metasploit, etc.) orchestrated by an LLM, and attaches proof.
A Defender agent produces a remediation recommendation — **advisory only**.
A human applies the fix; the Attacker then re-triggers against the same
finding to prove closure. There is **no auto-remediation**. Agents don't
call each other directly — they coordinate through the findings store's
`status` field. A frontend renders the graph, the live agent reasoning, and
reports.

![architecture diagram](images/architecture.png)
<!-- Drop the diagram image here, or re-export it from the conversation
     where it was designed and keep this path in sync. -->

## Layers

1. **Wazuh** — telemetry + detection source. Native multi-platform
   collection, decoder/rule engine, MITRE ATT&CK mapping, vulnerability
   detection, CIS benchmark assessment. Replaces the archived
   [`go-agent-v0/`](../go-agent-v0/) custom collector: "reuse the mature
   tool, build only the differentiating glue."
2. **Ingestor** (`ingestor/`) — normalizes Wazuh alerts into the shared
   PostgreSQL findings store (the coordination point between agents).
3. **Environment mapper** (`graph/`) — Neo4j. Asset/identity/trust/attack
   graph, built from nmap, SharpHound, and cloud-API loaders.
4. **Threat intel** (`threat-intel/`) — KEV + EPSS + OSV + ATT&CK/CWE/CAPEC,
   correlated onto the graph as `AFFECTED_BY` / `MAPS_TO` edges.
5. **Evaluator** (`evaluator/`) — triage for findings rules alone can't
   resolve, using graph + intel context. Outputs confidence/severity/
   business-impact score.
6. **Attacker** (`attacker/`) — tool-driven exploit validation (not raw LLM
   exploitation), scope-locked to `lab/scope.yaml`, attaches proof. See
   SECURITY.md.
7. **Defender** (`defender/`) — root cause, remediation, compliance mapping
   (NIST CSF / CIS / ATT&CK to start) — **advisory only**. A human applies
   the fix in the lab; the Defender then re-triggers the Attacker to verify
   closure.

## Decisions

Why each non-obvious choice was made lives in `docs/adr/`, not here — this
file is a map, not the territory. Start at `docs/adr/0001-record-architecture-decisions.md`.
