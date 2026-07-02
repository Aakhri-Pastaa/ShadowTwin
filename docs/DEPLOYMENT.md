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
Install the ShadowTwin Forwarder
      │
      ▼
Enable the Forwarder as a systemd service   ← current milestone, see PROJECT_STATUS.md
      │
      ▼
Verify Kafka is reachable and topics exist
      │
      ▼
Verify end-to-end stream (Wazuh alert → Kafka message)
```

## Verifying the stream

1. Trigger a Wazuh alert (any rule that fires is fine for a smoke test).
2. Confirm the Forwarder picked it up (check its logs / service status).
3. Confirm the message landed in the right Kafka topic (`wazuh-alerts` or
   `wazuh-logs`) — via a Kafka client, not Kafka UI (see the note in
   `PROJECT_STATUS.md` about Kafka UI being infra-only tooling, not a
   workflow step).
