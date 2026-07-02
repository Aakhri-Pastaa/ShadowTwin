# ShadowTwin Project Status

> **This is the living, as-built status doc.** It describes what is
> *actually running* right now, and gets updated every session. It will
> often be ahead of or different from [`architecture.md`](architecture.md)
> (the target/aspirational design) — that's expected. When the two
> diverge for more than a session or two, reconcile them or record why in
> [`DECISIONS.md`](DECISIONS.md).
>
> Real hostnames, container IDs, and IPs are **not** recorded here — see
> the local-only, gitignored [`INFRASTRUCTURE.md`](INFRASTRUCTURE.md) for
> those.

**Last updated:** 2026-07-03
**Version:** v0.1.0
**Project stage:** Infrastructure pipeline in progress

---

## Overall progress

| Area | Progress |
|---|---|
| Infrastructure | `████████░░` 80% |
| Data pipeline | `██████████` 100% |
| AI | `██░░░░░░░░` 20% |
| Database | `░░░░░░░░░░` 0% |
| Dashboard | `░░░░░░░░░░` 0% |
| Agents (Evaluator/Attacker/Defender) | `░░░░░░░░░░` 0% |

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
Defender design in `architecture.md`. Kafka + the Forwarder sit in front of
what that doc calls the "ingestor"; the "AI consumer" is where
Evaluator-style triage will eventually plug in. **Open discrepancy:**
`architecture.md` and `CLAUDE.md` currently say the frontend is Next.js;
this pipeline uses Streamlit. Not yet reconciled — flag before either doc
is treated as final.

---

## Completed

- ✅ Lab infrastructure (Proxmox-based; see `INFRASTRUCTURE.md` for specifics)
- ✅ Wazuh manager deployed
- ✅ Kafka deployed
- ✅ Kafka UI (infrastructure-only tooling — see Notes)
- ✅ Docker
- ✅ ShadowTwin Forwarder (ships Wazuh JSON logs to Kafka)
- ✅ `archives.json` streaming, verified end-to-end
- ✅ `alerts.json` streaming, verified end-to-end
- ✅ Kafka message delivery verified
- ✅ Pivoted repo docs/architecture from custom Go agent to Wazuh-based
  design; archived the Go agent as `go-agent-v0/` (PR #7)

## Current work

**Productionizing the ShadowTwin Forwarder.**

- Current state: runs in development mode (started manually).
- Need: a `systemd` service so it survives restarts and doesn't need a
  human to start it.

## Next milestone

Implement `shadowtwin-forwarder.service` (systemd unit).

## Blockers

None.

## Known issues

- Forwarder is currently started manually — needs systemd (see Current work).

## Success criteria (current milestone)

`systemctl start shadowtwin-forwarder` works, and the service comes back
up automatically after a reboot.

## Notes

- Kafka UI is infrastructure/ops tooling only. End users (and the eventual
  Streamlit dashboard) should never need it — if a feature requires someone
  to open Kafka UI, that's a gap in the dashboard, not a workaround.
