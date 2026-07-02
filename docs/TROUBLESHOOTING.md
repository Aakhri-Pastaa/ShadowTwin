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

**Reason.** The forwarder process isn't running.

**Solution.** Once it's a systemd service (see `PROJECT_STATUS.md` current
milestone): `systemctl start shadowtwin-forwarder`. Until then, start it
manually and check its logs.

---

### Kafka offsets not increasing

**Reason.** The forwarder stopped producing (see "Forwarder not sending"
above) — offsets only advance when something is written to the topic.

**Solution.** Check the forwarder's service/process status first, before
suspecting Kafka itself.
