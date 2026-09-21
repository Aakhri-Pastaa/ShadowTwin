"""ShadowTwin Ingestor — Wazuh alerts from Kafka into a PostgreSQL findings store.

Reads the topics the forwarder writes, normalises each alert into a row
(``normalize.py``), and commits each batch together with the Kafka offsets it
covers (``store.py``). Offsets live in PostgreSQL, not in Kafka:

* a batch is committed with its offsets or not at all, so a crash re-reads
  exactly the uncommitted batch — no gap, and no row written twice;
* partitions are assigned directly rather than through a consumer group, so a
  broker outage cannot expire a group session and strand the consumer;
* the forwarder is at-least-once, so the topic can repeat an alert — the
  table's primary key makes the second copy a no-op.

If PostgreSQL goes away, the batch in hand is not committed, but the
consumer's in-memory position has already moved past it. Recovery therefore
rewinds every partition to the offset PostgreSQL holds, then continues.

Configuration (environment):
    INGESTOR_BOOTSTRAP   Kafka brokers, comma-separated   (default localhost:9092)
    INGESTOR_TOPICS      topics, comma-separated          (default wazuh-alerts)
    INGESTOR_DSN         libpq connection string          (default below; password
                         via PGPASSWORD or a ~/.pgpass file, never on the command line)
    INGESTOR_BATCH       messages per transaction          (default 500)
    INGESTOR_START_FROM  beginning | end, first run only   (default beginning)
    INGESTOR_LOG_LEVEL   DEBUG..CRITICAL                   (default INFO)
"""

from __future__ import annotations

import logging
import os
import signal
import sys
import threading
import time

import psycopg
from confluent_kafka import (
    OFFSET_BEGINNING,
    OFFSET_END,
    Consumer,
    KafkaError,
    KafkaException,
    TopicPartition,
)

from normalize import normalize
from store import Store

logger = logging.getLogger("ingestor")

STATS_INTERVAL = 60.0   # seconds between stats lines
BACKOFF_MAX = 30.0      # longest wait between reconnect attempts


def assign(consumer, store: Store, topics: list[str], start_from: str) -> None:
    """Assign every partition of *topics* at the offset PostgreSQL holds."""
    saved = store.load_offsets()
    default = OFFSET_BEGINNING if start_from == "beginning" else OFFSET_END
    metadata = consumer.list_topics(timeout=10)
    partitions = []
    for topic in topics:
        meta = metadata.topics.get(topic)
        if meta is None or meta.error is not None:
            raise LookupError(f"topic {topic} is not available")
        for partition in sorted(meta.partitions):
            partitions.append(TopicPartition(topic, partition,
                                             saved.get((topic, partition), default)))
    consumer.assign(partitions)
    for tp in partitions:
        where = {OFFSET_BEGINNING: "the beginning", OFFSET_END: "the end"}.get(
            tp.offset, f"offset {tp.offset}")
        logger.info("reading %s[%d] from %s", tp.topic, tp.partition, where)


def connect(consumer, store: Store, topics: list[str], start_from: str,
            stop: threading.Event) -> bool:
    """(Re)connect PostgreSQL and (re)assign from its offsets, retrying with backoff.

    A database or broker that is not up yet is a condition that heals, not a
    reason to exit. Returns False only if asked to stop while waiting.
    """
    delay = 1.0
    while not stop.is_set():
        try:
            store.connect()
            assign(consumer, store, topics, start_from)
            return True
        except (psycopg.OperationalError, KafkaException, LookupError) as exc:
            # Database down, broker unreachable, topic not created yet.
            # Anything else is a bug and should surface, not retry forever.
            logger.warning("not ready (%s); retrying in %.0f s", exc, delay)
            stop.wait(delay)
            delay = min(delay * 2, BACKOFF_MAX)
    return False


