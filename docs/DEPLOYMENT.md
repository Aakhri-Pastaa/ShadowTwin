# Deployment

High-level deployment sequence for the current pipeline. This file
describes *steps*, not real hosts/IPs — see [`TOPOLOGY.md`](TOPOLOGY.md)
for the genericized public diagram, or the local-only, gitignored
`INFRASTRUCTURE.md` for actual target details.

```
Install Docker
      │
      ▼
Install Kafka (official image, KRaft mode)
      │
      ▼
Install Wazuh (manager + agent)
      │
      ▼
Install the ShadowTwin Forwarder   →  sudo forwarder/install_service.sh
      │                                (creates service user + venv + systemd
      ▼                                 unit, installs /etc config, health-checks)
Forwarder runs as a systemd service   ✅ done — auto-starts on boot
      │
      ▼
Verify Kafka is reachable and topics exist
      │
      ▼
Verify end-to-end stream (Wazuh alert → Kafka message)
```

## Forwarder install (one command)

The forwarder ships its own installer — see
[`forwarder/README.md`](../forwarder/README.md) for the full detail:

```bash
cd forwarder
sudo ./install_service.sh     # user + venv + unit + /etc config + health check
```

Day-to-day it's pure systemd — no venv activation, no manual `python app.py`:

```bash
systemctl status shadowtwin-forwarder
sudo systemctl restart shadowtwin-forwarder
journalctl -u shadowtwin-forwarder -f
```

## Verifying the stream

1. Trigger a Wazuh alert (any rule that fires is fine for a smoke test).
2. Confirm the Forwarder picked it up (check its logs / service status).
3. Confirm the message landed in the right Kafka topic (`wazuh-alerts` or
   `wazuh-logs`) — via a Kafka client, not Kafka UI (see the note in
   `PROJECT_STATUS.md` about Kafka UI being infra-only tooling, not a
   workflow step).
