"""Smoke tests for ShadowTwin Forwarder.

Runs on Linux (real inode semantics are required for the rotation tests)
with the project virtualenv:  make test  /  .venv/bin/python tests/smoke_test.py
"""
import json
import os
import sys
import tempfile
import threading
import time

PROJECT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, PROJECT)

FAILURES = []


def check(name, cond, extra=""):
    print(("PASS" if cond else "FAIL"), name, extra)
    if not cond:
        FAILURES.append(name)


# --- AckTracker --------------------------------------------------------------
from state import AckTracker, OffsetStore  # noqa: E402

t = AckTracker((10, 100))
s1 = t.track(10, 150)
s2 = t.track(10, 200)
s3 = t.track(10, 250)
t.ack(s2)
check("tracker holds on gap", t.committed == (10, 100))
t.ack(s1)
check("tracker advances contiguously", t.committed == (10, 200))
t.ack(s3)
check("tracker completes", t.committed == (10, 250) and t.in_flight == 0)

# --- OffsetStore -------------------------------------------------------------
d = tempfile.mkdtemp()
sp = os.path.join(d, "state.json")
st = OffsetStore(sp)
st.update("alerts", 42, 1234)
st.save()
check("state file written", os.path.exists(sp))
st2 = OffsetStore(sp)
check("state roundtrip", st2.get("alerts") == (42, 1234))
with open(sp, "w") as fh:
    fh.write("{broken")
st3 = OffsetStore(sp)
check("corrupt state quarantined",
      st3.get("alerts") is None and os.path.exists(sp + ".corrupt"))

# backup and automatic recovery
d2 = tempfile.mkdtemp()
sp2 = os.path.join(d2, "state.json")
stA = OffsetStore(sp2)
stA.update("alerts", 1, 100)
stA.save()
stA.update("alerts", 1, 200)
stA.save()
check("backup checkpoint written", os.path.exists(sp2 + ".bak"))
with open(sp2, "w") as fh:
    fh.write("garbage{{{")
stB = OffsetStore(sp2)
check("recovered offsets from backup", stB.get("alerts") == (1, 100))
check("corrupt main file quarantined", os.path.exists(sp2 + ".corrupt"))

# --- FileTail ----------------------------------------------------------------
from watcher import FileTail  # noqa: E402

wd = tempfile.mkdtemp()
p = os.path.join(wd, "alerts.json")
with open(p, "w") as fh:
    fh.write('{"a":1}\n{"a":2}\n')

ft = FileTail("alerts", p, start_from="beginning", saved=None)
lines = ft.poll(100)
check("reads existing lines", [ln[0] for ln in lines] == [b'{"a":1}', b'{"a":2}'])
first_inode = lines[0][1]

with open(p, "a") as fh:
    fh.write('{"a":3')          # half-written line
check("partial line withheld", ft.poll(100) == [])
with open(p, "a") as fh:
    fh.write('}\n')
lines = ft.poll(100)
check("partial line completed", [ln[0] for ln in lines] == [b'{"a":3}'])

os.rename(p, p + ".1")          # classic rotation
with open(p, "w") as fh:
    fh.write('{"b":1}\n')
lines = ft.poll(100)
check("rotation handled",
      [ln[0] for ln in lines] == [b'{"b":1}'] and lines and lines[0][1] != first_inode)

with open(p, "w") as fh:        # truncation (copytruncate style)
    pass
check("truncation detected quietly", ft.poll(100) == [])
with open(p, "a") as fh:
    fh.write('{"c":1}\n')
lines = ft.poll(100)
check("reads after truncation", [ln[0] for ln in lines] == [b'{"c":1}'])

os.unlink(p)                    # deletion + recreation
ft.poll(100)
check("closed after deletion", ft.position is None)
check("waits while missing", ft.poll(100) == [])
with open(p, "w") as fh:
    fh.write('{"d":1}\n')
lines = ft.poll(100)
check("re-attached after recreation", [ln[0] for ln in lines] == [b'{"d":1}'])
ft.close()

