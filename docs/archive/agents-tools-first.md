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

# ShadowTwin — Tools-First Agent Architecture (LLM optional, deferred to v2)

**Status:** decided direction. Supersedes the LLM-centric framing of the three
agents for **v1**. Companion to `docs/architecture-v2.md`,
`docs/evaluator-architecture.md` (now the *v2 LLM layer* reference), and
`docs/DEVLOG-infra.md`.

## ⚠️ Honest identity: where is the "AI"? (read this — it defines the project)

**Pure tools = automation, NOT AI.** If this project were *only* Wazuh rules +
Caldera + lookup tables + templates, calling it "AI" would be overclaiming — and a
knowledgeable reviewer would see through it instantly. So the identity is
deliberate and honest:

> **The tools handle the KNOWN and mechanical (detection, exploit validation,
> compliance lookup). The AI (an LLM reasoning layer) does what rules
> *fundamentally cannot*: triage novel/ambiguous events no rule anticipated,
> correlate weak signals into a story, EXPLAIN its reasoning, and TAILOR
> remediation to context. The AI is reserved for exactly the work that has no
> deterministic solution — which is the only place "AI" is an honest label.**

This is a **stronger** AI claim than "three autonomous LLM agents," because it's
defensible: rules can't triage what they weren't written for, can't justify their
verdicts, and can't tailor advice to context. The LLM does those, and only those.

**The non-negotiable catch:** for "AI" to be honest, the LLM layer **must actually
exist and do that reasoning** — it cannot be permanent vaporware. "Tools now, AI
someday" is, today, an automation project. The build sequence below is tools-first
**to de-risk**, but the AI reasoning layer is a **committed deliverable, not an
indefinite v2**. (Timeline — right-after-v1 vs in-parallel — to be aligned with
the AI-lead partner.)

## The build decision (and why)

**v1 tooling is built first with NO LLM dependency**, so we get a working,
demoable loop fast on CPU-only infra. The Evaluator, Attacker, and Defender's
*mechanical bulk* is deterministic open-source tools. The **LLM reasoning layer is
then added as the headline differentiator** — it is what makes this AIxSec, not
automation.

**Why this is right for us:**
1. **Infra fit.** The Proxmox box is CPU-only and shared (Wazuh + VMs). Running
   three LLM roles on it was the bottleneck. Tools need ~no inference.
2. **Most of each agent's job was never "reasoning."** It's mechanical — run a
   known technique, match a rule, map a technique, fill a template. Tools do that
   better, faster, and reproducibly.
3. **Credibility.** "We use Wazuh's native ATT&CK mapping + Caldera/Atomic for
   validation + published compliance mappings" is a *grounded, defensible*
   architecture. "Three autonomous LLMs do everything" is the overclaim our own
   project notes warn against. Tools-first is the more respectable story.
4. **De-risks the build.** Get a working, demoable pipeline first; add AI as a
   clean upgrade, not a dependency.

**Key insight that makes this work:** each agent does *mechanical* work and
*reasoning* work. Only the reasoning slice ever needed an LLM — and even that is
small and rare. v1 ships the mechanical bulk; v2 adds the thin reasoning layer.

---

## Per-agent design

### Evaluator — "is this suspicious?"
**v1 (tools only):** the bulk is **already done by Wazuh**, which we just learned:
- **Parsing** → Wazuh decoders already produce structured alerts.
- **MITRE ATT&CK mapping** → **Wazuh already maps each rule to ATT&CK** tactics/
  techniques. No model needed.
- **Correlation** → Wazuh's rules engine (`if_sid`, frequency/aggregation rules)
  combines multiple alerts, requires multiple signals before escalating.
- **Noise reduction (100k→500)** → severity thresholds (e.g. act on level ≥ 12) +
  dedup + known-good allowlists, all rule-based.
- **Sigma rules** → extra detection logic on top, also ATT&CK-tagged.

So the v1 Evaluator = **Wazuh rules + Sigma + thresholds + correlation**, with our
**ingestor** turning the surviving high-severity, ATT&CK-mapped alerts into
Findings in the Store. Scoring is a formula (severity × confidence from rule
match).

**v2 (add LLM):** reserve the LLM for the **ambiguous residue** — events that look
odd but no rule fired ("is this suspicious, and why?"). That's a small fraction,
so even slow CPU inference is fine because it runs rarely.

### Attacker — "is it really exploitable?"  *(100% tool-driven in v1)*
**v1 (tools only):** execution is mechanical — run the known technique tied to the
finding's ATT&CK ID, capture proof, report confirmed/not. Stack:
- **MITRE Caldera** — orchestration/automation of adversary emulation using real
  ATT&CK TTPs; also has built-in blue-team tracking. This is the "autonomous
  attacker" engine, no LLM.
- **Atomic Red Team** — the granular, ATT&CK-mapped **technique library** Caldera
  (and our orchestrator) fires. Precise, repeatable.
- **Nuclei / nmap / sqlmap** — validate web/service exploitability (perfect for
  the Juice Shop target).
- **Finding's ATT&CK ID → which test** = a **lookup table**, not a decision. The
  Evaluator/Wazuh already supplied the technique ID.
- **Scope-lock** to `lab/scope.yaml` (the vmbr1 cage CIDR) stays mandatory.
- **Proof artifact mandatory** to mark `confirmed` (the credibility anchor).

