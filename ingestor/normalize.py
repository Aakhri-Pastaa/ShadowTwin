"""Wazuh alert JSON -> one findings row.

The row is the ingestion slice of the Finding object designed in
``docs/archive/architecture-v2.md``: the fields every downstream question
starts from (when, which rule, how severe, which ATT&CK techniques, which
agent, from where) as typed columns, plus the full alert as ``raw`` so nothing
Wazuh emitted is lost.

Nothing here touches the network or the database, so it is tested directly.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime
from typing import Any

# Order matches the INSERT in store.py.
COLUMNS = (
    "manager", "alert_id", "ts", "rule_id", "rule_level", "rule_description",
    "rule_groups", "mitre_ids", "mitre_tactics", "agent_id", "agent_name",
    "agent_ip", "decoder", "location", "src_ip", "src_user", "raw",
    "source_topic", "source_partition", "source_offset",
)

_SMALLINT_MAX = 32767


def normalize(raw: bytes, topic: str, partition: int, offset: int) -> dict[str, Any] | None:
    """Build a findings row from one Kafka message, or None if it is not an alert.

    Tolerant of missing fields: a real alert with an unexpected shape still
    lands, with NULLs where fields are absent and the whole object in ``raw``.
    """
    try:
        alert = json.loads(raw)
    except (ValueError, UnicodeDecodeError):
        return None
    if not isinstance(alert, dict):
        return None
    # PostgreSQL's text and jsonb types cannot store U+0000, which would fail
    # the insert. JSON can only carry it as an escape, so check the bytes
    # cheaply and clean the parsed strings only when one might be present.
    if b"\\u0000" in raw:
        alert = _without_nul(alert)

    rule = _dict(alert.get("rule"))
    mitre = _dict(rule.get("mitre"))
    agent = _dict(alert.get("agent"))
    data = _dict(alert.get("data"))

    alert_id = _text(alert.get("id"))
    if alert_id is None:
        # Wazuh always sets "id"; without one, the content itself is the
        # identity. A re-delivered duplicate is byte-identical, so it still
        # deduplicates.
        alert_id = "sha256:" + hashlib.sha256(raw).hexdigest()

    return {
        "manager": _text(_dict(alert.get("manager")).get("name")) or "",
        "alert_id": alert_id,
        "ts": parse_timestamp(alert.get("timestamp")),
        "rule_id": _text(rule.get("id")),
        "rule_level": _level(rule.get("level")),
        "rule_description": _text(rule.get("description")),
        "rule_groups": _texts(rule.get("groups")),
        "mitre_ids": _texts(mitre.get("id")),
        "mitre_tactics": _texts(mitre.get("tactic")),
        "agent_id": _text(agent.get("id")),
        "agent_name": _text(agent.get("name")),
        "agent_ip": _text(agent.get("ip")),
        "decoder": _text(_dict(alert.get("decoder")).get("name")),
        "location": _text(alert.get("location")),
        "src_ip": _text(data.get("srcip")),
        "src_user": _text(data.get("srcuser")),
        "raw": alert,
        "source_topic": topic,
        "source_partition": partition,
        "source_offset": offset,
    }


def parse_timestamp(value: Any) -> datetime | None:
    """Wazuh writes ``2026-09-21T16:12:21.123+0000``; None if unparseable."""
    if not isinstance(value, str):
        return None
    for fmt in ("%Y-%m-%dT%H:%M:%S.%f%z", "%Y-%m-%dT%H:%M:%S%z"):
        try:
            return datetime.strptime(value, fmt)
        except ValueError:
            continue
    try:
        parsed = datetime.fromisoformat(value)
    except ValueError:
        return None
    return parsed if parsed.tzinfo is not None else None  # never guess a timezone


def _without_nul(value: Any) -> Any:
    """Replace U+0000 with U+FFFD in every string, keys included."""
    if isinstance(value, str):
        return value.replace("\x00", "\ufffd")
    if isinstance(value, list):
        return [_without_nul(v) for v in value]
    if isinstance(value, dict):
        return {_without_nul(k): _without_nul(v) for k, v in value.items()}
    return value


def _dict(value: Any) -> dict:
    return value if isinstance(value, dict) else {}


def _text(value: Any) -> str | None:
    if value is None or isinstance(value, (dict, list)):
        return None
    return str(value)


def _texts(value: Any) -> list[str]:
    if isinstance(value, str):
        return [value]
    if isinstance(value, list):
        return [str(v) for v in value if v is not None and not isinstance(v, (dict, list))]
    return []


def _level(value: Any) -> int | None:
    try:
        level = int(value)
    except (TypeError, ValueError):
        return None
    return level if 0 <= level <= _SMALLINT_MAX else None

