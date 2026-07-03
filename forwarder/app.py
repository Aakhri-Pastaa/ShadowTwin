"""ShadowTwin Forwarder — entry point.

Tails Wazuh NDJSON log files and forwards every new line to Kafka:

* :class:`watcher.FileTail` follows each file like ``tail -F`` and yields
  complete lines,
* :class:`producer.KafkaWriter` publishes them (acks=all, lz4, retries),
* :class:`state.AckTracker` + :class:`state.OffsetStore` persist the highest
  fully-acknowledged offset so a restart resumes exactly where it stopped.

Run modes::

    python app.py --config config.yaml     forward events (the default)
    python app.py --validate-config        print every operational check, exit 0 if all pass
    python app.py --health                 same checks, terse output, for monitoring
"""

from __future__ import annotations

import argparse
import json
import logging
import os
import platform
import signal
import sys
import threading
import time

import checks
from config import AppConfig, ConfigError, load_config
from logger import setup_logging
from producer import KafkaWriter
from state import AckTracker, OffsetStore
from version import __version__
from watcher import FileTail

logger = logging.getLogger("app")

STATS_INTERVAL = 60.0  # seconds between throughput log lines
FLUSH_TIMEOUT = 30.0   # max seconds to wait for Kafka on shutdown

# Startup checks that may fail without preventing a start, and why. Anything
# not listed here (broken permissions) is fatal: an unwritable state
# directory would silently lose offsets.
_NON_FATAL_CHECKS = {
    "Kafka": "the producer buffers locally and reconnects forever",
    "Topics": "publishing begins once the topics exist",
    "Files": "the watcher attaches as soon as the file appears",
}


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    """Parse command-line arguments."""
    parser = argparse.ArgumentParser(
        prog="shadowtwin-forwarder",
        description="Forward Wazuh NDJSON log files to Kafka in real time.",
    )
    parser.add_argument(
        "--config",
        default=os.environ.get("SHADOWTWIN_CONFIG", "config.yaml"),
        help="path to config.yaml (default: $SHADOWTWIN_CONFIG or ./config.yaml)",
    )
    parser.add_argument(
        "--health",
        action="store_true",
        help="run operational checks and exit 0 when healthy (for monitoring)",
    )
    parser.add_argument(
        "--validate-config",
        dest="validate_config",
        action="store_true",
        help="print the result of every operational check and exit",
    )
    parser.add_argument("--version", action="version", version=f"%(prog)s {__version__}")
    return parser.parse_args(argv)


def install_signal_handlers(stop: threading.Event) -> None:
    """Turn SIGINT/SIGTERM into a request to shut down cleanly."""

    def _handle(signum: int, _frame) -> None:
        logger.info("received %s; shutting down", signal.Signals(signum).name)
        stop.set()

    for sig in (signal.SIGINT, signal.SIGTERM):
        signal.signal(sig, _handle)


def run_checks_cli(cfg: AppConfig, verbose: bool) -> int:
    """Back --health and --validate-config: print results, return exit code."""
    results = checks.run_checks(cfg)
    for result in results:
        if result.ok:
            print(f"{result.name} OK" + (f"  ({result.detail})" if verbose else ""))
        else:
            print(f"{result.name} FAIL - {result.detail}", file=sys.stderr)
    return 0 if all(r.ok for r in results) else 1


def _log_banner(cfg: AppConfig, config_path: str) -> None:
    """Emit the startup banner into the logs."""
    logger.info("=" * 56)
    logger.info("ShadowTwin Forwarder v%s", __version__)
    logger.info("Python:   %s", platform.python_version())
    logger.info("Kafka:    %s", ", ".join(cfg.kafka.bootstrap_servers))
    for wf in cfg.files:
        logger.info("Forward:  %s -> %s", wf.path, wf.topic)
    logger.info("Config:   %s", os.path.abspath(config_path))
    logger.info("State:    %s", os.path.abspath(cfg.state.file))
    logger.info("Log file: %s", os.path.abspath(cfg.logging.file))
    logger.info("=" * 56)


def _startup_validation(cfg: AppConfig) -> bool:
    """Log every operational check; returns True when a fatal one failed.

    Only broken permissions are fatal. An unreachable broker or a missing
    source file heals itself while the service keeps running (the producer
    retries forever, the watcher waits for files), so those are warnings.
    """
    fatal = False
    for result in checks.run_checks(cfg):
        if result.ok:
            logger.info("startup check: %-11s OK   (%s)", result.name, result.detail)
            continue
        reason = _NON_FATAL_CHECKS.get(result.name)
        if reason is None:
            logger.critical("startup check: %-11s FAIL (%s)", result.name, result.detail)
            fatal = True
        else:
            logger.warning("startup check: %-11s FAIL (%s) - continuing: %s",
                           result.name, result.detail, reason)
    return fatal


