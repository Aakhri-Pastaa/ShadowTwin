# Troubleshooting

Real problems hit and how they were solved. Add an entry the same day you
solve something non-obvious — this file is only useful if it stays current.

---

### Kafka UI shows 0 messages

**Reason.** Looking at the Broker page instead of the topic's message view.

**Solution.** Open the relevant **Topic → Messages** tab, not the Broker
overview.

---

### Forwarder not sending

**Reason.** The forwarder service isn't running (or a start-up check failed).

**Solution.** It's a systemd service now:
`sudo systemctl restart shadowtwin-forwarder`, then
`journalctl -u shadowtwin-forwarder -e` — the startup validation logs
exactly which check (Kafka / Topics / Files / Permissions) failed. A
Permissions failure is the only one that aborts start.

> For forwarder-specific issues (permission denied on `alerts.json`,
> missing `archives.json`, brokers down, duplicate events after a crash),
> see the dedicated troubleshooting section in
> [`forwarder/README.md`](../forwarder/README.md).

---

### Kafka offsets not increasing

**Reason.** The forwarder stopped producing (see "Forwarder not sending"
above) — offsets only advance when something is written to the topic.

**Solution.** Check the forwarder's service/process status first, before
suspecting Kafka itself.
