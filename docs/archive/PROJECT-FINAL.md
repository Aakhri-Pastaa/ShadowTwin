> # ⚠️ ARCHIVED — never implemented
>
> Written 2026-06-20 while designing the platform, before the scope was frozen
> at the ingestion layer (see [`../DECISIONS.md`](../DECISIONS.md)). Recovered
> on 2026-09-21 from a working copy where it had never been committed, and
> published unchanged apart from removing identifiers of the author's homelab.
>
> It references components that were never built (`lab/scope.yaml`,
> `attacker/`, `defender/`, the benchmark) and two private lab notes that are
> not part of this repository (`DEVLOG-infra.md`, `lab-setup.md`). For what
> the repository actually contains, see the root `README.md`.

---

# ShadowTwin — Finalized Project Definition

**Status:** the locked, scope-disciplined definition of what ShadowTwin *is*,
what it claims, and what it's built from. Supersedes scattered framing across the
other docs; those remain the detailed references. This doc is the **single source
of truth for scope and claims** — its job is to stop scope creep and dishonest
claims, the project's two named risks.

Companion references:
- `docs/architecture-v2.md` — full platform design.
- `docs/agents-tools-first.md` — the tools-first agents + honest-AI identity.
- `docs/evaluator-architecture.md` — the LLM reasoning layer (v2).
- `docs/DEVLOG-infra.md` — the lab build history.

---

## 1. What ShadowTwin is (one honest paragraph)

ShadowTwin is an **AI-assisted, closed-loop, human-in-the-loop purple-team lab**.
Deterministic tools handle the known and mechanical work — detection + ATT&CK
mapping (Wazuh), exploit validation (Caldera/Atomic/scanners), compliance lookup
(published mappings). An **LLM reasoning layer** does only what rules cannot:
triage events no rule anticipated, correlate weak signals into a story, explain
its reasoning, and tailor remediation to context. A finding is triaged, *proven
exploitable with evidence* inside a sandbox, mapped to a compliance-aware fix, and
handed to a **human who applies it** — then the attack is re-run to prove closure.

**It is NOT:** a fully-autonomous pentest platform; a SOC product; a system that
applies fixes itself; or (today) a multi-scenario benchmark. Claiming any of those
would be overclaiming. See §6.

---

## 2. Goals — sequenced, and the honesty rule

**Primary now: career artifact.** A polished, honest, demoable closed-loop on a
small number of *deep* scenarios, with real compliance mapping — aimed at the EU
detection-engineering / cloud-security / GRC market both founders target.

**Secondary, later: recognition artifact.** Expand into a reproducible
multi-scenario **benchmark** — but ONLY after v1 lands.

**The honesty rule (non-negotiable):** *we do not claim benchmark / recognition /
novelty significance until the scenarios that would justify it actually exist.*
Until then, the public framing is "an honest closed-loop demo + write-up," not "a
novel benchmark." Breaking this rule is the fastest way to lose credibility with
exactly the audience we want (employers, the security community).

---

## 3. The scope wedge (what makes it intelligence, not a script)

