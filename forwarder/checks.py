"""Operational checks shared by --health, --validate-config and startup.

Nothing in this module raises or logs on a failed check — every probe
returns a :class:`CheckResult` and the caller decides what is fatal
(see ``app._startup_validation`` and ``app.run_checks_cli``).
"""

from __future__ import annotations

import os
import tempfile
from dataclasses import dataclass

from confluent_kafka.admin import AdminClient

from config import AppConfig

KAFKA_TIMEOUT = 5.0  # seconds to wait for cluster metadata


@dataclass(frozen=True)
class CheckResult:
    """Outcome of one operational check."""

    name: str
    ok: bool
    detail: str


def run_checks(cfg: AppConfig, timeout: float = KAFKA_TIMEOUT) -> list[CheckResult]:
    """Run every check; returns results named Kafka, Topics, Files, Permissions."""
    results = check_kafka(cfg, timeout)
    results.append(_aggregate("Files", check_files(cfg)))
    results.append(_aggregate("Permissions", check_permissions(cfg)))
    return results


def check_kafka(cfg: AppConfig, timeout: float = KAFKA_TIMEOUT) -> list[CheckResult]:
    """Probe broker reachability and topic existence with a metadata request.

    Uses an AdminClient so the probe cannot trigger broker-side topic
    auto-creation the way a producer metadata request can.
    """
    brokers = ",".join(cfg.kafka.bootstrap_servers)
    wanted = sorted({wf.topic for wf in cfg.files})
    try:
        admin = AdminClient(
            {
                "bootstrap.servers": brokers,
                "client.id": f"{cfg.kafka.client_id}-check",
                "log_level": 0,  # keep probe noise out of stderr
            }
        )
        metadata = admin.list_topics(timeout=timeout)
    except Exception as exc:  # noqa: BLE001 - report any client failure as a result
        return [
            CheckResult("Kafka", False, f"no broker reachable at {brokers}: {exc}"),
            CheckResult("Topics", False, "not checked (broker unreachable)"),
        ]
    results = [
        CheckResult("Kafka", True,
                    f"connected to {brokers}; {len(metadata.brokers)} broker(s) in cluster")
    ]
    missing = [topic for topic in wanted if topic not in metadata.topics]
    if missing:
        results.append(CheckResult(
            "Topics", False,
            f"missing on the broker: {', '.join(missing)} "
            "(create them, or enable topic auto-creation)",
        ))
    else:
        results.append(CheckResult("Topics", True, f"{', '.join(wanted)} exist"))
    return results


def check_files(cfg: AppConfig) -> list[CheckResult]:
    """Verify every watched file exists and is readable by this user."""
    results = []
    for wf in cfg.files:
        try:
            with open(wf.path, "rb") as fh:
                fh.read(1)
            results.append(CheckResult(f"file:{wf.name}", True, f"{wf.path} readable"))
        except FileNotFoundError:
            results.append(CheckResult(
                f"file:{wf.name}", False,
                f"{wf.path} does not exist yet (the forwarder waits for it)",
            ))
        except OSError as exc:
            results.append(CheckResult(f"file:{wf.name}", False, f"cannot read {wf.path}: {exc}"))
    return results


def check_permissions(cfg: AppConfig) -> list[CheckResult]:
    """Verify the state and log directories are writable (probe file)."""
    results = []
    seen: set[str] = set()
    for label, target in (("state dir", cfg.state.file), ("log dir", cfg.logging.file)):
        directory = os.path.dirname(os.path.abspath(target)) or "."
        if directory in seen:
            continue
        seen.add(directory)
        try:
            os.makedirs(directory, exist_ok=True)
            with tempfile.NamedTemporaryFile(dir=directory, prefix=".probe-"):
                pass
            results.append(CheckResult(label, True, f"{directory} writable"))
        except OSError as exc:
            results.append(CheckResult(label, False, f"{directory} not writable: {exc}"))
    return results


def _aggregate(name: str, parts: list[CheckResult]) -> CheckResult:
    """Collapse per-item results into one named result for display."""
    failed = [r for r in parts if not r.ok]
    if failed:
        return CheckResult(name, False, "; ".join(r.detail for r in failed))
    return CheckResult(name, True, "; ".join(r.detail for r in parts))