p2 = os.path.join(wd, "resume.json")
with open(p2, "w") as fh:
    fh.write('{"x":1}\n{"x":2}\n')
ft2 = FileTail("resume", p2, start_from="end",
               saved=(os.stat(p2).st_ino, len('{"x":1}\n')))
lines = ft2.poll(100)
check("resumes at saved offset", [ln[0] for ln in lines] == [b'{"x":2}'])
ft2.close()

p3 = os.path.join(wd, "tailend.json")
with open(p3, "w") as fh:
    fh.write('{"old":1}\n')
ft3 = FileTail("tailend", p3, start_from="end", saved=None)
check("start_from end skips history", ft3.poll(100) == [])
with open(p3, "a") as fh:
    fh.write('{"new":1}\n')
lines = ft3.poll(100)
check("start_from end sees new lines", [ln[0] for ln in lines] == [b'{"new":1}'])
ft3.close()

# --- config ------------------------------------------------------------------
from config import ConfigError, KafkaConfig, load_config  # noqa: E402

cfg = load_config(os.path.join(PROJECT, "config.yaml"))
check("config topics join",
      {(f.name, f.topic) for f in cfg.files}
      == {("alerts", "wazuh-alerts"), ("archives", "wazuh-logs")})
check("config broker", cfg.kafka.bootstrap_servers == ("localhost:9092",))

os.environ["SHADOWTWIN_KAFKA_BOOTSTRAP_SERVERS"] = "10.0.0.5:9092,10.0.0.6:9092"
cfg_env = load_config(os.path.join(PROJECT, "config.yaml"))
check("env override brokers",
      cfg_env.kafka.bootstrap_servers == ("10.0.0.5:9092", "10.0.0.6:9092"))
del os.environ["SHADOWTWIN_KAFKA_BOOTSTRAP_SERVERS"]

bad = os.path.join(wd, "bad.yaml")
with open(bad, "w") as fh:
    fh.write("kafka:\n  bootstrap_servers: [x:9092]\n"
             "topics: {}\nfiles:\n  alerts: /tmp/a\n")
try:
    load_config(bad)
    check("missing topic rejected", False)
except ConfigError:
    check("missing topic rejected", True)

# --- real librdkafka accepts our producer settings ----------------------------
import producer as producer_mod  # noqa: E402

kc = KafkaConfig(
    bootstrap_servers=("localhost:19092",), client_id="smoke", compression="lz4",
    linger_ms=50, batch_size=131072, message_timeout_ms=0, queue_max_messages=1000,
)
try:
    w = producer_mod.KafkaWriter(kc, on_ack=lambda n, s: None)
    check("librdkafka accepts producer config", True)
except Exception as exc:  # noqa: BLE001
    w = None
    check("librdkafka accepts producer config", False, repr(exc))

# reconnect accounting: broker-down transition, then a successful delivery
if w is not None:
    from confluent_kafka import KafkaError  # noqa: E402

    w._on_error(KafkaError(KafkaError._ALL_BROKERS_DOWN))
    check("broker-down flag set", w._broker_down and w.reconnects == 0)
    cb = w._delivery_callback("alerts", 0, 100)
    cb(None, None)
    check("reconnect counted on recovery",
          w.reconnects == 1 and not w._broker_down and w.acked == 1)
    del w

# --- end-to-end through app.run() with a fake producer -------------------------
class FakeProducer:
    instances = []

    def __init__(self, conf):
        self.sent = []
        self._cbs = []
        FakeProducer.instances.append(self)

    def produce(self, topic, value=None, on_delivery=None):
        self.sent.append((topic, value))
        self._cbs.append(on_delivery)

    def poll(self, timeout=0):
        cbs, self._cbs = self._cbs, []
        for cb in cbs:
            cb(None, None)
        return len(cbs)

    def flush(self, timeout=None):
        self.poll(0)
        return 0

    def __len__(self):
        return len(self._cbs)


producer_mod.Producer = FakeProducer

import app     # noqa: E402
import checks  # noqa: E402

