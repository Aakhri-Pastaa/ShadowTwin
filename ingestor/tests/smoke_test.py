"""Smoke tests for the ShadowTwin Ingestor.

Normalisation tests always run. Database and pipeline tests need a real
PostgreSQL and run only when INGESTOR_TEST_DSN is set — it must point at a
THROWAWAY database: these tests drop and recreate the ingestor's tables.

    INGESTOR_TEST_DSN=postgresql://user:pass@localhost:5432/scratch python tests/smoke_test.py

Kafka is replaced by an in-memory fake that honours assign(), offsets and
consume(), so every delivery scenario is deterministic.
"""
import json
import logging
import os
import sys
import threading
import time
from types import SimpleNamespace

PROJECT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, PROJECT)

FAILURES = []


def check(name, cond, extra=""):
    print(("PASS" if cond else "FAIL"), name, extra)
    if not cond:
        FAILURES.append(name)


def alert(n, **override):
    """A Wazuh-shaped alert with a unique id."""
    doc = {
        "timestamp": "2026-09-21T16:12:21.123+0000",
        "id": f"1758471141.{n}",
        "rule": {"level": 7, "description": "Successful sudo to ROOT executed.", "id": "5402",
                 "groups": ["syslog", "sudo"],
                 "mitre": {"id": ["T1548.003"], "tactic": ["Privilege Escalation"],
                           "technique": ["Sudo and Sudo Caching"]}},
        "agent": {"id": "001", "name": "web-01", "ip": "10.20.0.11"},
        "manager": {"name": "wazuh-manager"},
        "decoder": {"name": "sudo"},
        "location": "/var/log/auth.log",
        "full_log": "sudo: deploy : TTY=pts/0 ; COMMAND=/bin/bash",
        "data": {"srcuser": "deploy", "srcip": "203.0.113.9"},
    }
    doc.update(override)
    return json.dumps(doc, separators=(",", ":")).encode()


# --- normalisation -------------------------------------------------------------
from normalize import normalize, parse_timestamp  # noqa: E402

row = normalize(alert(1), "wazuh-alerts", 0, 41)
check("full alert: identity",
      row["manager"] == "wazuh-manager" and row["alert_id"] == "1758471141.1")
check("full alert: rule", (row["rule_id"], row["rule_level"], row["rule_groups"])
      == ("5402", 7, ["syslog", "sudo"]))
check("full alert: ATT&CK", row["mitre_ids"] == ["T1548.003"]
      and row["mitre_tactics"] == ["Privilege Escalation"])
check("full alert: agent and source", (row["agent_name"], row["src_ip"], row["src_user"])
      == ("web-01", "203.0.113.9", "deploy"))
check("full alert: provenance", (row["source_topic"], row["source_partition"],
                                 row["source_offset"]) == ("wazuh-alerts", 0, 41))
check("full alert: raw keeps everything", row["raw"]["full_log"].startswith("sudo:"))

ts = parse_timestamp("2026-09-21T16:12:21.123+0000")
check("timestamp: Wazuh format is timezone-aware UTC",
      ts is not None and ts.utcoffset().total_seconds() == 0 and ts.microsecond == 123000)
check("timestamp: without milliseconds", parse_timestamp("2026-09-21T16:12:21+0000") is not None)
check("timestamp: garbage and naive values are rejected, never guessed",
      parse_timestamp("yesterday") is None and parse_timestamp("2026-09-21T16:12:21") is None
      and parse_timestamp(1758471141) is None)

sparse = normalize(b'{"id":"9.9","timestamp":"2026-09-21T16:12:21.1+0000"}', "t", 0, 0)
check("sparse alert still lands, NULLs and empty arrays",
      sparse is not None and sparse["rule_level"] is None and sparse["mitre_ids"] == []
      and sparse["manager"] == "")

no_id = b'{"rule":{"level":3}}'
a1, a2 = normalize(no_id, "t", 0, 0), normalize(no_id, "t", 0, 1)
check("missing id: content hash is the identity, stable across re-delivery",
      a1["alert_id"].startswith("sha256:") and a1["alert_id"] == a2["alert_id"])

check("non-JSON and non-object messages are rejected",
      normalize(b"not json", "t", 0, 0) is None and normalize(b"[1,2]", "t", 0, 0) is None
      and normalize(b"\xff\xfe", "t", 0, 0) is None)

