"""Configuration loading for ShadowTwin Forwarder.

All runtime settings live in a single YAML file (see ``config.yaml``).
A few settings can be overridden through environment variables, which is
convenient for container deployments:

    SHADOWTWIN_KAFKA_BOOTSTRAP_SERVERS  comma-separated broker list
    SHADOWTWIN_LOG_LEVEL                log level name (DEBUG..CRITICAL)
    SHADOWTWIN_STATE_FILE               path of the offset state file
"""

from __future__ import annotations

import os
from dataclasses import dataclass
from typing import Any

import yaml

_LOG_LEVELS = ("CRITICAL", "ERROR", "WARNING", "INFO", "DEBUG")


class ConfigError(Exception):
    """The configuration file is missing, unreadable or invalid."""


@dataclass(frozen=True)
class KafkaConfig:
    """Producer connection and batching settings."""

    bootstrap_servers: tuple[str, ...]
    client_id: str
    compression: str
    linger_ms: int
    batch_size: int
    message_timeout_ms: int
    queue_max_messages: int


@dataclass(frozen=True)
class WatchedFile:
    """One NDJSON file and the Kafka topic its lines are published to."""

    name: str
    path: str
    topic: str


@dataclass(frozen=True)
class WatcherConfig:
    """File-following behaviour."""

    poll_interval: float
    batch_lines: int
    start_from: str  # "end" or "beginning"; applies only when no state exists


@dataclass(frozen=True)
class LoggingConfig:
    """Application log destination and rotation."""

    level: str
    file: str
    max_bytes: int
    backup_count: int
    console: bool


@dataclass(frozen=True)
class StateConfig:
    """Offset persistence."""

    file: str
    save_interval: float


@dataclass(frozen=True)
class AppConfig:
    """Fully validated application configuration."""

    kafka: KafkaConfig
    files: tuple[WatchedFile, ...]
    watcher: WatcherConfig
    logging: LoggingConfig
    state: StateConfig


def _mapping(data: dict[str, Any], key: str, required: bool = True) -> dict[str, Any]:
    """Return ``data[key]`` as a dict, or an empty dict for optional sections."""
    value = data.get(key)
    if value is None:
        if required:
            raise ConfigError(f"missing required section '{key}'")
        return {}
    if not isinstance(value, dict):
        raise ConfigError(f"section '{key}' must be a mapping")
    return value


def _bootstrap_servers(kafka: dict[str, Any]) -> tuple[str, ...]:
    """Resolve the broker list from the environment or the kafka section."""
    override = os.environ.get("SHADOWTWIN_KAFKA_BOOTSTRAP_SERVERS")
    if override:
        servers = tuple(part.strip() for part in override.split(",") if part.strip())
        if servers:
            return servers
        raise ConfigError("SHADOWTWIN_KAFKA_BOOTSTRAP_SERVERS is set but empty")
    raw = kafka.get("bootstrap_servers")
    if not isinstance(raw, list) or not raw or not all(isinstance(s, str) and s for s in raw):
        raise ConfigError("kafka.bootstrap_servers must be a non-empty list of host:port strings")
    return tuple(raw)


def _watched_files(files: dict[str, Any], topics: dict[str, Any]) -> tuple[WatchedFile, ...]:
    """Join the 'files' and 'topics' sections on their shared logical names."""
    watched: list[WatchedFile] = []
    for name, path in files.items():
        if not isinstance(path, str) or not path:
            raise ConfigError(f"files.{name} must be a filesystem path string")
        topic = topics.get(name)
        if not isinstance(topic, str) or not topic:
            raise ConfigError(f"files.{name} has no matching topic under 'topics'")
        watched.append(WatchedFile(name=str(name), path=path, topic=topic))
    if not watched:
        raise ConfigError("no files configured under 'files'")
    return tuple(watched)


def load_config(path: str) -> AppConfig:
    """Read *path* as YAML, apply environment overrides, validate and return."""
    try:
        with open(path, encoding="utf-8") as fh:
            data = yaml.safe_load(fh)
    except FileNotFoundError:
        raise ConfigError(f"config file not found: {path}") from None
    except OSError as exc:
        raise ConfigError(f"cannot read {path}: {exc}") from None
    except yaml.YAMLError as exc:
        raise ConfigError(f"invalid YAML in {path}: {exc}") from None
    if not isinstance(data, dict):
        raise ConfigError(f"{path}: top level must be a mapping")

    kafka = _mapping(data, "kafka")
    topics = _mapping(data, "topics")
    files = _mapping(data, "files")
    watcher = _mapping(data, "watcher", required=False)
    log_cfg = _mapping(data, "logging", required=False)
    state = _mapping(data, "state", required=False)

    level = (os.environ.get("SHADOWTWIN_LOG_LEVEL") or str(log_cfg.get("level", "INFO"))).upper()
    if level not in _LOG_LEVELS:
        raise ConfigError(f"logging.level must be one of {', '.join(_LOG_LEVELS)}")

    start_from = str(watcher.get("start_from", "end")).lower()
    if start_from not in ("end", "beginning"):
        raise ConfigError("watcher.start_from must be 'end' or 'beginning'")

    try:
        poll_interval = float(watcher.get("poll_interval", 0.25))
        batch_lines = int(watcher.get("batch_lines", 500))
        save_interval = float(state.get("save_interval", 2.0))
        kafka_cfg = KafkaConfig(
            bootstrap_servers=_bootstrap_servers(kafka),
            client_id=str(kafka.get("client_id", "shadowtwin-forwarder")),
            compression=str(kafka.get("compression", "lz4")),
            linger_ms=int(kafka.get("linger_ms", 50)),
            batch_size=int(kafka.get("batch_size", 131072)),
            message_timeout_ms=int(kafka.get("message_timeout_ms", 0)),
            queue_max_messages=int(kafka.get("queue_max_messages", 100_000)),
        )
        logging_cfg = LoggingConfig(
            level=level,
            file=str(log_cfg.get("file", "shadowtwin-forwarder.log")),
            max_bytes=int(log_cfg.get("max_bytes", 10 * 1024 * 1024)),
            backup_count=int(log_cfg.get("backup_count", 5)),
            console=bool(log_cfg.get("console", True)),
        )
        state_cfg = StateConfig(
            file=os.environ.get("SHADOWTWIN_STATE_FILE") or str(state.get("file", "state.json")),
            save_interval=save_interval,
        )
    except (TypeError, ValueError) as exc:
        raise ConfigError(f"invalid value in {path}: {exc}") from None

    if poll_interval <= 0:
        raise ConfigError("watcher.poll_interval must be > 0")
    if batch_lines <= 0:
        raise ConfigError("watcher.batch_lines must be > 0")

    return AppConfig(
        kafka=kafka_cfg,
        files=_watched_files(files, topics),
        watcher=WatcherConfig(
            poll_interval=poll_interval,
            batch_lines=batch_lines,
            start_from=start_from,
        ),
        logging=logging_cfg,
        state=state_cfg,
    )
