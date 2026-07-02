# AI

Everything related to the AI/LLM side of ShadowTwin: models, prompts,
routing, RAG, and evaluation. This is the detail-level companion to the
**tools-first** principle in `CLAUDE.md`: AI is reserved for what
deterministic rules can't do (ambiguous triage, cross-signal correlation,
plain-language explanation) — most of the pipeline should not touch a
model at all.

Currently a placeholder — the AI consumer / Evaluator hasn't been built yet
(see `PROJECT_STATUS.md`, `ROADMAP.md` Phase 3). Fill in as it lands:

## Models

*(which local/hosted models are used and for what — triage vs. explanation
vs. attacker planning likely want different models/cost profiles)*

## Prompt templates

*(triage prompts, MITRE ATT&CK mapping prompts, evaluation/scoring
prompts — link to the actual template files once they exist)*

## Routing

*(what decides whether a finding needs an AI pass at all, vs. Wazuh's rule
engine already having resolved it)*

## RAG / context sources

*(threat-intel correlation, the environment graph, past findings — what
gets retrieved and attached to a prompt)*

## Evaluation

*(how prompt/model changes get regression-tested against known findings
before they ship — see `TROUBLESHOOTING.md` once real failure modes show up)*
