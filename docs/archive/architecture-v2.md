> # ⚠️ ARCHIVED — never implemented
>
> Written 2026-06-18 while designing the platform, before the scope was frozen
> at the ingestion layer (see [`../DECISIONS.md`](../DECISIONS.md)). Recovered
> on 2026-09-21 from a working copy where it had never been committed, and
> published unchanged apart from removing identifiers of the author's homelab.
>
> It references components that were never built (`lab/scope.yaml`,
> `attacker/`, `defender/`, the benchmark) and two private lab notes that are
> not part of this repository (`DEVLOG-infra.md`, `lab-setup.md`). For what
> the repository actually contains, see the root `README.md`.

---

# ShadowTwin — Architecture v2 (merged, human-in-the-loop, advisory)

> Status: design spec. Supersedes the high-level sketch in `architecture.md`
> for the agent layer. The host-agent reality (Go, mTLS, buffer, enrollment)
> is unchanged and feeds straight into this.

This document is the buildable blueprint: every component, whether we build
it or reuse an open-source thing, and exactly how the pieces connect. It is
written **what → why → how** throughout, and it keeps the architecture *you*
designed: a staged pipeline where each agent produces a detailed report, the
Attacker proves exploitability with evidence, the Defender *recommends* (never
applies) a compliance-mapped fix with business impact, and a **human** applies
the fix and triggers re-verification.

---

## 0. Design principles (the rules every component obeys)

1. **Human closes the loop.** No agent ever applies a fix to anything. The
   system produces evidence and recommendations; a human acts. Re-verification
   is a one-command re-run of the Attacker, triggered by the human.
