# ShadowTwin Project Status

> **This is the living, as-built status doc.** It describes what is
> *actually running* right now, and gets updated every session. It will
> often be ahead of or different from [`archive/original-architecture.md`](archive/original-architecture.md)
> (the target/aspirational design) — that's expected. When the two
> diverge for more than a session or two, reconcile them or record why in
> [`DECISIONS.md`](DECISIONS.md).
>
> Real hostnames, container IDs, and IPs are **not** recorded here — see
> the local-only, gitignored [`INFRASTRUCTURE.md`](INFRASTRUCTURE.md) for
> those.

**Last updated:** 2026-09-21
**Version:** v1.2.0
**Project stage:** Scope frozen at the ingestion layer — Wazuh → Kafka →
PostgreSQL pipeline built, with a one-command demo environment

---

## Overall progress

| Area | Progress |
|---|---|
| Infrastructure | `██████████` 100% |
| Data pipeline (forwarder) | `██████████` 100% |
| Findings store (ingestor) | `██████████` 100% — built and demo-verified; not yet deployed on the homelab |
| CI / tests | `██████████` 100% |

The AI, dashboard and agent rows were removed: those components
were never started and are no longer planned. See
[`DECISIONS.md`](DECISIONS.md) and [`archive/`](archive/).

---

## Current architecture (as-built)

See [`TOPOLOGY.md`](TOPOLOGY.md) for a rendered diagram with host-role
breakdown. Quick version:

```
Windows endpoint
      │
      ▼
Wazuh agent
      │
      ▼
Wazuh manager
      │  alerts.json / archives.json
      ▼
ShadowTwin Forwarder          at-least-once
      │
      ▼
Kafka
      │  topics: wazuh-alerts, wazuh-logs
      ▼
ShadowTwin Ingestor *         offsets committed with the rows
      │
      ▼
PostgreSQL findings *         one row per alert, deduplicated
```

`*` Built and verified end to end in the [`demo/`](../demo/) environment;
not yet deployed on the homelab, where the pipeline currently ends at Kafka.

There is no dashboard, triage or attack stage, and none is planned: the
platform those would have belonged to was archived on 2026-09-20.

---

## Completed

- ✅ Lab infrastructure (Proxmox-based; see `INFRASTRUCTURE.md` for specifics)
- ✅ Wazuh manager deployed
- ✅ Kafka deployed
- ✅ Kafka UI (infrastructure-only tooling — see Notes)
- ✅ Docker
- ✅ ShadowTwin Forwarder (ships Wazuh JSON logs to Kafka) — source now in
  [`forwarder/`](../forwarder/)
- ✅ `archives.json` streaming, verified end-to-end
- ✅ `alerts.json` streaming, verified end-to-end
- ✅ Kafka message delivery verified
- ✅ **Forwarder productionized** — `systemd` service (`shadowtwin-forwarder`),
  dedicated service user, config split to `/etc/`, health checks, state
  persistence across restarts, auto-restart on crash/reboot. Validated:
  restart, reboot, and crash-recovery all bring it back automatically.
- ✅ Pivoted repo docs/architecture from custom Go agent to Wazuh-based
  design; archived the Go agent as `go-agent-v0/` (PR #7)
- ✅ Scope frozen at the ingestion layer; every document aligned with what
  exists; forwarder CI; v1.0.0 (PR #9)
- ✅ One-command demo environment with a gap-auditing consumer; it found a
  data-loss bug in v1.0.0 (rotation while stopped), fixed in v1.1.0 (PR #11)
- ✅ **Ingestor → PostgreSQL findings store** — offsets committed in the same
  transaction as the rows, direct partition assignment, alert-id primary
  key, rewind on database loss, poison-value isolation. Verified in the demo:
  ingestor killed, database stopped, forwarder killed across rotations —
  6 duplicates in the topic, 0 duplicates and 0 gaps in the table (v1.2.0)

## Current work

**None — the project is scope-frozen; v1.2.0 adds the findings store.**

The ingestion edge (Wazuh → Forwarder → Kafka) is complete, tested in CI,
and running hands-off as a systemd service. The ingestor and findings store
are complete and tested in CI and in the demo. On 2026-09-20 the remaining
platform components (Evaluator, Attacker, Defender, environment graph,
threat intel, lab, benchmark, frontend) were formally dropped rather than
carried as indefinite aspiration — see [`DECISIONS.md`](DECISIONS.md).

## Possible extensions

Not planned, but consistent with the frozen scope:

- **Deploy the ingestor on the homelab** next to the existing Kafka, so the
  as-built pipeline matches the demo. Operational work, no new code.
- A read-only view over `findings`. It would present the store, not revive
  the archived platform.

## Blockers

None.

## Known issues

None open. (The forwarder's manual-start limitation is resolved — it's a
systemd service now.)

## Definition of done

- Forwarder streams both Wazuh logs to Kafka with at-least-once delivery ✅
- Survives rotation, truncation, deletion, broker outage, crash, reboot ✅
- Survives rotation *while stopped*, including several rotations ✅ (v1.1.0)
- Runs unattended as a systemd service with health checks ✅
- 59-check forwarder suite and 30-check ingestor suite (against real
  PostgreSQL) passing in CI on every push ✅
- Findings store: exactly one row per alert across ingestor crash, database
  outage and forwarder re-delivery ✅ (v1.2.0)
- Reproducible without Wazuh: `cd demo && docker compose up --build` ✅
- Every document describes only what exists ✅

## Notes

- Kafka UI is infrastructure/ops tooling only. End users (and the eventual
  Streamlit dashboard) should never need it — if a feature requires someone
  to open Kafka UI, that's a gap in the dashboard, not a workaround.
- Day-to-day the forwarder is managed entirely through systemd
  (`systemctl {start,stop,restart,status} shadowtwin-forwarder`,
  `journalctl -u shadowtwin-forwarder -f`). No venv activation or manual
  `python app.py` for normal operation.
