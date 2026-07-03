"""``tail -F`` style following of NDJSON files.

:class:`FileTail` follows a single file the way ``tail -F`` does:

* new complete lines are returned as soon as they are written,
* a partially written last line is left alone until its newline arrives,
* rotation (new inode under the same path), truncation, deletion and
  re-creation are all detected and survived without a restart.

Detection compares the inode and size from ``os.stat`` against the open
file handle whenever a read reaches end-of-file — the same strategy GNU
tail uses when inotify is unavailable. Data that has already been read is
never re-read or re-scanned; between polls the process sleeps.
"""

from __future__ import annotations

import logging
import os
from typing import BinaryIO

logger = logging.getLogger(__name__)

# (raw line without trailing newline, inode it came from, byte offset after the line)
Line = tuple[bytes, int, int]


class FileTail:
    """Follow one file like ``tail -F`` and return complete lines."""

    def __init__(
        self,
        name: str,
        path: str,
        start_from: str = "end",
        saved: tuple[int, int] | None = None,
    ) -> None:
        self.name = name
        self.path = path
        self._start_from = start_from
        self._file: BinaryIO | None = None
        self._inode = -1
        self._offset = 0
        self._missing_logged = False
        self._open_initial(saved)

    @property
    def position(self) -> tuple[int, int] | None:
        """Current (inode, offset), or None while no file is open."""
        if self._file is None:
            return None
        return self._inode, self._offset

    def poll(self, max_lines: int) -> list[Line]:
        """Return up to *max_lines* new complete lines.

        Also detects and handles rotation, truncation, deletion and
        re-creation of the followed path. Returns an empty list when there
        is nothing new.
        """
        if self._file is None and not self._open():
            return []
        lines = self._read_lines(max_lines)
        if len(lines) >= max_lines:
            return lines  # more data is likely waiting; check the path later

        # We are at end-of-file (or at a half-written line): compare the
        # path against the handle we are reading from.
        try:
            st = os.stat(self.path)
        except FileNotFoundError:
            lines += self._drain_final_partial()
            self._close("file was deleted; waiting for it to reappear")
            return lines
        except OSError as exc:
            logger.error("%s: stat(%s) failed: %s", self.name, self.path, exc)
            return lines

        if st.st_ino != self._inode:
            # Classic rotation: the path now points at a new file. Finish the
            # old handle, then start the new file from the beginning.
            lines += self._read_lines(max_lines - len(lines))
            if len(lines) >= max_lines:
                return lines  # keep draining the rotated-away file next poll
            lines += self._drain_final_partial()
            self._close("rotated (inode changed)")
            if self._open():
                lines += self._read_lines(max_lines - len(lines))
        elif st.st_size < self._offset:
            # Truncation (e.g. copytruncate rotation): start over.
            logger.warning(
                "%s: file truncated (size %d < offset %d); reading from the start",
                self.name, st.st_size, self._offset,
            )
            self._file.seek(0)
            self._offset = 0
            lines += self._read_lines(max_lines - len(lines))
        return lines

    def close(self) -> None:
        """Release the underlying file handle."""
        self._close("shutdown")

    def _open_initial(self, saved: tuple[int, int] | None) -> None:
        """First open: honour saved state, else the start_from policy."""
        if not self._open():
            return
        if saved is not None:
            inode, offset = saved
            if inode == self._inode:
                size = os.fstat(self._file.fileno()).st_size
                if offset <= size:
                    self._file.seek(offset)
                    self._offset = offset
                    logger.info("%s: resuming inode %d at offset %d", self.name, inode, offset)
                else:
                    logger.warning(
                        "%s: saved offset %d is beyond file size %d "
                        "(truncated while stopped); reading from the start",
                        self.name, offset, size,
                    )
            else:
                logger.info(
                    "%s: file was rotated while stopped (inode %d -> %d); "
                    "reading the new file from the start",
                    self.name, inode, self._inode,
                )
        elif self._start_from == "end":
            self._offset = self._file.seek(0, os.SEEK_END)
            logger.info("%s: no saved state; starting at end of file (offset %d)",
                        self.name, self._offset)
        else:
            logger.info("%s: no saved state; reading from the start", self.name)

    def _open(self) -> bool:
        """Open the followed path; True on success."""
        try:
            handle = open(self.path, "rb")  # noqa: SIM115 - long-lived handle
        except FileNotFoundError:
            if not self._missing_logged:
                logger.warning("%s: %s does not exist yet; waiting for it",
                               self.name, self.path)
                self._missing_logged = True
            return False
        except OSError as exc:
            if not self._missing_logged:
                logger.error("%s: cannot open %s: %s", self.name, self.path, exc)
                self._missing_logged = True
            return False
        self._file = handle
        self._inode = os.fstat(handle.fileno()).st_ino
        self._offset = 0
        self._missing_logged = False
        logger.info("%s: opened %s (inode %d)", self.name, self.path, self._inode)
        return True

    def _close(self, reason: str) -> None:
        if self._file is not None:
            logger.info("%s: closing %s: %s", self.name, self.path, reason)
            self._file.close()
            self._file = None

    def _read_lines(self, max_lines: int) -> list[Line]:
        """Read up to *max_lines* complete lines from the current position."""
        assert self._file is not None
        lines: list[Line] = []
        while len(lines) < max_lines:
            start = self._offset
            raw = self._file.readline()
            if not raw:
                break
            if not raw.endswith(b"\n"):
                # A line still being written: rewind and wait for its newline.
                self._file.seek(start)
                break
            self._offset = start + len(raw)
            lines.append((raw.rstrip(b"\r\n"), self._inode, self._offset))
        return lines

    def _drain_final_partial(self) -> list[Line]:
        """Emit a trailing unterminated line; used only when the file is gone.

        Once the file has been rotated away or deleted, a partial last line
        can never be completed, so forward whatever is there instead of
        dropping it.
        """
        assert self._file is not None
        raw = self._file.readline()
        if not raw:
            return []
        self._offset += len(raw)
        line = raw.rstrip(b"\r\n")
        if not line:
            return []
        logger.warning("%s: last line had no newline; forwarding it as-is", self.name)
        return [(line, self._inode, self._offset)]