e2e = tempfile.mkdtemp()
alerts = os.path.join(e2e, "alerts.json")
archives = os.path.join(e2e, "archives.json")
with open(alerts, "w") as fh:
    fh.write('{"n":1}\n{"n":2}\nnot json\n\n')
with open(archives, "w") as fh:
    fh.write('{"m":1}\n')

cfg_path = os.path.join(e2e, "config.yaml")
with open(cfg_path, "w") as fh:
    fh.write(f"""
kafka:
  bootstrap_servers: [localhost:19092]
topics:
  alerts: wazuh-alerts
  archives: wazuh-logs
files:
  alerts: {alerts}
  archives: {archives}
watcher:
  poll_interval: 0.05
  start_from: beginning
logging:
  level: ERROR
  file: {e2e}/fwd.log
  console: false
state:
  file: {e2e}/state.json
  save_interval: 0.1
""")
cfg2 = load_config(cfg_path)

# --- checks module against an unreachable broker (short timeout) ---------------
results = {r.name: r for r in checks.run_checks(cfg2, timeout=1.0)}
check("check: kafka unreachable detected", not results["Kafka"].ok)
check("check: topics not checked without broker", not results["Topics"].ok)
check("check: files readable", results["Files"].ok, results["Files"].detail)
check("check: permissions writable", results["Permissions"].ok)

# CLI helpers over stubbed check results
from checks import CheckResult  # noqa: E402

real_run_checks = checks.run_checks
checks.run_checks = lambda cfg, timeout=None: [CheckResult("Kafka", True, "stub")]
check("--validate-config exit 0 when all pass", app.run_checks_cli(cfg2, verbose=True) == 0)
checks.run_checks = lambda cfg, timeout=None: [CheckResult("Kafka", False, "stub")]
check("--health exit 1 on failure", app.run_checks_cli(cfg2, verbose=False) == 1)

# banner must not raise
app._log_banner(cfg2, cfg_path)
check("startup banner renders", True)

# keep the e2e run free of 5-second metadata probes
checks.run_checks = lambda cfg, timeout=None: []

stop = threading.Event()
rc = {}
thread = threading.Thread(target=lambda: rc.update(code=app.run(cfg2, stop)))
thread.start()
time.sleep(0.6)
with open(alerts, "a") as fh:
    fh.write('{"n":3}\n')
time.sleep(0.6)
stop.set()
thread.join(timeout=10)
check("run() exited cleanly", not thread.is_alive() and rc.get("code") == 0)

fp = FakeProducer.instances[-1]
by_topic = {}
for topic, value in fp.sent:
    by_topic.setdefault(topic, []).append(value)
check("alerts forwarded (malformed + blank skipped)",
      by_topic.get("wazuh-alerts") == [b'{"n":1}', b'{"n":2}', b'{"n":3}'])
check("archives forwarded", by_topic.get("wazuh-logs") == [b'{"m":1}'])

with open(os.path.join(e2e, "state.json")) as fh:
    saved = json.load(fh)["files"]
check("alerts offset checkpointed to EOF",
      saved["alerts"]["offset"] == os.path.getsize(alerts),
      f"saved={saved['alerts']['offset']} size={os.path.getsize(alerts)}")
check("archives offset checkpointed to EOF",
      saved["archives"]["offset"] == os.path.getsize(archives))

# restart: resumes from state, ships only the new line
with open(alerts, "a") as fh:
    fh.write('{"n":4}\n')
stop2 = threading.Event()
rc2 = {}
cfg3 = load_config(cfg_path)
thread2 = threading.Thread(target=lambda: rc2.update(code=app.run(cfg3, stop2)))
thread2.start()
time.sleep(0.6)
stop2.set()
thread2.join(timeout=10)
fp2 = FakeProducer.instances[-1]
check("restart resumes exactly where it stopped",
      [v for t2, v in fp2.sent if t2 == "wazuh-alerts"] == [b'{"n":4}'])
check("second run exited cleanly", rc2.get("code") == 0)

checks.run_checks = real_run_checks

print()
if FAILURES:
    print(f"{len(FAILURES)} FAILED: {FAILURES}")
    sys.exit(1)
print("ALL CHECKS PASSED")
