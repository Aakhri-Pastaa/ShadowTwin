# Archive

The platform ShadowTwin was originally designed to become — a closed-loop,
human-in-the-loop purple-team lab — and the plans for building it. **None of
it was implemented.** The scope was frozen at the ingestion layer on
2026-09-20; the reasoning is in [`../DECISIONS.md`](../DECISIONS.md).

These are kept because the design reasoning is part of the project's history,
and because some of it carried forward: the findings schema used by
[`../../ingestor/`](../../ingestor/) is the ingestion slice of the Finding
object defined in `architecture-v2.md`.

| Document | Written | What it is |
|---|---|---|
| [`PROJECT-FINAL.md`](PROJECT-FINAL.md) | 2026-06-20 | The locked project definition: scope, the claims discipline (what may and may not be said), build order, and a skeptic's checklist |
| [`architecture-v2.md`](architecture-v2.md) | 2026-06-18 | The full blueprint: the Finding object and its status lifecycle, the Store interface, the three agent stages, the report bundle, `verify`, the LLM provider interface, the benchmark |
| [`agents-tools-first.md`](agents-tools-first.md) | 2026-06-20 | Why deterministic tools do the mechanical work and an LLM only what rules cannot — and why "tools only" would be automation, not AI |
| [`evaluator-architecture.md`](evaluator-architecture.md) | 2026-06-19 | Build spec for the triage stage: pre-trained local LLM with retrieved context, deterministic pre-filtering, grounded ATT&CK mapping |
| [`original-architecture.md`](original-architecture.md) | 2026-06 | The earlier, thinner sketch these superseded |
| [`ROADMAP.md`](ROADMAP.md) | 2026-07 | The phased plan toward that design |
