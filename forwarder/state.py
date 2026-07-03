"""Offset persistence and delivery-order tracking.

The forwarder stores, for every watched file, the inode it was reading and
the byte offset of the next unread line. Offsets only advance once Kafka has
acknowledged every message up to that point (see :class:`AckTracker`), so a
restart re-reads at most the messages that were still in flight —
at-least-once delivery.
"""

from __future__ import annotations

import json
import logging
import os
import tempfile
from collections import deque

logger = logging.getLogger(__name__)


class AckTracker:
    """Track produced-but-unacknowledged lines for one watched file.

    Lines are produced in file order, but Kafka may acknowledge them out of
    order. The committed position therefore only advances along the
    contiguous prefix of acknowledged lines — the offset of a line that is
    still in flight (or that failed delivery) is never skipped past.
    """

    def __init__(self, initial: tuple[int, int] | None) -> None:
        self._committed: tuple[int | None, int] = initial if initial else (None, 0)
        self._next_seq = 0
        self._pending: deque[tuple[int, int, int]] = deque()  # (seq, inode, end_offset)
        self._acked: set[int] = set()

    def track(self, inode: int, end_offset: int) -> int:
        """Register a line that was read; returns its sequence number."""
        seq = self._next_seq
        self._next_seq += 1
        self._pending.append((seq, inode, end_offset))
        return seq

    def ack(self, seq: int) -> None:
        """Mark a line as delivered and advance the committed prefix."""
        self._acked.add(seq)
        while self._pending and self._pending[0][0] in self._acked:
            seq0, inode, end_offset = self._pending.popleft()
            self._acked.discard(seq0)
            self._committed = (inode, end_offset)

    @property
    def committed(self) -> tuple[int | None, int]:
        """(inode, offset) that is safe to persist; inode None = nothing yet."""
        return self._committed

    @property
    def in_flight(self) -> int:
        """Number of lines produced but not yet fully acknowledged."""
        return len(self._pending)


class OffsetStore:
    """Load and persist per-file (inode, offset) positions atomically.

    Writes go to a temporary file in the same directory (fsync'd), followed
    by an ``os.replace``, so the state file is either the old version or the
    new one — never a torn write. The previous checkpoint is kept as
    ``state.json.bak`` and loaded automatically if the main file is ever
    corrupt or lost.
    """

    def __init__(self, path: str) -> None:
        self._path = os.path.abspath(path)
        self._backup = self._path + ".bak"
        self._files: dict[str, dict[str, int]] = {}
        self._dirty = False
        self._load()

    def _load(self) -> None:
        if self._read(self._path):
            logger.info("loaded offsets for %d file(s) from %s", len(self._files), self._path)
            return
        # Main file missing or corrupt: fall back to the backup written on
        # each save. Its offsets are one checkpoint older, so a few events
        # may be re-sent — at-least-once is preserved either way.
        if self._read(self._backup):
            logger.warning("recovered offsets for %d file(s) from backup %s",
                           len(self._files), self._backup)
            self._dirty = True  # rewrite the main file on the next save
            return
        logger.info("no usable state at %s; starting fresh", self._path)

    def _read(self, path: str) -> bool:
        """Load offsets from *path*; quarantines the file if it is corrupt."""
        try:
            with open(path, encoding="utf-8") as fh:
                data = json.load(fh)
            self._files = {
                str(name): {"inode": int(entry["inode"]), "offset": int(entry["offset"])}
                for name, entry in data["files"].items()
            }
            return True
        except FileNotFoundError:
            return False
        except (OSError, ValueError, KeyError, TypeError) as exc:
            quarantine = path + ".corrupt"
            logger.error("%s is unreadable (%s); quarantining it to %s", path, exc, quarantine)
            try:
                os.replace(path, quarantine)
            except OSError:
                pass
            return False

    def get(self, name: str) -> tuple[int, int] | None:
        """Return the saved (inode, offset) for *name*, or None."""
        entry = self._files.get(name)
        if entry is None:
            return None
        return entry["inode"], entry["offset"]

    def update(self, name: str, inode: int, offset: int) -> None:
        """Record a new position for *name*; marks the store dirty if changed."""
        entry = {"inode": inode, "offset": offset}
        if self._files.get(name) != entry:
            self._files[name] = entry
            self._dirty = True

    def save(self) -> None:
        """Atomically write the state file if anything changed since last save."""
        if not self._dirty:
            return
        directory = os.path.dirname(self._path) or "."
        os.makedirs(directory, exist_ok=True)
        fd, tmp_path = tempfile.mkstemp(dir=directory, prefix=".state-", suffix=".tmp")
        try:
            with os.fdopen(fd, "w", encoding="utf-8") as fh:
                json.dump({"version": 1, "files": self._files}, fh, indent=2)
                fh.flush()
                os.fsync(fh.fileno())
            if os.path.exists(self._path):
                os.replace(self._path, self._backup)  # keep the previous checkpoint
            os.replace(tmp_path, self._path)
        except OSError as exc:
            logger.error("could not save state to %s: %s", self._path, exc)
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
            return
        self._fsync_directory(directory)
        self._dirty = False

    @staticmethod
    def _fsync_directory(directory: str) -> None:
        """Flush the directory entry so the rename survives a power loss."""
        try:
            dir_fd = os.open(directory, os.O_RDONLY)
        except OSError:
            return  # not supported on this platform/filesystem
        try:
            os.fsync(dir_fd)
        except OSError:
            pass
        finally:
            os.close(dir_fd)
