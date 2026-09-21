"""PostgreSQL persistence: findings rows and Kafka offsets, written together.

Each batch is one transaction that inserts the rows and advances the stored
offset of every partition they came from. A crash leaves either both or
neither, so the table never holds rows the offsets do not account for, and
never skips rows the offsets claim were written.
"""

from __future__ import annotations

import logging
from pathlib import Path
from typing import Any

import psycopg
from psycopg.types.json import Jsonb

from normalize import COLUMNS

logger = logging.getLogger(__name__)

SCHEMA = Path(__file__).with_name("schema.sql").read_text(encoding="utf-8")

_INSERT = (
    f"INSERT INTO findings ({', '.join(COLUMNS)}) "
    f"VALUES ({', '.join(['%s'] * len(COLUMNS))}) "
    "ON CONFLICT (manager, alert_id) DO NOTHING"
)
_SAVE_OFFSET = (
    "INSERT INTO ingest_offsets (topic, partition, next_offset) VALUES (%s, %s, %s) "
    "ON CONFLICT (topic, partition) DO UPDATE "
    "SET next_offset = EXCLUDED.next_offset, updated_at = now()"
)

# Raised for a value PostgreSQL will not store (DataError), or one that cannot
# even be encoded to send (UnicodeEncodeError, e.g. a lone surrogate).
_BAD_VALUE = (psycopg.DataError, UnicodeEncodeError)

Offsets = dict[tuple[str, int], int]


class Store:
    """Connection to the findings database."""

    def __init__(self, dsn: str) -> None:
        self._dsn = dsn
        self._conn: psycopg.Connection | None = None

    def connect(self) -> None:
        """(Re)connect and apply the idempotent schema."""
        self.close()
        self._conn = psycopg.connect(self._dsn, autocommit=True)
        self._conn.execute(SCHEMA)
        logger.info("connected to PostgreSQL; schema ready")

    def close(self) -> None:
        if self._conn is not None:
            try:
                self._conn.close()
            finally:
                self._conn = None

    def load_offsets(self) -> Offsets:
        """Next offset to read, per (topic, partition), as last committed."""
        rows = self._conn.execute(
            "SELECT topic, partition, next_offset FROM ingest_offsets").fetchall()
        return {(topic, partition): offset for topic, partition, offset in rows}

    def write(self, rows: list[dict[str, Any]], offsets: Offsets) -> tuple[int, int]:
        """Insert *rows* and advance *offsets* atomically.

        Returns (inserted, rejected). Rows already present — the forwarder is
        at-least-once, so the topic can repeat an alert — are neither.
        """
        params = [_params(row) for row in rows]
        # ponytail: one INSERT per row keeps rowcount exact (inserted vs
        # duplicate) at ~1 round trip each. Fine at Wazuh alert rates; if
        # throughput ever matters, switch to pipeline mode or INSERT ... SELECT
        # FROM unnest(...) RETURNING and count the returned rows.
        try:
            with self._conn.transaction(), self._conn.cursor() as cur:
                inserted = 0
                for p in params:
                    cur.execute(_INSERT, p)
                    inserted += cur.rowcount
                self._save_offsets(cur, offsets)
            return inserted, 0
        except _BAD_VALUE as exc:
            logger.warning("batch of %d rejected by PostgreSQL (%s); retrying row by row",
                           len(rows), exc)

        # One bad value must not wedge the pipeline: retry each row in its own
        # savepoint, skip and report what still fails, and commit the offsets
        # so the batch is not re-read forever.
        inserted = rejected = 0
        with self._conn.transaction(), self._conn.cursor() as cur:
            for row, p in zip(rows, params, strict=True):
                try:
                    with self._conn.transaction():
                        cur.execute(_INSERT, p)
                        inserted += cur.rowcount
                except _BAD_VALUE as exc:
                    rejected += 1
                    logger.error("rejected alert %s from %s[%d]@%d: %s", row["alert_id"],
                                 row["source_topic"], row["source_partition"],
                                 row["source_offset"], exc)
            self._save_offsets(cur, offsets)
        return inserted, rejected

    @staticmethod
    def _save_offsets(cur: psycopg.Cursor, offsets: Offsets) -> None:
        for (topic, partition), next_offset in offsets.items():
            cur.execute(_SAVE_OFFSET, (topic, partition, next_offset))


def _params(row: dict[str, Any]) -> tuple:
    return tuple(Jsonb(row[c]) if c == "raw" else row[c] for c in COLUMNS)
