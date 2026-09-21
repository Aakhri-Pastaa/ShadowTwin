"""Synthetic Wazuh alert generator for the demo environment.

Writes NDJSON in the shape Wazuh's ``alerts.json`` / ``archives.json`` use, so
the forwarder runs against realistic input without a Wazuh installation.

Two things make this useful beyond producing traffic:

* every record carries a monotonic ``demo_seq``, which lets ``consumer.py``
  prove that nothing was lost end to end (its periodic ``[audit]`` line),
* the log is rotated and truncated on a schedule, exercising the two cases
  that break naive forwarders, live, while the demo is running.

Environment:
    DEMO_RATE          alerts per second (default 5)
    DEMO_ROTATE_EVERY  rename-rotate after this many alerts, 0 disables (default 120)
    DEMO_ROTATE_KEEP   rotated generations kept, like logrotate's `rotate N` (default 5)
    DEMO_TRUNCATE_EVERY truncate in place after this many alerts, 0 disables (default 300)
    DEMO_ALERTS_PATH   default /var/ossec/logs/alerts/alerts.json
    DEMO_ARCHIVES_PATH default /var/ossec/logs/archives/archives.json
    DEMO_SEQ_FILE      default /var/ossec/logs/.demo_seq

The sequence counter is persisted in the shared volume, so a restarted
generator continues numbering instead of starting again at 1 — which the
consumer's audit would otherwise read as thousands of duplicates.
"""

from __future__ import annotations

import json
import os
import random
import signal
import sys
import time
from datetime import UTC, datetime

RATE = float(os.environ.get("DEMO_RATE", "5"))
ROTATE_EVERY = int(os.environ.get("DEMO_ROTATE_EVERY", "120"))
TRUNCATE_EVERY = int(os.environ.get("DEMO_TRUNCATE_EVERY", "300"))
KEEP = max(1, int(os.environ.get("DEMO_ROTATE_KEEP", "5")))
ALERTS = os.environ.get("DEMO_ALERTS_PATH", "/var/ossec/logs/alerts/alerts.json")
ARCHIVES = os.environ.get("DEMO_ARCHIVES_PATH", "/var/ossec/logs/archives/archives.json")
SEQ_FILE = os.environ.get("DEMO_SEQ_FILE", "/var/ossec/logs/.demo_seq")

AGENTS = [
    ("001", "web-01", "10.20.0.11"),
    ("002", "db-01", "10.20.0.12"),
    ("003", "app-02", "10.20.0.13"),
    ("004", "jump-01", "10.20.0.14"),
]

# (level, rule_id, description, decoder, groups, mitre_id, mitre_tactic, mitre_technique)
RULES = [
    (5, "5716", "sshd: authentication failed.", "sshd",
     ["syslog", "sshd", "authentication_failed"],
     "T1110.001", "Credential Access", "Password Guessing"),
    (10, "5712", "sshd: brute force trying to get access to the system.", "sshd",
     ["syslog", "sshd", "authentication_failures"],
     "T1110.001", "Credential Access", "Password Guessing"),
    (3, "5501", "PAM: Login session opened.", "pam",
     ["pam", "syslog", "authentication_success"],
     "T1078", "Persistence", "Valid Accounts"),
    (7, "5402", "Successful sudo to ROOT executed.", "sudo",
     ["syslog", "sudo"],
     "T1548.003", "Privilege Escalation", "Sudo and Sudo Caching"),
    (7, "550", "Integrity checksum changed.", "syscheck_integrity_changed",
     ["ossec", "syscheck", "syscheck_entry_modified"],
     "T1565.001", "Impact", "Stored Data Manipulation"),
    (12, "554", "File added to the system.", "syscheck_new_entry",
     ["ossec", "syscheck", "syscheck_entry_added"],
     "T1105", "Command and Control", "Ingress Tool Transfer"),
    (6, "31151", "Multiple web server 400 error codes from same source ip.", "web-accesslog",
     ["web", "accesslog", "attack"],
     "T1595", "Reconnaissance", "Active Scanning"),
    (4, "2501", "syslog: User authentication failure.", "syslog",
     ["syslog", "access_control", "authentication_failed"],
     "T1110", "Credential Access", "Brute Force"),
]

USERS = ["admin", "root", "deploy", "svc_backup", "oracle", "postgres", "test"]


def now_iso() -> str:
    """Wazuh's alert timestamp format: ISO-8601 with milliseconds and offset."""
    return datetime.now(UTC).strftime("%Y-%m-%dT%H:%M:%S.%f")[:-3] + "+0000"