2. **Advisory-only Defender.** Every Defender output carries the disclaimer in
   `defender/templates/disclaimer.md` (CLAUDE.md hard rule #3).
3. **No proof, no claim.** A vulnerability is only ever reported as *confirmed*
   if the Attacker produced a concrete proof artifact. Otherwise it is
   `suspected` or `not-reproducible`.
4. **Scope-lock is absolute.** Anything in `attacker/` reads targets from
   `lab/scope.yaml` and refuses anything outside it (CLAUDE.md hard rule #1).
5. **One finding object, many views.** All agents read/write a single
   structured **Finding**. Reports are *renders* of it. The benchmark reads the
   same objects. There is exactly one source of truth.
6. **Storage is swappable.** The graph/store sits behind an interface
   (`Store`). v1 = SQLite. Upgrade to Neo4j later **without touching agents**.
   This is the key to keeping the graph optional and upgradable at any stage.
7. **Reuse > rebuild.** We build orchestration, the Finding model, prompts +
   reasoning capture, proof-gating, compliance mapping, and the benchmark. We
   reuse everything else (scanners, exploit tools, LLM runtime, intel feeds,
   the vulnerable apps).

---

## 1. The data backbone — the Finding object and the Store

Everything else plugs into this. Build the backbone first.

### 1.1 The Finding object  *(WE BUILD)*

**What.** One JSON/dataclass that accretes data as it flows through the stages.
It is the unit of work and the unit of evidence.

```
Finding
  finding_id          uuid
  run_id              uuid (groups findings from one pipeline run)
  status              enum: new | triaged | proven | not-reproducible |
                            remediation-proposed | awaiting-human |
                            applied | reverified-pass | reverified-fail
  asset               { host, service, port, scenario_id }
  source_event        raw telemetry / scanner hit that started it

  evaluator   { severity, confidence, business_context, rationale,
                attack_path?, model, prompt_hash, reasoning_trace }
  attacker    { attack_status: confirmed|suspected|failed,
                attck_technique, tool, command_log, proof_artifacts[],
                repro_steps, model, reasoning_trace }
  defender    { root_cause, recommended_fix_steps,
                framework_mappings: { nist_csf[], cis_controls[], attck_mitigations[] },
                business_impact: { cia, asset_criticality, blast_radius? },
                residual_risk, disclaimer, model, reasoning_trace }
  verification{ status, retested_at, retest_command, attacker_rerun_ref }

  timestamps  { created, triaged, proven, proposed, reverified }
```

**Why.** It makes the per-stage reports trivial (render a slice), makes the
benchmark trivial (read the field you score), and makes every claim traceable
to evidence — which is exactly what reviewers and employers respect. It also
forces honesty: `attack_status` and `status` can't be faked past the gate.

**How.** A `shadowtwin/core/finding.py` Pydantic model (Pydantic also gives you
the JSON schema for free, and you already use Pydantic verdicts in your other
project). Stages only ever *append* to their own sub-object; they never rewrite
another stage's data.

### 1.2 The Store interface  *(WE BUILD the interface; REUSE the engines)*

**What.** A thin `Store` interface with the operations agents need:
`create(finding)`, `get(id)`, `update(id, patch)`, `find_by_status(status)`,
and graph-flavoured reads `neighbors(asset)`, `attack_paths(asset)`.

**Why.** This is the single decision that makes the graph *optional and
upgradable at any stage*. Agents call `store.attack_paths(asset)` and don't
care whether that's a SQL recursive query or a Neo4j Cypher traversal. You can
ship v1 with no graph, add a lite graph, or go full Neo4j — agents never change.

**How — three interchangeable backends behind the same interface:**

| Backend | Engine (reuse) | When | Graph reasoning |
|---|---|---|---|
| `SqliteStore` | SQLite (stdlib) | **v1, now** | assets + edges as tables; `attack_paths` = recursive CTE (shallow) |
| `LiteGraphStore` | SQLite + edge table, or DuckDB | interim | richer relational edges, still file-based |
| `Neo4jStore` | **Neo4j Community** (Docker) | upgrade | full Cypher traversal, real attack paths, intel edges |

Start with `SqliteStore`. The day you want real attack-path reasoning, you
write `Neo4jStore`, point a config flag at it, and nothing upstream moves.

---

## 2. Telemetry in — the host agent + the lab  *(host agent: BUILT; lab: REUSE)*

### 2.1 Host agent  *(WE BUILT — keep)*
**What/why/how.** Already done: Go single binary, journald auth collector,
durable buffer, mTLS shipping, enrollment, renewal/revocation, one-command
install. It is the trusted, authenticated source of telemetry. It stays exactly
as is; v2 only adds *what consumes its events* (the Evaluator, via the Store).

> Upgrade later: a second collector (osquery or auditd) to prove the
> `Collector` contract generalizes. Not on the v1 critical path.

### 2.2 The vulnerable lab  *(REUSE — do not build vulnerable apps)*
**What.** A `lab/docker-compose.yml` that stands up known-vulnerable targets,
plus `lab/scope.yaml` declaring the only IP/CIDR ranges the Attacker may touch.

**Why.** Building vulnerable apps is wasted effort and less credible than
community-vetted ones. Reusing labelled labs is also what makes a *benchmark*
possible — they come with known ground truth.

**How — reuse these:**
- **OWASP Juice Shop** — single container, rich web-vuln surface, great demo.
- **VulHub** — per-CVE Docker environments; each maps to a real CVE = perfect
  benchmark scenarios with ground truth.
- **DVWA / WebGoat** — classic, adjustable difficulty.

Each lab target becomes a **scenario** with a `scenario_id`, the planted
vuln(s), and (for the benchmark) the ground-truth label.

---

## 2.3 Collection upgrade — Wazuh + LogAI (the pre-Evaluator layer)

Decision: **Wazuh becomes the primary telemetry + detection source; the Go host
agent is parked as a later upgrade; LogAI is the anomaly/dedup pre-filter.**
Crucially, *nothing from the Evaluator onward changes* — this is all upstream of
the Store seam.

### Wazuh  *(REUSE — open-source XDR/SIEM)*
**What.** Wazuh agents run on each lab host (log collection, Syscollector
inventory, FIM, SCA); a Wazuh Manager decodes + rule-matches and runs modules
for **Vulnerability Detection**, **Security Configuration Assessment (CIS)**, and
**File Integrity Monitoring**. Alerts are read via the Wazuh API / indexer.

**Why.** One agent replaces ad-hoc collection *and* adds detections we'd never
hand-build. Three modules map straight onto our needs:
- **Vulnerability Detection** → CVE-based **candidate findings** (alongside/instead of nuclei).
- **SCA (CIS benchmarks + remediation text)** → a direct **head-start for the Defender's CIS mapping**.
- **FIM** → ready-made **proof material for the Attacker** (planted-file / tamper canary).

**Deliberate non-use:** **Active Response stays OFF.** Auto-blocking/quarantine
would violate the human-applies-the-fix principle (§0.1).

**Go host agent (BUILT):** parked. Kept as documented engineering prior art;
revisit later as a lightweight custom mTLS collector for cases Wazuh doesn't
cover. Not on the v1 critical path.

### Wazuh ingestor  *(WE BUILD — the one new component)*
**What.** A small worker that pulls relevant Wazuh alerts and converts each into
a **Finding** (`status=new`), normalized to the one schema, written via `Store`.
**Why.** It is the only new glue needed; it bridges Wazuh's output to our
pipeline. **How.** Python worker → Wazuh API → map alert fields → Finding →
`store.create()`. nuclei/nmap may still inject extra candidates on demand.

### LogAI  *(REUSE — Salesforce open-source library)*
**What.** Log **parsing + clustering + anomaly detection** (OpenTelemetry data
model). Sits between the ingestor and the Evaluator. **Why.** Wazuh emits volume;
sending every alert to the LLM is slow/noisy. LogAI **dedups** (clusters similar
findings) and **anomaly-scores** so only unusual, non-duplicate candidates reach
the Evaluator — this fills the "cheap ML pre-filter before the LLM" slot with an
open-source library instead of custom code. Complementary to Wazuh: Wazuh =
known-signature filtering, LogAI = unknown/anomalous + volume collapse.
**How.** Call LogAI on the batch of `new` findings; attach an anomaly score the
Evaluator uses to prioritize.

### Revised pre-Evaluator flow
```
Wazuh agents (lab hosts)
   logs · Syscollector · FIM · SCA
        │
        ▼
Wazuh Manager  — decoders + rules + Vuln Detection + SCA + FIM   (Active Response OFF)
        │  alerts via API / indexer
        ▼
Wazuh ingestor (WE BUILD)  — alert → Finding (status=new)
        │            ▲ nuclei/nmap optional extra candidates
        ▼
LogAI pre-filter  — cluster/dedup + anomaly score
        ▼
[ Store ]  status=new  →  EVALUATOR (unchanged from here on)
```

Free wins: SCA→Defender (CIS), FIM→Attacker (proof), Vuln Detection→candidates.

---

## 3. The agent pipeline — staged, each emits a report

All three agents are LLM-orchestrated. They share one **LLM provider
interface** (§5) so the model is swappable and the benchmark can compare models.

### 3.1 Evaluator  *(WE BUILD orchestration; REUSE the LLM + a scanner)*

**What.** Consumes telemetry + scanner output, triages each candidate into a
Finding with severity, confidence, business context and a rationale. Writes
`status = triaged`. Can **suppress/merge** noise so reports don't drown.

**Why.** This is the cheap filter that decides what's worth the Attacker's
(expensive) time. The suppression/dedup path is what keeps the system usable.

**How.**
- Reuse a scanner to generate candidates: **nuclei** (templated vuln checks) and
  **nmap** (service/version discovery). The Evaluator reads their output.
- Pull context from the Store: asset info and, if a graph backend is active,
  `attack_path` hints (does this asset lead anywhere valuable?).
- LLM call via the provider interface → structured verdict (Pydantic) →
  `finding.evaluator`. Capture the prompt hash + reasoning trace.
- Optional cheap pre-filter (an ML/heuristic stage) can come later; v1 can be
  scanner-hit + LLM triage and still be honest.

### 3.2 Attacker  *(WE BUILD orchestration + proof-gating; REUSE tools)*

**What.** Takes `triaged` findings and **proves exploitability inside the lab**,
producing concrete evidence. Writes `attack_status` and, if confirmed,
`status = proven`. No proof → `not-reproducible`, and it does **not** get
promoted.

**Why.** This is your differentiator and your credibility anchor: not "an LLM
thinks it's vulnerable" but "here is the captured proof that it is." The
proof-gate is the single rule that kills most false positives.

**How (matches your chosen depth — scanner-proof + scripted PoCs):**
- **Scope check first**, always, against `lab/scope.yaml`. Refuse otherwise.
- LLM **selects and parameterises** tools/PoCs; it does not freehand exploits:
  - **nmap / nuclei** for confirmation,
  - **sqlmap** for injection classes,
  - **curated per-scenario PoC scripts** (`attacker/pocs/<scenario>.py`) the LLM
    chooses from. This keeps exploitation real, reproducible, controllable, safe.
- **Proof artifact is mandatory** to mark `confirmed`: captured tool transcript,
  a planted canary file retrieved, a returned token/loot, an HTTP response
  proving the condition. Stored under `findings/<run>/<id>/proof/`.
- Tag the **ATT&CK technique** used (feeds the Defender's mapping + benchmark).
- Capture the full command log + reasoning trace for the report and the paper.

> Upgrade later (your option B, deferred): LLM-driven Metasploit modules for
> deeper exploitation. Not v1 — heavier, more failure modes, more scope risk.

### 3.3 Defender  *(WE BUILD mapping + impact; REUSE framework data + LLM)*

**What.** Takes `proven` findings, produces a root-cause, a **recommended** fix
(exact steps), a compliance mapping, and a business-impact assessment. Writes
`status = remediation-proposed → awaiting-human`. Applies **nothing**.

**Why.** This is the second differentiator and the part the 39+ crowded
AI-pentest repos skip: translating an exploit into "here's the control it
violates, here's the business impact, here's how to fix it." It's also the
bridge to your MSc/GRC interests.

**How (matches your chosen frameworks — NIST CSF + CIS + ATT&CK mitigations):**
- **Mapping tables you curate** (`defender/mappings/`): ATT&CK technique →
  ATT&CK mitigation, → NIST CSF subcategory, → CIS Control/Safeguard. Reuse the
  public framework data (MITRE ATT&CK STIX, NIST CSF, CIS Controls v8) — don't
  invent it; structure it.
- **Business impact** is structured, not prose: CIA effect + asset criticality
  (from the Store; richer if a graph backend supplies blast-radius) →
  `finding.defender.business_impact`.
- LLM drafts root-cause + remediation steps; the mapping tables ground the
  compliance fields so they're deterministic, not hallucinated.
- **Disclaimer auto-attached** (hard rule #3). Output → report bundle.

---

## 4. Human handoff + verification  *(WE BUILD the render + the verify command)*

### 4.1 The report bundle  *(WE BUILD)*
**What.** One timestamped folder per run is the deliverable a human picks up:
```
findings/<run_id>/
  SUMMARY.md                # run overview: counts, severities, what needs action
  <finding_id>/
    finding.json            # the full structured object (machine-readable)
    evaluator.md            # triage + rationale
    attacker.md             # proof, repro steps, command log
    defender.md             # fix steps, NIST CSF / CIS / ATT&CK mapping, impact, disclaimer
    proof/                  # screenshots, transcripts, retrieved canaries
```
**Why.** Three loose text blobs aren't a deliverable; one bundle derived from
one Finding is. It's also exactly the package that becomes a portfolio artifact
and a benchmark record.
**How.** A renderer walks the Finding and emits Markdown; optional PDF via the
`pdf` skill. `SUMMARY.md` lists everything in `awaiting-human`.

### 4.2 The human gate  *(state, not a step)*
**What/why.** "Hand to human" = the system parks findings in `awaiting-human`.
The human reads the bundle and applies the fix **themselves** (your design,
correct for security). Nothing in the system writes to a real system.

### 4.3 Re-verification  *(WE BUILD a thin command; REUSE the Attacker)*
**What.** `shadowtwin verify <finding_id>` re-runs **only** the Attacker step
against that one finding after the human applied the fix.
**Why.** This is what keeps your loop *demonstrably closed* even though a human
applies the fix. "Re-attack proves the fix" survives — the human just owns the
apply step. It's also the human's "did my fix actually work?" button.
**How.** Loads the finding, re-invokes the Attacker against the same scenario,
writes `verification.status = reverified-pass | reverified-fail` and updates
`status`. A pass is the headline result; a fail sends it back to `awaiting-human`.

---

## 5. The LLM layer — provider interface + security-model options  *(WE BUILD the interface; REUSE models)*

**What.** A thin `LLMProvider` interface (`complete(prompt, schema) -> object`)
that every agent calls. Concrete providers wrap Ollama, Groq, or a security
framework backend.

**Why.** (a) Swap models without touching agents. (b) The benchmark's whole
point is "run different model sets against the same scenarios" — that needs this
seam. (c) Lets you trade a strong hosted model for a private local one freely.

**How — the open-source options to evaluate (research-backed, June 2026):**

*General local (good default, free, private, reproducible):*
- **Qwen2.5-Coder-7B / Qwen3-8B** — Apache-2.0, ~8GB VRAM, strong reasoning at
  small size; practical first choice on the OmniBook via Ollama.
- **DeepSeek-R1 (distills)** — top-tier *reasoning* for attack-chain / triage
  logic; heavier, use where depth matters.

*Security-specialised (evaluate for Attacker/Defender depth):*
- **DeepHat (formerly WhiteRabbitNeo)** — open model purpose-built for security
  use; uncensored for offensive/defensive tasks.
- **xOffense (Qwen3-32B fine-tune)** — reported ~79% sub-task completion on
  pentest tasks, beating GPT-4/Llama-3 baselines; strong Attacker candidate.

*Framework to reuse instead of building the agent runtime:*
- **CAI (Cybersecurity AI)** — lightweight, extensible, **300+ model backends**,
  built-in recon/exploit/privesc tooling, self-hosted/air-gapped LLM support.
  Strong candidate to host the Attacker's tool-use loop rather than hand-rolling
  it. Evaluate: adopt CAI for the Attacker, keep our Finding/Store/Defender/
  benchmark as the ShadowTwin-specific glue.

**Caveat to respect (from the research):** a 7B local model will *not* reliably
do hard multi-hop reasoning. Practical answer: small local model for triage and
drafting; reserve a stronger model (bigger local or hosted free tier) for the
Attacker's reasoning and the Defender's mapping. The provider interface makes
this per-stage choice a config line.

---

## 6. The benchmark — the recognition artifact  *(WE BUILD)*

**What.** A harness that runs the pipeline across all labelled scenarios and
scores it: precision/recall on detection, **proof rate** (confirmed with
evidence vs. claimed), false-positive rate, fix-correctness, and
verification-pass rate.

**Why.** Your own notes: the reproducible benchmark is the artifact most likely
to earn real recognition (paper, talk, citation, CV). Because every stage writes
a structured Finding with a ground-truth-labelled scenario, scoring is just
reading fields. This is what turns "a repo that exists" into "a thing people
cite and run their own agents against."

**How.** `benchmark/` holds labelled scenarios + a scorer that reads
`finding.json` against each scenario's ground truth and emits a scorecard.
Because the LLM layer is swappable, the same harness scores different model sets
— that's the leaderboard story.

---

## 7. Frontend  *(DEFER)*
**What/why.** A graph + reasoning-feed dashboard. **Deferred.** The report
bundle *is* the v1 UI. Build the dashboard only after one scenario runs
end-to-end and the benchmark has a few entries — it's high effort, low
benchmark value, and your #1 risk is scope.

---

## 8. End-to-end flow (the golden path)

```
host agent / nuclei+nmap
        │  (telemetry + scanner hits)
        ▼
   [ Store.create ]  status=new
        ▼
   EVALUATOR  ──LLM──▶  finding.evaluator   status=triaged   (+evaluator.md)
        │  (reads asset/attack-path context from Store)
        ▼
   ATTACKER   ──tools/PoCs──▶  proof artifacts
        │  scope-lock ✔  ATT&CK technique tagged
        ├─ proof?  yes ─▶ finding.attacker  status=proven    (+attacker.md +proof/)
        └─ proof?  no  ─▶ status=not-reproducible (stops here, reported as such)
        ▼
   DEFENDER   ──LLM + mapping tables──▶ fix + NIST CSF/CIS/ATT&CK + impact
        │  disclaimer attached, applies nothing
        ▼  status=remediation-proposed → awaiting-human
   REPORT BUNDLE  (SUMMARY.md + per-finding folder)
        ▼
   👤 HUMAN  reads bundle, applies the recommended fix themselves
        ▼
   $ shadowtwin verify <finding_id>   (re-runs ONLY the Attacker)
        ├─ pass ─▶ status=reverified-pass   ✅ loop closed, proven fix
        └─ fail ─▶ status=awaiting-human    (back to the human)
        ▼
   BENCHMARK  scores the whole run vs. ground truth → scorecard
```

---

## 9. Build order (scope-disciplined, each step shippable)

1. **Backbone** — Finding model + `SqliteStore` + report renderer. *(no agents yet)*
2. **One scenario, one tool** — wire Juice Shop (or one VulHub CVE) + nuclei/nmap
   into the Store as candidate findings.
3. **Evaluator** — LLM triage → `triaged`, with the provider interface.
4. **Attacker** — scope-lock + one curated PoC + **proof-gate** → `proven`.
5. **Defender** — root-cause + NIST CSF/CIS/ATT&CK mapping + impact + disclaimer.
6. **Report bundle + `verify` command** — close the loop with a human in it.
7. **Benchmark** — label that first scenario, score the run end-to-end.
8. *Then fork:* add **Neo4jStore** (real graph) and/or more scenarios and/or a
   security-specialised model — none of which require touching the agents.

Get steps 1–7 perfect on **one** scenario before adding breadth. One golden
path that works beats six half-built ones — and it's already a paper.

---

## 10. Build-vs-reuse, at a glance

| Component | Build / Reuse | Concretely |
|---|---|---|
| Host agent | **BUILT** | Go binary (done) |
| Vulnerable lab | REUSE | Juice Shop, VulHub, DVWA/WebGoat |
| Scanners | REUSE | nuclei, nmap, sqlmap |
| Exploit PoCs | **BUILD** (thin) | curated per-scenario scripts |
| Agent tool-use runtime | REUSE (evaluate) | **CAI** framework |
| LLM runtime | REUSE | Ollama (local); Groq (hosted) |
| LLM models | REUSE | Qwen/DeepSeek; DeepHat/xOffense (security) |
| Threat intel | REUSE | KEV, EPSS, OSV, MITRE ATT&CK STIX |
| Graph DB | REUSE (when upgraded) | Neo4j Community |
| Compliance framework data | REUSE | NIST CSF, CIS v8, ATT&CK mitigations |
| **Finding object** | **BUILD** | the backbone |
| **Store interface + backends** | **BUILD** | the upgradability seam |
| **Agent orchestration + prompts** | **BUILD** | Evaluator/Attacker/Defender |
| **Proof-gating logic** | **BUILD** | the credibility anchor |
| **Compliance mapping tables** | **BUILD** | the differentiator |
| **Report renderer + verify cmd** | **BUILD** | the human loop |
| **Benchmark harness** | **BUILD** | the recognition artifact |
| Frontend | DEFER | report bundle is v1 UI |

The pattern: **we own the glue, the loop, the evidence discipline, the
compliance mapping, and the benchmark. We reuse every commodity part.** That is
exactly the slice that is novel and the slice that is worth your time.
