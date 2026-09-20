"""Demo consumer: prints alerts arriving from Kafka and audits for gaps.

Each alert carries a monotonic ``demo_seq`` from ``alert_generator.py``. This
consumer tracks which sequences arrive, so the forwarder's delivery guarantee
becomes something you watch rather than something you take on trust:

* **gap** — a sequence that never arrived. At-least-once forbids this, so a
  gap means the guarantee was violated. Reported loudly.
* **duplicate** — a sequence seen more than once. Permitted, and expected
  after a crash or broker outage re-sends in-flight messages.

Kill the broker mid-run (``docker compose stop kafka``), wait, start it again,
and the audit line should still report zero gaps.

Environment:
    DEMO_BOOTSTRAP  Kafka bootstrap servers (default kafka:9092)
    DEMO_TOPICS     comma-separated topics (default wazuh-alerts)
    DEMO_GROUP      consumer group id (default shadowtwin-demo)
    DEMO_QUIET      1 to print only the periodic audit line
"""

from __future__ import annotations

import json
import os
import signal
import sys
import time

from confluent_kafka import Consumer, KafkaError

BOOTSTRAP = os.environ.get("DEMO_BOOTSTRAP", "kafka:9092")
TOPICS = [t.strip() for t in os.environ.get("DEMO_TOPICS", "wazuh-alerts").split(",") if t.strip()]
GROUP = os.environ.get("DEMO_GROUP", "shadowtwin-demo")
QUIET = os.environ.get("DEMO_QUIET", "0") == "1"

AUDIT_INTERVAL = 10.0  # seconds between audit lines

LEVEL_TAG = {range(0, 5): "low", range(5, 8): "med", range(8, 12): "HIGH", range(12, 17): "CRIT"}


def tag_for(level: int) -> str:
    for span, tag in LEVEL_TAG.items():
        if level in span:
            return tag
    return "?"


class SeqAudit:
    """Track which demo_seq values arrived; report gaps and duplicates.

    Sequences can legitimately arrive out of order across partitions, so a
    missing value is only a gap once a higher sequence has been seen. The
    'pending' count is how many are still legitimately outstanding.
    """

    def __init__(self) -> None:
        self.seen: set[int] = set()
        self.duplicates = 0
        self.highest = 0
        self.total = 0

    def record(self, seq: int) -> bool:
        """Returns False if this sequence is a duplicate."""
        self.total += 1
        if seq in self.seen:
            self.duplicates += 1
            return False
        self.seen.add(seq)
        self.highest = max(self.highest, seq)
        return True

    @property
    def gaps(self) -> list[int]:
        """Sequences below the high-water mark that never arrived."""
        if not self.seen:
            return []
        return sorted(set(range(1, self.highest + 1)) - self.seen)

    def line(self) -> str:
        gaps = self.gaps
        verdict = "NO GAPS" if not gaps else f"!! {len(gaps)} GAP(S): {gaps[:10]}"
        return (f"[audit] received={self.total} unique={len(self.seen)} "
                f"highest={self.highest} duplicates={self.duplicates} | {verdict}")


def main() -> int:
    consumer = Consumer({
        "bootstrap.servers": BOOTSTRAP,
        "group.id": GROUP,
        "auto.offset.reset": "earliest",
        # Commit only what we have actually processed, mirroring the
        # forwarder's discipline on the producing side.
        "enable.auto.commit": False,
    })
    consumer.subscribe(TOPICS)
    print(f"[consumer] {BOOTSTRAP} topics={','.join(TOPICS)} group={GROUP}", flush=True)

    audit = SeqAudit()
    running = True

    def stop(_signum, _frame):
        nonlocal running
        running = False

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)

    last_audit = time.monotonic()
    try:
        while running:
            msg = consumer.poll(1.0)

            now = time.monotonic()
            if now - last_audit >= AUDIT_INTERVAL:
                print(audit.line(), flush=True)
                last_audit = now

            if msg is None:
                continue
            if msg.error():
                if msg.error().code() != KafkaError._PARTITION_EOF:
                    print(f"[consumer] error: {msg.error()}", flush=True)
                continue

            try:
                alert = json.loads(msg.value())
            except (ValueError, UnicodeDecodeError):
                print("[consumer] skipping unparseable message", flush=True)
                consumer.commit(msg, asynchronous=False)
                continue

            seq = alert.get("demo_seq")
            fresh = audit.record(seq) if isinstance(seq, int) else True

            if not QUIET:
                rule = alert.get("rule", {})
                level = rule.get("level", 0)
                mitre = ", ".join(rule.get("mitre", {}).get("id", [])) or "-"
                marker = "" if fresh else "  [duplicate]"
                print(
                    f"#{seq:<6} {tag_for(level):<4} L{level:<2} "
                    f"{alert.get('agent', {}).get('name', '?'):<8} "
                    f"{rule.get('description', '?')[:52]:<52} {mitre}{marker}",
                    flush=True,
                )

            # Offset committed only after the record is processed.
            consumer.commit(msg, asynchronous=False)
    finally:
        print(audit.line(), flush=True)
        consumer.close()

    return 1 if audit.gaps else 0


if __name__ == "__main__":
    sys.exit(main())