def run(consumer, store: Store, topics: list[str], batch: int, start_from: str,
        stop: threading.Event) -> int:
    """Main loop; returns a process exit code."""
    if not connect(consumer, store, topics, start_from, stop):
        return 0
    stats = {"consumed": 0, "inserted": 0, "duplicates": 0, "rejected": 0, "db_retries": 0}
    last_stats = time.monotonic()

    while not stop.is_set():
        rows, offsets, consumed, skipped = [], {}, 0, 0
        for msg in consumer.consume(num_messages=batch, timeout=1.0):
            err = msg.error()
            if err is not None:
                if err.code() != KafkaError._PARTITION_EOF:
                    logger.warning("Kafka: %s", err)
                continue
            consumed += 1
            key = (msg.topic(), msg.partition())
            offsets[key] = msg.offset() + 1
            row = normalize(msg.value(), key[0], key[1], msg.offset())
            if row is None:
                skipped += 1
                logger.warning("skipping %s[%d]@%d: not a JSON object", *key, msg.offset())
            else:
                rows.append(row)

        if offsets:
            try:
                inserted, rejected = store.write(rows, offsets)
            except psycopg.OperationalError as exc:
                # Nothing from this batch was committed; it is re-read after
                # the rewind, so it is not counted yet.
                stats["db_retries"] += 1
                logger.error("PostgreSQL unavailable (%s); rewinding to the stored offsets", exc)
                if not connect(consumer, store, topics, start_from, stop):
                    break
                continue
            stats["consumed"] += consumed
            stats["inserted"] += inserted
            stats["rejected"] += skipped + rejected
            stats["duplicates"] += len(rows) - inserted - rejected

        now = time.monotonic()
        if now - last_stats >= STATS_INTERVAL:
            logger.info("stats: " + " ".join(f"{k}={v}" for k, v in stats.items()))
            last_stats = now

    logger.info("stopped: " + " ".join(f"{k}={v}" for k, v in stats.items()))
    return 0


def main() -> int:
    level = os.environ.get("INGESTOR_LOG_LEVEL", "INFO").upper()
    logging.basicConfig(level=level, stream=sys.stdout,
                        format="%(asctime)s %(levelname)-8s %(name)-10s %(message)s",
                        datefmt="%Y-%m-%dT%H:%M:%S%z")

    topics = [t.strip() for t in os.environ.get("INGESTOR_TOPICS", "wazuh-alerts").split(",")
              if t.strip()]
    start_from = os.environ.get("INGESTOR_START_FROM", "beginning").lower()
    if start_from not in ("beginning", "end"):
        logger.critical("INGESTOR_START_FROM must be 'beginning' or 'end'")
        return 2
    try:
        batch = int(os.environ.get("INGESTOR_BATCH", "500"))
    except ValueError:
        batch = 0
    if batch <= 0:
        logger.critical("INGESTOR_BATCH must be a positive integer")
        return 2

    stop = threading.Event()

    def _stop(signum, _frame):
        logger.info("received %s; stopping after the current batch", signal.Signals(signum).name)
        stop.set()

    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, _stop)

    consumer = Consumer({
        "bootstrap.servers": os.environ.get("INGESTOR_BOOTSTRAP", "localhost:9092"),
        # Required by librdkafka, but unused: partitions are assigned directly
        # and offsets are stored in PostgreSQL, never committed to Kafka.
        "group.id": "shadowtwin-ingestor",
        "enable.auto.commit": False,
        "enable.auto.offset.store": False,
        "logger": logging.getLogger("librdkafka"),
    })
    store = Store(os.environ.get("INGESTOR_DSN",
                                 "postgresql://shadowtwin@localhost:5432/shadowtwin"))
    logger.info("ShadowTwin Ingestor: topics=%s batch=%d start_from=%s",
                ",".join(topics), batch, start_from)
    try:
        return run(consumer, store, topics, batch, start_from, stop)
    finally:
        consumer.close()
        store.close()


if __name__ == "__main__":
    sys.exit(main())
