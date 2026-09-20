"""Integration test for the demo environment, without Docker.

Runs the real forwarder (`app.run`) against the real generator's output with a
fake Kafka producer, then feeds what was produced into the real consumer's
`SeqAudit`. This proves the thing the demo claims on screen — that nothing is
lost across a rotation and a truncation — on a machine with no container
runtime, and in CI.

What it does NOT cover: the compose file, image builds, and broker behaviour.
Those need `docker compose up`.

    python tests/demo_test.py
"""
import json
import os
import shutil
import sys
import tempfile
import threading
import time

DEMO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
FORWARDER = os.path.join(os.path.dirname(DEMO), "forwarder")
sys.path.insert(0, DEMO)
sys.path.insert(0, FORWARDER)

FAILURES = []


def check(name, cond, extra=""):
    print(("PASS" if cond else "FAIL"), name, extra)
    if not cond:
        FAILURES.append(name)


# --- generator produces Wazuh-shaped NDJSON ---------------------------------
import alert_generator as gen  # noqa: E402

a = gen.make_alert(1)
check("alert has monotonic demo_seq", a["demo_seq"] == 1)
check("alert is JSON-serialisable", isinstance(json.dumps(a), str))
for field in ("timestamp", "id", "rule", "agent", "manager", "decoder", "full_log", "data"):
    check(f"alert has {field}", field in a)
check("rule carries level/id/description",
      all(k in a["rule"] for k in ("level", "id", "description")))
check("rule carries MITRE mapping",
      a["rule"]["mitre"]["id"] and a["rule"]["mitre"]["tactic"])
check("timestamp matches Wazuh format",
      a["timestamp"].endswith("+0000") and "T" in a["timestamp"] and
      len(a["timestamp"].split(".")[1]) == 8)
check("sequences increment", gen.make_alert(7)["demo_seq"] == 7)

# --- consumer's gap audit behaves ------------------------------------------
from consumer import SeqAudit  # noqa: E402

au = SeqAudit()
for s in (1, 2, 3):
    au.record(s)
check("contiguous run reports no gaps", au.gaps == [])
au.record(5)
check("missing sequence detected as gap", au.gaps == [4])
au.record(4)
check("gap closes when it arrives", au.gaps == [])
fresh = au.record(3)
check("duplicate detected, not counted as new", fresh is False and au.duplicates == 1)
check("duplicates do not create gaps", au.gaps == [])

# --- end-to-end: generator -> forwarder -> audit, across a rotation ---------
import producer as producer_mod  # noqa: E402


class FakeProducer:
    """Stand-in for confluent_kafka.Producer; acks everything on poll()."""
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

import app  # noqa: E402
import checks  # noqa: E402
from checks import CheckResult  # noqa: E402
from config import load_config  # noqa: E402

# The forwarder's startup probes Kafka; stub them so no broker is needed.
checks.run_checks = lambda cfg, timeout=5.0: [
    CheckResult("Kafka", True, "stub"), CheckResult("Topics", True, "stub"),
    CheckResult("Files", True, "stub"), CheckResult("Permissions", True, "stub"),
]

work = tempfile.mkdtemp()
alerts = os.path.join(work, "logs", "alerts", "alerts.json")
archives = os.path.join(work, "logs", "archives", "archives.json")
os.makedirs(os.path.dirname(alerts))
os.makedirs(os.path.dirname(archives))

gen.ALERTS, gen.ARCHIVES = alerts, archives
for p in (alerts, archives):
    open(p, "a").close()

cfg_path = os.path.join(work, "config.yaml")
with open(cfg_path, "w", encoding="utf-8") as fh:
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
  file: {work}/fwd.log
  console: false
state:
  file: {work}/state.json
  save_interval: 0.1
""")

TOTAL = 60
ROTATE_AT = 25
TRUNCATE_AT = 45

# Write the first records before the forwarder starts: start_from=beginning
# must pick them up, or the demo would under-report from the first second.
for seq in range(1, 6):
    gen.write_line(alerts, gen.make_alert(seq))

cfg = load_config(cfg_path)
stop = threading.Event()
rc = {}
thread = threading.Thread(target=lambda: rc.update(code=app.run(cfg, stop)))
thread.start()
time.sleep(0.3)

for seq in range(6, TOTAL + 1):
    gen.write_line(alerts, gen.make_alert(seq))
    if seq == ROTATE_AT:
        time.sleep(0.25)          # let the tailer reach EOF first
        gen.rotate(alerts)        # rename rotation: new inode
    if seq == TRUNCATE_AT:
        time.sleep(0.25)
        gen.truncate(alerts)      # copytruncate: same inode, size resets
    time.sleep(0.02)

time.sleep(1.0)
stop.set()
thread.join(timeout=15)
check("forwarder exited cleanly", rc.get("code") == 0)

produced = [v for inst in FakeProducer.instances for t, v in inst.sent if t == "wazuh-alerts"]
audit = SeqAudit()
for raw in produced:
    audit.record(json.loads(raw)["demo_seq"])

check("every generated alert was forwarded",
      audit.highest == TOTAL, f"highest={audit.highest} expected={TOTAL}")
check("NO GAPS across rotation and truncation",
      audit.gaps == [], f"gaps={audit.gaps[:20]}")
check("all forwarded lines are valid JSON", len(produced) == audit.total)
check("audit line renders", "NO GAPS" in audit.line())

shutil.rmtree(work, ignore_errors=True)

print()
if FAILURES:
    print(f"{len(FAILURES)} FAILED: {FAILURES}")
    sys.exit(1)
print("ALL CHECKS PASSED")