check("rule level outside smallint or not a number becomes NULL",
      normalize(alert(2, rule={"level": 99999}), "t", 0, 0)["rule_level"] is None
      and normalize(alert(3, rule={"level": "high"}), "t", 0, 0)["rule_level"] is None)

nul = normalize(alert(4, full_log="a\x00b"), "t", 0, 0)
check("U+0000 replaced (PostgreSQL cannot store it)", nul["raw"]["full_log"] == "a\ufffdb")
literal = normalize(alert(5, full_log="path\\u0000literal"), "t", 0, 0)
check("a literal backslash-u0000 in text is left alone",
      literal["raw"]["full_log"] == "path\\u0000literal")

# --- database and pipeline -------------------------------------------------------
DSN = os.environ.get("INGESTOR_TEST_DSN")
if not DSN:
    print("SKIP database and pipeline tests: set INGESTOR_TEST_DSN to a throwaway database")
else:
    import psycopg  # noqa: E402
    from confluent_kafka import OFFSET_BEGINNING, OFFSET_END  # noqa: E402

    import app  # noqa: E402
    from store import Store  # noqa: E402

    def fresh_store():
        with psycopg.connect(DSN, autocommit=True) as conn:
            conn.execute("DROP TABLE IF EXISTS findings, ingest_offsets")
        s = Store(DSN)
        s.connect()
        return s

    def count(s, sql="SELECT count(*) FROM findings"):
        return s._conn.execute(sql).fetchone()[0]

    # -- store ------------------------------------------------------------------
    st = fresh_store()
    st.connect()                                            # schema applied twice
    check("schema is idempotent across reconnects", count(st) == 0)

    rows = [normalize(alert(i), "wazuh-alerts", 0, i) for i in range(3)]
    check("write: rows inserted", st.write(rows, {("wazuh-alerts", 0): 3}) == (3, 0))
    check("write: offsets committed with them", st.load_offsets() == {("wazuh-alerts", 0): 3})
    check("write: re-delivered duplicates are no-ops",
          st.write(rows, {("wazuh-alerts", 0): 3}) == (0, 0) and count(st) == 3)

    try:
        st.write([normalize(alert(10), "wazuh-alerts", 0, 10)], {("wazuh-alerts", 0): None})
        check("atomic: failing offset write rolls back the rows", False)
    except psycopg.Error:
        check("atomic: failing offset write rolls back the rows",
              count(st) == 3 and st.load_offsets() == {("wazuh-alerts", 0): 3})

    poison = normalize(alert(21), "wazuh-alerts", 0, 21)
    poison["rule_description"] = "bad \ud800 surrogate"     # cannot be encoded to UTF-8
    mixed = [normalize(alert(20), "wazuh-alerts", 0, 20), poison,
             normalize(alert(22), "wazuh-alerts", 0, 22)]
    check("poison row: the rest of the batch still lands",
          st.write(mixed, {("wazuh-alerts", 0): 23}) == (2, 1) and count(st) == 5)
    check("poison row: offsets advance past it, so it cannot wedge the pipeline",
          st.load_offsets() == {("wazuh-alerts", 0): 23})
    st.close()

    # -- pipeline with a fake Kafka ------------------------------------------------
    class FakeMsg:
        def __init__(self, topic, partition, offset, value):
            self._t, self._p, self._o, self._v = topic, partition, offset, value

        def error(self):
            return None

        def topic(self):
            return self._t

        def partition(self):
            return self._p

        def offset(self):
            return self._o

        def value(self):
            return self._v

    class FakeConsumer:
        """Honours assign() and offsets; serves each partition's log in order."""

        def __init__(self, log):
            self.log, self.pos = log, {}

        def list_topics(self, timeout=None):
            topics = {}
            for topic, partition in self.log:
                meta = topics.setdefault(topic, SimpleNamespace(partitions={}, error=None))
                meta.partitions[partition] = None
            return SimpleNamespace(topics=topics)

        def assign(self, tps):
            self.pos = {}
            for tp in tps:
                size = len(self.log[(tp.topic, tp.partition)])
                self.pos[(tp.topic, tp.partition)] = {OFFSET_BEGINNING: 0,
                                                      OFFSET_END: size}.get(tp.offset, tp.offset)

        def consume(self, num_messages=1, timeout=0):
            out = []
            for key, i in self.pos.items():
                msgs = self.log[key]
                while i < len(msgs) and len(out) < num_messages:
                    out.append(FakeMsg(key[0], key[1], i, msgs[i]))
                    i += 1
                self.pos[key] = i
            if not out:
                time.sleep(0.01)
            return out

    def run_until(consumer, store, done, batch=7, start_from="beginning", timeout=10):
        stop = threading.Event()
        result = {}
        th = threading.Thread(target=lambda: result.update(code=app.run(
            consumer, store, ["wazuh-alerts"], batch, start_from, stop)))
        th.start()
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline and not done():
            time.sleep(0.05)
        stop.set()
        th.join(timeout=10)
        return result.get("code")

    class _Capture(logging.Handler):
        def __init__(self):
            super().__init__()
            self.messages = []

        def emit(self, record):
            self.messages.append(record.getMessage())

    cap = _Capture()
    logging.getLogger("ingestor").addHandler(cap)
    logging.getLogger("ingestor").setLevel(logging.INFO)

    # 40 alerts, two of them repeated (forwarder at-least-once), two junk lines.
    log = [alert(i) for i in range(40)]
    log.insert(10, alert(3))
    log.insert(25, alert(17))
    log.insert(30, b"not json")
    log.insert(31, b"[]")
    topic_log = {("wazuh-alerts", 0): log}

    st = fresh_store()
    watcher = Store(DSN)
    watcher.connect()
    code = run_until(FakeConsumer(topic_log), st,
                     lambda: watcher.load_offsets().get(("wazuh-alerts", 0)) == len(log))
    check("pipeline: exits cleanly", code == 0)
    check("pipeline: every distinct alert stored once", count(watcher) == 40)
    check("pipeline: offsets cover every message, junk included",
          watcher.load_offsets() == {("wazuh-alerts", 0): len(log)})

    # Restart: a new consumer resumes from PostgreSQL's offset, not from zero.
    log.extend(alert(i) for i in range(40, 45))
    fresh_consumer = FakeConsumer(topic_log)
    run_until(fresh_consumer, st,
              lambda: watcher.load_offsets().get(("wazuh-alerts", 0)) == len(log))
    check("restart: resumes from the stored offset", count(watcher) == 45)
    check("restart: assigned at the stored offset, not the beginning",
          any("from offset 44" in m for m in cap.messages))

    # PostgreSQL drops mid-stream: the batch in hand is rewound, not lost.
    class FlakyStore(Store):
        writes = 0

        def write(self, rows, offsets):
            FlakyStore.writes += 1
            if FlakyStore.writes == 3:
                raise psycopg.OperationalError("simulated connection loss")
            return super().write(rows, offsets)

    flaky_log = {("wazuh-alerts", 0): [alert(i) for i in range(100, 150)]}
    fresh_store().close()
    flaky = FlakyStore(DSN)
    cap.messages.clear()
    run_until(FakeConsumer(flaky_log), flaky,
              lambda: watcher.load_offsets().get(("wazuh-alerts", 0)) == 50)
    check("database outage: rewound to the stored offsets",
          any("rewinding" in m for m in cap.messages))
    check("database outage: every alert stored exactly once afterwards",
          count(watcher) == 50 and count(
              watcher, "SELECT count(DISTINCT alert_id) FROM findings") == 50)

    # First run with start_from=end ignores what is already in the topic.
    end_log = {("wazuh-alerts", 0): [alert(i) for i in range(200, 205)]}
    fresh_store().close()
    end_store = Store(DSN)
    end_consumer = FakeConsumer(end_log)
    stop_end = threading.Event()
    th = threading.Thread(target=lambda: app.run(end_consumer, end_store, ["wazuh-alerts"],
                                                 7, "end", stop_end))
    th.start()
    time.sleep(0.5)
    end_log[("wazuh-alerts", 0)].extend(alert(i) for i in range(205, 208))
    deadline = time.monotonic() + 10
    while time.monotonic() < deadline and count(watcher) < 3:
        time.sleep(0.05)
    stop_end.set()
    th.join(timeout=10)
    check("start_from=end: only alerts written after start", count(watcher) == 3
          and count(watcher, "SELECT min(source_offset) FROM findings") == 5)

    watcher.close()
    st.close()

print()
if FAILURES:
    print(f"{len(FAILURES)} FAILED: {FAILURES}")
    sys.exit(1)
print("ALL CHECKS PASSED")