def make_alert(seq: int) -> dict:
    """Build one Wazuh-shaped alert carrying a monotonic demo_seq."""
    level, rule_id, desc, decoder, groups, mitre_id, tactic, technique = random.choice(RULES)
    agent_id, agent_name, agent_ip = random.choice(AGENTS)
    src_ip = f"203.0.113.{random.randint(2, 254)}"
    src_user = random.choice(USERS)
    src_port = random.randint(1024, 65535)
    ts = now_iso()

    return {
        "timestamp": ts,
        # Wazuh's own alert id is "<epoch>.<offset>"; demo_seq is ours.
        "id": f"{int(time.time())}.{seq}",
        "demo_seq": seq,
        "rule": {
            "level": level,
            "description": desc,
            "id": rule_id,
            "firedtimes": random.randint(1, 9),
            "mail": level >= 10,
            "groups": groups,
            "mitre": {"id": [mitre_id], "tactic": [tactic], "technique": [technique]},
        },
        "agent": {"id": agent_id, "name": agent_name, "ip": agent_ip},
        "manager": {"name": "wazuh-manager"},
        "decoder": {"name": decoder},
        "location": "/var/log/auth.log",
        "full_log": (
            f"{datetime.now().strftime('%b %d %H:%M:%S')} {agent_name} "
            f"sshd[{random.randint(400, 9999)}]: Failed password for invalid "
            f"user {src_user} from {src_ip} port {src_port} ssh2"
        ),
        "data": {"srcip": src_ip, "srcport": str(src_port), "srcuser": src_user},
    }


def write_line(path: str, payload: dict) -> None:
    """Append one NDJSON line, creating the file if it is missing."""
    with open(path, "a", encoding="utf-8") as fh:
        fh.write(json.dumps(payload, separators=(",", ":")) + "\n")


def load_seq(path: str) -> int:
    """Last sequence number written by a previous run, or 0."""
    try:
        with open(path, encoding="utf-8") as fh:
            return int(fh.read().strip() or 0)
    except (FileNotFoundError, ValueError):
        return 0


def save_seq(path: str, seq: int) -> None:
    """Persist the counter atomically (temp file + os.replace)."""
    tmp = path + ".tmp"
    with open(tmp, "w", encoding="utf-8") as fh:
        fh.write(str(seq))
    os.replace(tmp, path)


def rotate(path: str) -> None:
    """Rename-rotate like logrotate: .1 -> .2 ... -> .KEEP, then live -> .1.

    The path gets a new inode and any open handle goes stale — the case that
    silently kills a forwarder following a file descriptor. Keeping several
    generations means a forwarder that was down across more than one
    rotation can still find every file it missed.
    """
    for i in range(KEEP - 1, 0, -1):
        if os.path.exists(f"{path}.{i}"):
            os.replace(f"{path}.{i}", f"{path}.{i + 1}")
    try:
        os.replace(path, path + ".1")
    except FileNotFoundError:
        return
    open(path, "a", encoding="utf-8").close()
    print(f"[generator] rotated {path} -> {path}.1 (new inode)", flush=True)


def truncate(path: str) -> None:
    """copytruncate-style: same inode, size drops below the reader's offset."""
    try:
        with open(path, "w", encoding="utf-8"):
            pass
    except FileNotFoundError:
        return
    print(f"[generator] truncated {path} in place (same inode)", flush=True)


def main() -> int:
    for path in (ALERTS, ARCHIVES):
        os.makedirs(os.path.dirname(path), exist_ok=True)
        # World-readable: the forwarder container runs as a different uid.
        open(path, "a", encoding="utf-8").close()
        os.chmod(path, 0o644)

    running = True

    def stop(_signum, _frame):
        nonlocal running
        running = False

    signal.signal(signal.SIGINT, stop)
    signal.signal(signal.SIGTERM, stop)

    interval = 1.0 / RATE if RATE > 0 else 0.2
    print(f"[generator] {RATE}/s -> {ALERTS}", flush=True)
    print(f"[generator] rotate every {ROTATE_EVERY or 'never'}, "
          f"truncate every {TRUNCATE_EVERY or 'never'}", flush=True)

    seq = load_seq(SEQ_FILE)
    if seq:
        print(f"[generator] resuming after sequence {seq}", flush=True)
    while running:
        seq += 1
        alert = make_alert(seq)
        write_line(ALERTS, alert)
        # Saved after the write: a crash between the two re-uses one number,
        # which the audit counts as a duplicate. Saving first would instead
        # skip a number on crash, which the audit would report as a false gap.
        save_seq(SEQ_FILE, seq)
        # archives.json carries the unfiltered stream; one line in three here.
        if seq % 3 == 0:
            write_line(ARCHIVES, alert)

        if seq % 50 == 0:
            print(f"[generator] {seq} alerts written", flush=True)
        if ROTATE_EVERY and seq % ROTATE_EVERY == 0:
            rotate(ALERTS)
        if TRUNCATE_EVERY and seq % TRUNCATE_EVERY == 0:
            truncate(ARCHIVES)

        time.sleep(interval)

    print(f"[generator] stopped after {seq} alerts", flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
