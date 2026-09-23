# AI-assisted triage (experiment)

**A design for a local-LLM triage layer over ShadowTwin's `findings` table: deterministic enrichment from MITRE ATT&CK, the CISA Known Exploited Vulnerabilities (KEV) catalog and the NVD, followed by a verdict from a local model (Qwen3-14B via Ollama) that is rejected if it cites any fact the enrichment did not produce.**

> [!NOTE]
> **Status: design only — not built, not tested, not part of the pipeline.** The project is the pipeline described in the [root README](../../README.md); its scope freeze stands. Nothing in this directory runs, and nothing here claims a result.
>
> Designed and directed by Kunal Patil; drafted with AI coding assistants. See [AI disclosure](#ai-disclosure).

## What it would do

The ingestor already stores every Wazuh alert as a `findings` row: typed
columns for rule, level, agent and source, the ATT&CK technique IDs Wazuh
attached (`mitre_ids`), and the full alert as `jsonb`. The experiment would
read new rows, decide which deserve an analyst's attention, and write one
`verdicts` row per case with the reasoning and the evidence behind it.

It would not act on hosts, block anything, or replace analyst review. It
does not train or fine-tune a model; it uses a pre-trained one with the
facts supplied in the prompt.

## The design rule

**The model never asserts a fact.** Every fact in a verdict — a technique
name, a tactic, a CVSS score, whether a CVE is exploited in the wild — comes
from a deterministic lookup made before the model is called. The model
contributes judgement only: how suspicious the case is, how confident, and
what an analyst should check next. A verdict that cites a technique ID or a
CVE the enrichment step did not produce is rejected and counted, so the
hallucinated-fact rate is a measured number rather than a hope.

## How it would work

```text
findings (PostgreSQL)
   ↓  prefilter: dedup, collapse bursts, drop known-good     ← no model, cheap
   ↓  enrich: ATT&CK extract · KEV catalog · NVD CVSS        ← deterministic, cached
   ↓  case file → qwen3:14b (JSON schema, temperature 0)     ← judgement only
   ↓  validate: every cited technique and CVE ⊆ enrichment set
   ↓  verdicts: verdict, confidence, rationale, cited facts,
                model, prompt hash, latency, raw response
```

1. **Prefilter.** Duplicate alerts are merged, bursts are collapsed into one
   case (a flood of rule 5712, sshd brute force, becomes a single case with
   a count), allowlisted admin activity is dropped, and a level threshold
   applies. Only what survives costs model time.
2. **Enrichment.** Technique IDs resolve to names and tactics from a
   vendored extract of the ATT&CK enterprise bundle. CVE IDs, which Wazuh's
   vulnerability detector places in the alert (for example
   `data.vulnerability.cve`), resolve to KEV membership and NVD CVSS v3.1
   scores. Results are cached in PostgreSQL, so NVD's rate limit and outages
   do not stall triage.
3. **Model call.** The case file — the alert and the enrichment facts — goes
   to Ollama with the verdict's JSON schema as the required output format
   and temperature 0.
4. **Validation.** The verdict must parse against the schema, and its cited
   technique IDs and CVEs must be a subset of the enrichment set. A failure
   is stored with its reason instead of being retried into agreement.
5. **Storage.** Each verdict keeps its provenance: model name and digest,
   SHA-256 of the prompt, latency, and the raw response, so every result can
   be audited and reproduced.

| Verdict field | Meaning |
|---|---|
| `verdict` | `benign`, `suspicious` or `malicious` |
| `confidence` | 0.0–1.0 |
| `rationale` | Short reasoning, referring only to cited facts |
| `cited_facts` | Technique IDs and CVEs the rationale relies on |
| `next_steps` | What an analyst should check |
| `rejected_reason` | Set when validation fails; the verdict is not used |

## Verified inputs

Checked on 2026-09-23 from my Windows machine. These checks confirm that
the inputs exist and are usable; no model was run on any alert.

| Source | Check | Result |
|---|---|---|
| CISA KEV JSON feed | Downloaded and parsed | Catalog 2026.09.22, 1,721 entries, 1.7 MB; lists CVE-2021-44228 |
| NVD CVE API 2.0 | `cveId=CVE-2021-44228`, no API key | HTTP 200; CVSS v3.1 10.0, CRITICAL |
| MITRE ATT&CK enterprise bundle (`mitre/cti`) | Downloaded and reduced to id, name, tactics, first line of description | 48.0 MB → 0.26 MB for 697 non-revoked, non-deprecated techniques; `T1110.001` → Password Guessing (`credential-access`) |
| Ollama 0.31.1, `qwen3:14b` | Listed through `/api/tags` | Installed: Q4_K_M, 9.3 GB, 40,960-token context, advertises tools and thinking. Not run on alerts |

## Evaluation plan

A result is publishable only against ground truth and a baseline.

- **Synthetic benchmark.** Extend the demo's alert generator with labelled
  scenarios — routine admin activity, SSH brute force followed by a
  successful login, sudo to root, and a vulnerability-detector alert for a
  KEV-listed CVE. Labels are known by construction, so anyone who clones the
  repository can reproduce the numbers.
- **Homelab check.** 100–200 real alerts, labelled by hand. Reported
  separately; the alerts themselves are not published.
- **Metrics.** Precision and recall for `suspicious`/`malicious` against
  `benign`; the rejected-for-ungrounded-facts rate; the prefilter's
  reduction ratio; latency per case, with the hardware stated.
- **Baseline.** The same labels scored by Wazuh's `rule.level` alone (for
  example level ≥ 10). The model is worth running only if it beats that.

## Why it is not built

The design is feasible and its inputs are checked, but I don't currently
have the resources to run and evaluate it properly: model inference over a
labelled set large enough to mean something, and the time to label real
alerts. An untested LLM layer would weaken the part of this project that is
verified, so it stays a design until the evaluation can be run.

## Status

| Area | Status | Notes |
|---|---|---|
| Prefilter | Not built | |
| Enrichment (ATT&CK, KEV, NVD) | Not built | Sources verified reachable on 2026-09-23 |
| Model verdict and validation | Not built | `qwen3:14b` installed, not run |
| `verdicts` table | Not built | |
| Evaluation (synthetic + homelab) | Not built | |

## Origin

This narrows the Evaluator specified in
[`docs/archive/evaluator-architecture.md`](../../docs/archive/evaluator-architecture.md)
(June 2026): prefilter, context assembly, local-LLM triage and grounded
ATT&CK mapping. It drops that spec's tool-calling agent loop, vector
retrieval and hand-off to an Attacker, and adds the rejection rule and the
KEV and NVD enrichment.

## AI disclosure

I decided the idea, its scope, the rule that the model never asserts facts,
the choice of data sources and model, and to keep it as an unbuilt
experiment rather than ship it untested. An AI coding assistant (Claude
Code) drafted this design under my direction and ran the source checks in
[Verified inputs](#verified-inputs). Nothing here has been executed against
alerts.

## Credits

MITRE ATT&CK® (© The MITRE Corporation, used under the ATT&CK terms of use),
the CISA Known Exploited Vulnerabilities catalog, and the NIST National
Vulnerability Database. No data from them is committed yet.
