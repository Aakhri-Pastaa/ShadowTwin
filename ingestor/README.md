# ShadowTwin Ingestor

Reads the Wazuh alert topic the [forwarder](../forwarder/) writes and stores
each alert as a row in a PostgreSQL **findings** table — once, with no gaps,
across crashes, broker outages and database outages.

The forwarder guarantees at-least-once delivery into Kafka, so the topic can
contain the same alert twice. The ingestor turns that into exactly-once in the
table.

## How it works

**Offsets live in PostgreSQL, committed with the rows.** Each batch is one
transaction that inserts the rows and advances the stored offset of every
partition they came from, in `ingest_offsets`. A crash leaves both or
neither: on restart the ingestor reads the offset from PostgreSQL and
re-consumes exactly the batch that was not committed. Nothing is skipped,
and nothing is written twice by the ingestor itself.

**No consumer group.** Partitions are assigned directly
(`Consumer.assign()`) at the offsets PostgreSQL holds; nothing is ever
committed to Kafka. A consumer group would add a coordinator whose session
can expire during a broker outage — the demo showed a group member failing
to rejoin a single-node cluster — and with offsets in PostgreSQL it would
provide nothing. The cost: one ingestor instance, no automatic rebalancing.

**The alert is the key.** `findings` has `PRIMARY KEY (manager, alert_id)`,
Wazuh's alert id being unique per manager. A duplicate delivered by the
forwarder hits `ON CONFLICT DO NOTHING` and is counted as a duplicate, not
inserted. An alert without an id is keyed by the SHA-256 of its bytes, which
is identical for a re-delivered copy.

**Database outage → rewind.** If PostgreSQL goes away mid-batch, the batch
is rolled back — but the consumer's in-memory position has already moved
past it. The ingestor therefore reconnects with exponential backoff (1 s to
30 s) and **re-assigns every partition at the offset PostgreSQL holds**
before continuing. Resuming from the in-memory position would silently drop
the batch — removing the rewind makes the suite's outage check fail, which
was verified by doing exactly that.

**A bad value cannot wedge the pipeline.** If PostgreSQL rejects a batch
because of one value it cannot store (`DataError`, or text that cannot be
encoded), the batch is retried row by row, each in its own savepoint. Rows
that still fail are logged with their topic, partition and offset and
counted as rejected; everything else lands, and the offsets advance. Messages
that are not a JSON object are skipped the same way.

**Normalisation** (`normalize.py`) maps each alert to typed columns —
timestamp, rule id/level/description/groups, ATT&CK technique ids and
tactics, agent, decoder, location, source IP and user — and keeps the whole
alert in `raw` (`jsonb`). Missing fields become NULL or empty arrays rather
than rejecting the alert. Timestamps without a timezone are stored as NULL,
never guessed. U+0000, which PostgreSQL's `text` and `jsonb` cannot hold, is
replaced with U+FFFD.

The row is the ingestion slice of the Finding object designed in
[`docs/archive/architecture-v2.md`](../docs/archive/architecture-v2.md). The
triage, validation and remediation fields that design layered on top were
never built and are not part of the schema.

## Schema

[`schema.sql`](schema.sql), applied on every start (every statement is
idempotent):

- **`findings`** — one row per alert; indexes on time, level and time, agent
  and time, and a GIN index on ATT&CK technique ids. `source_topic`,
  `source_partition` and `source_offset` record the exact Kafka record each
  row came from.
- **`ingest_offsets`** — next offset to read, per topic and partition.

```sql
-- Most severe alerts in the last hour
SELECT ts, rule_level, agent_name, rule_description, mitre_ids
FROM findings WHERE ts > now() - interval '1 hour'
ORDER BY rule_level DESC, ts DESC LIMIT 20;

-- Which ATT&CK techniques fire most, and on how many agents
SELECT technique, count(*) AS alerts, count(DISTINCT agent_name) AS agents
FROM findings, unnest(mitre_ids) AS technique
GROUP BY technique ORDER BY alerts DESC;
```

## Configuration

Environment variables:

| Variable | Default | Meaning |
|---|---|---|
| `INGESTOR_BOOTSTRAP` | `localhost:9092` | Kafka brokers, comma-separated |
| `INGESTOR_TOPICS` | `wazuh-alerts` | Topics to ingest, comma-separated |
| `INGESTOR_DSN` | `postgresql://shadowtwin@localhost:5432/shadowtwin` | libpq connection string |
| `PGPASSWORD` | — | Database password (or a `~/.pgpass` file); keep it out of the DSN |
| `INGESTOR_BATCH` | `500` | Messages per transaction |
| `INGESTOR_START_FROM` | `beginning` | Where to start a partition with no stored offset: `beginning` or `end` |
| `INGESTOR_LOG_LEVEL` | `INFO` | `DEBUG` … `CRITICAL` |

`wazuh-logs` (the unfiltered archive stream) is not ingested by default:
those are events, not alerts. Add it to `INGESTOR_TOPICS` only if you want
every event in the table.

## Running it

The [demo](../demo/) runs it against PostgreSQL in one command. On its own:

```bash
docker build -t shadowtwin-ingestor ingestor
```

```bash
docker run --rm -e INGESTOR_BOOTSTRAP=kafka-host:9092 \
  -e INGESTOR_DSN=postgresql://shadowtwin@db-host:5432/shadowtwin \
  -e PGPASSWORD=... shadowtwin-ingestor
```

It logs a stats line every 60 seconds:

```
stats: consumed=1035 inserted=1029 duplicates=6 rejected=0 db_retries=1
```

`duplicates` counts alerts the forwarder delivered more than once, absorbed
by the primary key; `db_retries` counts database outages recovered from.

## Tests

```bash
cd ingestor && INGESTOR_TEST_DSN=postgresql://user:pass@localhost:5432/scratch python tests/smoke_test.py
```

30 checks. Normalisation always runs; the store and pipeline checks need a
real PostgreSQL and **drop and recreate the ingestor's tables — point
`INGESTOR_TEST_DSN` at a throwaway database**. Kafka is replaced by an
in-memory consumer that honours `assign()` and offsets, so every delivery
scenario is deterministic:

| Induced | Asserted |
|---|---|
| Re-delivered duplicate alerts | Stored once; the second copy is a no-op |
| Offset write fails inside the transaction | Rows rolled back with it |
| One value PostgreSQL cannot store, mid-batch | The other rows land; offsets advance past it |
| Junk (non-JSON, non-object) messages | Skipped, offsets still advance |
| Restart with a new consumer | Resumes at the offset PostgreSQL holds |
| Database connection lost mid-stream | Rewinds to the stored offsets; every alert stored exactly once |
| `start_from=end` on a first run | Only alerts written after start |

CI runs them against a PostgreSQL service container and fails if the
database checks are skipped.

## Limitations

- **One instance.** Partitions are assigned directly, so two ingestors would
  both read everything. The primary key keeps the table correct, but the
  work is doubled. Scaling out would need partition ownership.
- **Partitions are discovered at startup.** Partitions added to a topic
  while the ingestor runs are picked up on its next restart.
- **Schema, not migrations.** `schema.sql` is applied with `CREATE … IF NOT
  EXISTS`; a future column change would need a migration step.
- **One `INSERT` per row**, to count duplicates exactly. Comfortable at
  Wazuh alert rates, not tuned for bulk backfills.
- **Transport and storage only.** No triage, correlation or alerting.
