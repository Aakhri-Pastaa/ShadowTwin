-- ShadowTwin findings store.
--
-- One row per Wazuh alert: the ingestion slice of the Finding object designed
-- in docs/archive/architecture-v2.md. Typed columns for what queries filter
-- on; the complete alert in `raw` so nothing Wazuh emitted is lost.
--
-- Applied on every ingestor start; every statement is idempotent.

CREATE TABLE IF NOT EXISTS findings (
    manager          text        NOT NULL,
    alert_id         text        NOT NULL,
    ts               timestamptz,
    rule_id          text,
    rule_level       smallint,
    rule_description text,
    rule_groups      text[]      NOT NULL DEFAULT '{}',
    mitre_ids        text[]      NOT NULL DEFAULT '{}',
    mitre_tactics    text[]      NOT NULL DEFAULT '{}',
    agent_id         text,
    agent_name       text,
    agent_ip         text,
    decoder          text,
    location         text,
    src_ip           text,
    src_user         text,
    raw              jsonb       NOT NULL,
    -- Provenance: the exact Kafka record this row came from.
    source_topic     text        NOT NULL,
    source_partition integer     NOT NULL,
    source_offset    bigint      NOT NULL,
    ingested_at      timestamptz NOT NULL DEFAULT now(),
    -- The forwarder is at-least-once, so the topic can hold the same alert
    -- twice. Wazuh's alert id is unique per manager; this key makes the
    -- second copy a no-op.
    PRIMARY KEY (manager, alert_id)
);

CREATE INDEX IF NOT EXISTS findings_ts_idx       ON findings (ts);
CREATE INDEX IF NOT EXISTS findings_level_ts_idx ON findings (rule_level, ts);
CREATE INDEX IF NOT EXISTS findings_agent_ts_idx ON findings (agent_name, ts);
CREATE INDEX IF NOT EXISTS findings_mitre_idx    ON findings USING gin (mitre_ids);

-- Where to resume each partition. Written in the same transaction as the
-- rows it covers, so the two can never disagree.
CREATE TABLE IF NOT EXISTS ingest_offsets (
    topic       text        NOT NULL,
    partition   integer     NOT NULL,
    next_offset bigint      NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (topic, partition)
);
