# Decisions

A lightweight, fast-to-write running log of choices made and why — so six
months from now the reasoning isn't lost. This is deliberately low-ceremony.

**This does not replace `docs/adr/`.** Per `CLAUDE.md`'s hard rule, any
decision that's genuinely load-bearing (new datastore, new language,
dropping/merging a layer) still gets a proper ADR in `docs/adr/` with full
context and consequences. Use this file for the smaller, frequent calls —
promote an entry here to a full ADR if it turns out to matter more than
expected.

---

## 2026-07-03

**Decision.** Use Kafka instead of Redis Streams for the telemetry pipeline.

**Reason.** Replay capability, persistence, and scalability that Redis
Streams doesn't give us as cleanly for this use case.

---

**Decision.** Use Streamlit for the user-facing dashboard instead of Kafka UI.

**Reason.** Kafka UI is infrastructure/ops tooling — it exposes broker
internals, not a product surface. Streamlit is the actual user-facing
layer. (Note: this doesn't match `architecture.md`'s "Next.js frontend" —
see the open discrepancy flagged in `PROJECT_STATUS.md`.)

---

**Decision.** Run the ShadowTwin Forwarder as a systemd service.

**Reason.** Host integration, automatic restart, boot persistence — the
service shouldn't need a human to start it after a reboot.

---

**Decision.** Use the official Apache Kafka image.

**Reason.** Official image, Kafka 4, KRaft support (no separate ZooKeeper
to operate).

---

## 2026-07-02

**Decision.** Pivot from a custom Go host agent to Wazuh as the primary
telemetry/detection source; archive the Go agent as `go-agent-v0/` rather
than deleting it.

**Reason.** Wazuh already provides mature multi-platform collection, a
decoder/rule engine, MITRE ATT&CK mapping, vulnerability detection, and CIS
assessment. Reusing it lets effort go into the differentiating agents
(Evaluator/Attacker/Defender) instead of re-solving telemetry shipping. See
ADR-0002 for the original (now-superseded) reasoning for building a custom
agent, and the root `DEVLOG.md` / `go-agent-v0/DEVLOG.md` for the full
history either side of the pivot.
