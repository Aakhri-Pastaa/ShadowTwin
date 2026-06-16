# Architecture

One-paragraph version: a host agent ships telemetry from a bundled
vulnerable lab into a graph-backed platform. An Evaluator agent (cheap ML
filter, then LLM) triages events against the environment graph and threat
intel. High-confidence findings pass a human review gate, then an Attacker
agent validates exploitability inside the lab using real tools (nmap,
sqlmap, Metasploit, etc.) orchestrated by an LLM. A Defender agent
generates and applies a remediation in the lab, then re-triggers the
Attacker against the same finding to prove the fix worked. A frontend
renders the graph, the live agent reasoning, and reports.

![architecture diagram](images/architecture.png)
<!-- Drop the diagram image here, or re-export it from the conversation
     where it was designed and keep this path in sync. -->

## Layers

1. **Host agent** (`host-agent/`) — Go. osquery + auditd/Sysmon collectors,
   local buffer, mTLS shipping, single-binary install.
2. **Environment mapper** (`graph/`) — Neo4j. Asset/identity/trust/attack
   graph, built from nmap, SharpHound, and cloud-API loaders.
3. **Threat intel** (`threat-intel/`) — KEV + EPSS + OSV + ATT&CK/CWE/CAPEC,
   correlated onto the graph as `AFFECTED_BY` / `MAPS_TO` edges.
4. **Evaluator** (`evaluator/`) — ML pre-filter, then LLM triage with
   graph + intel context. Outputs confidence/severity/business-impact score.
5. **Attacker** (`attacker/`) — LLM-orchestrated tool use (not raw LLM
   exploitation), scope-locked to `lab/scope.yaml`. See SECURITY.md.
6. **Defender** (`defender/`) — root cause, remediation, advisory-only
   compliance mapping (NIST CSF / CIS / ATT&CK to start), applies the fix
   in the lab, re-triggers the Attacker to verify closure.

## Decisions

Why each non-obvious choice was made lives in `docs/adr/`, not here — this
file is a map, not the territory. Start at `docs/adr/0001-record-architecture-decisions.md`.
