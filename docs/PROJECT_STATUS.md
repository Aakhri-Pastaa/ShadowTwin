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
**Version:** v1.1.0
**Project stage:** Scope frozen at the ingestion layer — shipped, with a
one-command demo environment

---

## Overall progress

| Area | Progress |
|---|---|
| Infrastructure | `██████████` 100% |
| Data pipeline | `██████████` 100% |
| CI / tests | `██████████` 100% |

The AI, database, dashboard and agent rows were removed: those components
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
ShadowTwin Forwarder
      │
      ▼
Kafka
      │  topics: wazuh-alerts, wazuh-logs
      ▼
[future] AI consumer
      │
      ▼
PostgreSQL
      │
      ▼
Streamlit
```

Note: this concrete pipeline is the near-term build path toward the
Wazuh → ingestor → PostgreSQL findings store → Evaluator → Attacker →
Defender design in `archive/original-architecture.md`. Kafka + the Forwarder sit in front of
what that doc calls the "ingestor"; the "AI consumer" is where
Evaluator-style triage will eventually plug in. **Open discrepancy:**
`archive/original-architecture.md` and `CLAUDE.md` say the frontend is Next.js;
this pipeline uses Streamlit. Not yet reconciled — flag before either doc
is treated as final.

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

## Current work

**None — the project is scope-frozen and released at v1.0.0.**

The ingestion edge (Wazuh → Forwarder → Kafka) is complete, tested in CI,
and running hands-off as a systemd service. On 2026-09-20 the remaining
platform components (Evaluator, Attacker, Defender, environment graph,
threat intel, lab, benchmark, frontend) were formally dropped rather than
carried as indefinite aspiration — see [`DECISIONS.md`](DECISIONS.md).

## Possible extensions

Not planned, but consistent with the frozen scope if ever picked up — a
**Kafka consumer** reading `wazuh-alerts` / `wazuh-logs`, normalizing
events and landing them in a findings store. That extends the existing
pipeline rather than reviving the archived platform.

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
- 59-check fault-injection suite passing in CI on every push ✅
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