A single planted-vuln scenario proves *plumbing*, not intelligence — the loop
would be near-tautological ("put SQLi in → scanner finds SQLi → template fixes it
→ rescan confirms"). The fix is **two scenarios for v1**:

1. **One OBVIOUS scenario** — a planted, exploitable vuln (e.g. Juice Shop SQLi).
   Proves the loop works end-to-end. The plumbing baseline.
2. **One NON-OBVIOUS scenario** — where the naive answer is WRONG and the system
   must get it right. The canonical case: a finding Wazuh flags that is **NOT
   actually exploitable in context**, which the Attacker fails to prove and the
   system correctly **downgrades** (a true negative). Alternative: a chained path
   where individually-benign findings only matter *together*.

The non-obvious scenario is the whole point — it's the difference between
"automation that confirms what you planted" and "a system that correctly tells
exploitable from not-exploitable, with proof." **This is the v1 differentiator.**
(Why not several up front: that's recognition-phase labor; doing it before the
loop works contradicts career-first sequencing and re-expands cut scope. Several
scenarios = the benchmark phase, deferred.)

---

## 4. Architecture (locked)

Two planes on one Proxmox box (built; see `DEVLOG-infra.md`):
- **Safe side (vmbr0 + private VPN):** existing homelab services, Wazuh
  (monitoring), orchestrator (the brain). Internet + admin.
- **Caged side (vmbr1, gateless):** the vulnerable lab + the Attacker. No
  internet/LAN/VPN; one outbound path = lab Wazuh agent → manager.

Pipeline (tools-first; LLM is the targeted reasoning layer, not the spine):
```
lab host → Wazuh agent → Wazuh manager (decode + ATT&CK map + correlate + threshold)
   → ingestor → Finding in PostgreSQL(+pgvector ready)
   → Attacker (Caldera+Atomic+nuclei/nmap/sqlmap, scope-locked, PROOF-GATE)
   → Defender (NIST/CIS/ATT&CK lookup + remediation template + risk formula)
   → report bundle → 👤 human applies fix → `verify` re-runs Attacker → pass/fail
   → benchmark scores the run
        ▲ LLM reasoning layer plugs in at: ambiguous triage · weak-signal
          correlation · reasoning/explanations · tailored remediation prose
```

---

## 5. Component selection (FINAL)

| Concern | Component | Build/Reuse | v1? |
|---|---|---|---|
| Telemetry + detection + **ATT&CK mapping** + correlation | **Wazuh 4.14.5** (+ Sigma rules) | reuse | ✅ built |
| Vulnerable lab targets | **OWASP Juice Shop** (obvious) + a **VulHub/config** non-obvious case | reuse | ✅ |
| Alert → Finding bridge | **Wazuh ingestor** | build (thin) | ✅ |
| Findings store | **PostgreSQL** (+ **pgvector** installed, vector search deferred) | reuse | ✅ |
| Adversary emulation / automation | **MITRE Caldera** | reuse | ✅ |
| ATT&CK technique library | **Atomic Red Team** | reuse | ✅ |
| Exploit validation | **nuclei · nmap · sqlmap** | reuse | ✅ |
| Attacker proof artifacts | capture transcripts/canaries (build, thin) | build | ✅ |
| Compliance mapping | **NIST CSF + CIS + ATT&CK mitigations** (published data → lookup tables) | reuse data, build join | ✅ |
| Remediation guidance | **template library** per vuln class | build | ✅ |
| Risk scoring | **formula** (severity × asset-criticality × exploitability) | build | ✅ |
| Reports | renderer from the Finding (+ exec + technical views) | build | ✅ |
| Human gate + re-verify | `verify <id>` re-runs Attacker | build (thin) | ✅ |
| **LLM reasoning layer** | shared local model via **Ollama**, behind a provider interface; CPU on Proxmox; ~4B→8B; occasional calls only | build glue, reuse model | **committed, post-v1-baseline** |
| Benchmark harness | scenarios + scorer (P/R, proof-rate, fix-correctness, verify-pass) | build | secondary phase |

**Deferred (seams kept):** pgvector semantic search; LogAI richer pre-filter;
Neo4j graph (`Neo4jStore`); security-tuned LLMs; the frontend dashboard; threat-
intel CVE-lookup tool (light, near-v1).
**Rejected:** Qdrant (pgvector replaces); Drain3/log parsers (Wazuh decodes);
full enterprise telemetry (no Windows/Sysmon in the cage); training/fine-tuning
(use pre-trained); cloud/Colab runtime (breaks on-prem); "fully autonomous"
framing (it's human-in-the-loop, advisory).

---

## 6. Claims discipline (what we may and may NOT say)

**May say (true today / by v1):**
- "Closed-loop purple-team lab: attack → human-applied fix → re-attack proves it."
- "Tools handle the known; AI handles the ambiguous/novel and the explanation."
- "Evidence-backed validation — no finding is 'confirmed' without proof."
- "Advisory, human-in-the-loop, sandbox-only — never auto-remediates or touches
  production."
- "Compliance-aware remediation (NIST CSF / CIS / ATT&CK)."

**May NOT say until earned:**
- "Novel benchmark" / "state-of-the-art" — not until N diverse, ground-truthed
  scenarios exist.
- "Autonomous" — it isn't; a human applies fixes.
- "Detects unknown threats" beyond what the one non-obvious scenario demonstrates.
- Any precision/recall numbers until measured on real labeled scenarios.

**The proof-gate is what makes the AI claim safe:** the LLM's triage is a cheap
*suggester*; nothing is "confirmed" until a deterministic tool produces proof, and
nothing is fixed until a human acts. So a hallucinated LLM judgment fails closed
(dropped at the proof-gate), not open. This boundary is load-bearing — never trust
LLM output without proof.

---

## 7. Build order (finalized, scope-disciplined)

**Phase 1 — the loop on the OBVIOUS scenario (plumbing baseline, no LLM needed):**
1. Lab VM + Juice Shop in the cage; Wazuh agent reporting; isolation 4-checks pass.
2. Ingestor + PostgreSQL Store; Wazuh's ATT&CK-tagged alerts → Findings.
3. Attacker (Caldera/Atomic/nuclei) — ATT&CK-ID→test lookup, scope-locked,
   proof-gate.
4. Defender — compliance lookup + remediation template + risk formula + disclaimer.
5. Report bundle + `verify`. Human-in-the-loop closure works end-to-end.

**Phase 2 — the NON-OBVIOUS scenario (the differentiator):**
6. Construct the true-negative / chained case; tune the pipeline to get it right.
   This is where the system stops being a script.

**Phase 3 — the committed AI reasoning layer (makes "AI" honest):**
7. Add the shared local LLM for: ambiguous triage, weak-signal correlation,
   reasoning/explanations, tailored remediation prose. (Timeline vs Phase 1/2 — to
   align with the AI-lead partner; must NOT slip to "someday.")

**Phase 4 — recognition (only if Phases 1–3 land):**
8. Expand to several diverse ground-truthed scenarios → the reproducible
   benchmark. Only now may "benchmark/novelty" claims be made.

Get Phase 1 perfect on one scenario before Phase 2; Phase 2 before any breadth.

---

## 8. The skeptic's standing checklist (re-run before any claim or milestone)

- Is this claim earned by work that EXISTS, or by work we intend? (Only the
  former may be said publicly.)
- Did the LLM's output get TRUSTED without a proof-gate anywhere? (Must be no.)
- Are we adding scope before the current phase works end-to-end? (Must be no.)
- Has the AI layer slipped toward "someday"? (If yes, the project is automation,
  not AI — fix or rename.)
- Does the non-obvious scenario still make the loop non-tautological? (If we only
  ever confirm planted vulns, it's a script.)
