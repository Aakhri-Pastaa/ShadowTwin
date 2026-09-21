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

## 2026-09-21

**Decision.** Reopen the scope-frozen forwarder for a correctness fix and
release it as v1.1.0.

**Reason.** The demo environment found a data-loss bug in v1.0.0: if the log
rotated while the forwarder was stopped, it read the new file from zero and
lost the old file's tail. That contradicted the one guarantee the project
makes — "duplicates are possible, gaps are not" — in writing. The freeze
excludes new features; it never excluded making the shipped behaviour match
its documentation. Leaving a known gap under a no-gaps claim would have been
the same mistake the freeze was meant to correct.

**Decision.** Recover by inode, not by filename, and log what cannot be
recovered.

**Reason.** Rotation renames files, so a name recorded at checkpoint time
means nothing after a restart; the inode is the file's identity. Generations
rotated after the checkpointed one are queued by inode and resolved at the
moment they are opened, because a name can point somewhere else by then. A
rotated file that was deleted or compressed cannot be read, and pretending
otherwise is worse than an explicit ERROR naming the offset that was lost.

---

## 2026-09-20

**Decision.** Freeze the project's scope at the ingestion layer. The
Evaluator, Attacker, Defender, environment graph, threat-intel correlation,
vulnerable lab, benchmark, and frontend described in the original
architecture are **not** going to be built. `docs/architecture.md` and
`docs/ROADMAP.md` move to `docs/archive/`; `docs/API.md`, `docs/AI.md` and
`docs/TODO.md` are deleted.

**Reason.** Those components were placeholders for work that was never
started, and documenting them as though they were planned made the
repository describe a system that does not exist. Most damagingly,
`SECURITY.md`, `README.md` and `CLAUDE.md` all referenced an attacker
scope-lock reading `lab/scope.yaml` — a safety control that was never
implemented. Removing the claims is the honest fix; building the platform is
months of work this project is not going to get.

What *was* built — the Wazuh → Kafka ingestion edge — is complete, tested
and deployed, and stands on its own. The same judgement that archived the Go
agent in favour of Wazuh (2026-07-02) applies here: keep what earns its
place, archive the rest rather than carrying it as permanent aspiration.

**Decision.** Add CI for `forwarder/`; delete the `frontend` and
`python-services` workflows.

**Reason.** Both watched paths that do not exist (`frontend/`, `graph/`,
`threat-intel/`, `evaluator/`, `attacker/`, `defender/`), so neither ever
ran — while the one component with real code and a 42-check test suite
had no automated verification at all. Ruff rule selection is now pinned
explicitly in `forwarder/pyproject.toml`: the previous config inherited
ruff's defaults, which drift between releases and turn CI red on code that
never changed.

---

## 2026-07-03 (later)

**Decision.** Ship the forwarder's config as `/etc/shadowtwin-forwarder/config.yaml`,
separate from the application code under `/opt`.

**Reason.** Clean separation of configuration from code — the installer
never overwrites an existing `/etc` config on upgrade, so a redeploy can't
clobber a tuned production setup. The in-repo `config.yaml` is only a
template (broker defaults to `localhost:9092`; real brokers are set per
deployment or via `SHADOWTWIN_KAFKA_BOOTSTRAP_SERVERS`).

**Decision.** Bring the forwarder source into the monorepo under
[`forwarder/`](../forwarder/).

**Reason.** It's now a production component of the pipeline, not a throwaway
script — it belongs alongside the docs and (future) consumer/agents so the
whole system versions together. Real broker IPs/hostnames were genericized
before committing (public repo); the real values live only in the
gitignored `INFRASTRUCTURE.md`.

---

## 2026-07-03

**Decision.** Use Kafka instead of Redis Streams for the telemetry pipeline.

**Reason.** Replay capability, persistence, and scalability that Redis
Streams doesn't give us as cleanly for this use case.

---

**Decision.** Use Streamlit for the user-facing dashboard instead of Kafka UI.

**Reason.** Kafka UI is infrastructure/ops tooling — it exposes broker
internals, not a product surface. Streamlit is the actual user-facing
layer. (Note: this doesn't match `archive/original-architecture.md`'s "Next.js frontend" —
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