**v2 (add LLM, optional):** only for *choosing/chaining* techniques when the
mapping is ambiguous, or creative multi-step paths. v1 needs none of this.

### Defender — "how to fix + comply?"
**v1 (tools only):** decomposes into deterministic parts:
- **Compliance mapping** (finding → NIST CSF / CIS / ATT&CK mitigation) = a
  **lookup table**. MITRE *publishes* technique→mitigation mappings; CIS/NIST
  cross-maps are reference data. A deterministic join.
- **Remediation steps** = **canned playbooks/templates** keyed by vuln class
  (e.g. SQLi → parameterize queries + example). A template library.
- **Risk scoring** = a **formula** (severity × asset criticality × exploitability).
- **Advisory disclaimer** auto-attached (hard rule #3).

**v2 (add LLM):** only the **writing tasks** — executive-summary prose and
tailoring generic template advice to the specific finding. One LLM call per
report, rare and fine on CPU.

---

## What's tool vs LLM, at a glance

| Agent | v1 = deterministic tools (no LLM) | v2 = thin LLM layer (later) |
|---|---|---|
| Evaluator | Wazuh rules+Sigma: parse, **ATT&CK map**, correlate, dedup, threshold | second opinion on ambiguous, rule-less events |
| Attacker | Caldera + Atomic + nuclei/nmap/sqlmap; ATT&CK-ID→test lookup; proof-gate | choose/chain techniques when ambiguous |
| Defender | mapping tables (NIST/CIS/ATT&CK) + remediation templates + risk formula | exec-summary prose, tailor advice |

---

## v1 pipeline (no LLM anywhere)

```
Lab host → Wazuh agent → Wazuh manager
   (decode + ATT&CK map + correlate + threshold)         [Evaluator bulk, native]
        │  high-severity, ATT&CK-tagged alerts via API
        ▼
   Ingestor → Finding (status=triaged) in PostgreSQL Store
        ▼
   Attacker: ATT&CK ID → Atomic/Caldera/nuclei test (scope-locked)
        │   run in cage → capture PROOF → confirmed? 
        ├─ proof → status=proven
        └─ none  → status=not-reproducible
        ▼
   Defender: lookup compliance (NIST/CIS/ATT&CK) + remediation template + risk formula
        ▼
   Report bundle (rendered from the Finding) → 👤 human applies fix
        ▼
   $ verify <id>  → re-run Attacker test → reverified-pass/fail
        ▼
   Benchmark scores the run
```

Every box above is deterministic. No model inference. Runs on current infra.

---

## v2 enhancement (add the LLM reasoning layer)

Once v1 works end-to-end, add a **single shared local LLM** (Ollama, CPU, behind
the provider interface — see `evaluator-architecture.md` §8.5) used **only** for:
- Evaluator: triaging the ambiguous residue.
- Attacker: optional technique selection/chaining.
- Defender: executive-summary prose + tailoring.
Because these are rare/occasional calls, the slow CPU path is acceptable. The
model is swappable (4B→8B→GPU/OmniBook) via config.

---

## Tool inventory (v1)

| Need | Tool (reuse) | Runs where |
|---|---|---|
| Telemetry + detection + **ATT&CK mapping** + correlation | **Wazuh** (+ Sigma rules) | Wazuh host (done) |
| Adversary emulation / automation | **MITRE Caldera** | orchestrator / cage |
| ATT&CK technique library | **Atomic Red Team** | Attacker runner (cage) |
| Web/service exploit validation | **nuclei · nmap · sqlmap** | Attacker runner (cage) |
| Findings store | **PostgreSQL (+pgvector ready)** | orchestrator VM |
| Compliance mappings | MITRE ATT&CK mitigations, CIS, NIST CSF (published data) | orchestrator VM |
| Remediation playbooks | our template library (we build) | orchestrator VM |
| Risk score | formula (we build) | orchestrator VM |
| Reports | renderer from Finding (we build) | orchestrator VM |

No LLM, no GPU, no cloud required for v1.

---

## Build order (revised, tools-first)

1. **Wazuh detection content** — ensure rules/Sigma produce ATT&CK-tagged,
   correlated, thresholded alerts for the Juice Shop scenario.
2. **Ingestor + PostgreSQL Store** — high-severity alerts → Findings.
3. **Attacker (tools)** — ATT&CK-ID→Atomic/nuclei lookup, scope-locked, proof-gate.
4. **Defender (tables+templates)** — compliance lookup + remediation template +
   risk formula + disclaimer.
5. **Report bundle + `verify`** — human-in-the-loop closure.
6. **Benchmark** — score the tools-only run end-to-end. **This is already a
   complete, demoable, paper-worthy artifact with zero LLM.**
7. **v2:** add the shared LLM reasoning layer where it earns its place.

One golden scenario (Juice Shop) end-to-end, tools-only, before any LLM or breadth.

---

## One-paragraph summary

v1 is **deterministic and LLM-free**: Wazuh (with its native ATT&CK mapping,
correlation, and thresholds) is the Evaluator; **Caldera + Atomic Red Team +
nuclei/nmap/sqlmap** are the Attacker; **published compliance mappings +
remediation templates + a risk formula** are the Defender; a human applies the
fix and a re-run verifies it; a benchmark scores it. It runs entirely on existing
infra with no model inference. The LLM returns in **v2** as a thin, occasional
reasoning + writing layer — added as an upgrade, never a dependency. This is
lighter on the hardware *and* a more credible, grounded architecture.