def _commit(store: OffsetStore, trackers: dict[str, AckTracker]) -> None:
    """Push every tracker's fully-acknowledged position into the store."""
    for name, tracker in trackers.items():
        inode, offset = tracker.committed
        if inode is not None:
            store.update(name, inode, offset)


def run(cfg: AppConfig, stop: threading.Event) -> int:
    """Main forwarding loop; returns a process exit code."""
    if _startup_validation(cfg):
        logger.critical("startup validation failed; fix the reported problem and restart")
        return 1

    store = OffsetStore(cfg.state.file)
    trackers: dict[str, AckTracker] = {}
    tails: list[tuple[FileTail, str]] = []

    for wf in cfg.files:
        tail = FileTail(wf.name, wf.path, cfg.watcher.start_from, store.get(wf.name))
        trackers[wf.name] = AckTracker(tail.position)
        tails.append((tail, wf.topic))
        logger.info("forwarding %s (%s) -> topic %s", wf.name, wf.path, wf.topic)

    def _settle(name: str, seq: int) -> None:
        trackers[name].ack(seq)

    writer = KafkaWriter(cfg.kafka, on_ack=_settle)

    _commit(store, trackers)
    store.save()

    parse_errors = 0
    last_save = time.monotonic()
    last_stats = time.monotonic()

    while not stop.is_set():
        busy = False
        for tail, topic in tails:
            tracker = trackers[tail.name]
            lines = tail.poll(cfg.watcher.batch_lines)
            if lines:
                busy = True
            for raw, inode, end_offset in lines:
                seq = tracker.track(inode, end_offset)
                if not raw.strip():
                    tracker.ack(seq)  # blank line: settle and move on
                    continue
                try:
                    json.loads(raw)
                except (ValueError, UnicodeDecodeError):
                    parse_errors += 1
                    logger.warning("%s: skipping malformed JSON line ending at "
                                   "offset %d: %.200r", tail.name, end_offset, raw)
                    tracker.ack(seq)
                    continue
                if not writer.send(topic, raw, tail.name, seq, end_offset, abort=stop):
                    break  # shutdown requested while blocked on a full queue
            if stop.is_set():
                break

        writer.poll(0)
        if writer.fatal:
            logger.critical("unrecoverable Kafka producer error; exiting so the "
                            "service manager can restart with a clean producer")
            break

        now = time.monotonic()
        if now - last_save >= cfg.state.save_interval:
            _commit(store, trackers)
            store.save()
            last_save = now
        if now - last_stats >= STATS_INTERVAL:
            in_flight = sum(t.in_flight for t in trackers.values())
            offsets = ", ".join(f"{name}={tracker.committed[1]}"
                                for name, tracker in trackers.items())
            logger.info("stats: produced=%d acked=%d failed=%d parse_errors=%d "
                        "reconnects=%d queued=%d in_flight=%d | offsets: %s",
                        writer.produced, writer.acked, writer.failed, parse_errors,
                        writer.reconnects, writer.queued, in_flight, offsets)
            last_stats = now

        if not busy:
            stop.wait(cfg.watcher.poll_interval)

    logger.info("flushing Kafka producer (up to %.0f s)...", FLUSH_TIMEOUT)
    remaining = writer.flush(FLUSH_TIMEOUT)
    if remaining:
        logger.warning("%d message(s) were not confirmed before shutdown; "
                       "they will be re-sent on the next start", remaining)
    _commit(store, trackers)
    store.save()
    for tail, _topic in tails:
        tail.close()
    logger.info("shutdown complete: produced=%d acked=%d failed=%d parse_errors=%d",
                writer.produced, writer.acked, writer.failed, parse_errors)
    return 1 if writer.fatal else 0


def main(argv: list[str] | None = None) -> int:
    """Load configuration, dispatch the requested mode, run the loop."""
    args = parse_args(argv)
    try:
        cfg = load_config(args.config)
    except ConfigError as exc:
        print(f"configuration error: {exc}", file=sys.stderr)
        return 2

    if args.health or args.validate_config:
        # Check modes stay print-only: they must not create or chown the
        # service's log files when run interactively (possibly as root).
        return run_checks_cli(cfg, verbose=args.validate_config)

    try:
        setup_logging(cfg.logging)
    except OSError as exc:
        print(f"cannot initialise logging ({cfg.logging.file}): {exc}", file=sys.stderr)
        return 2
    _log_banner(cfg, args.config)

    stop = threading.Event()
    install_signal_handlers(stop)
    try:
        return run(cfg, stop)
    except Exception:
        logger.exception("unhandled error; exiting")
        return 1


if __name__ == "__main__":
    sys.exit(main())
