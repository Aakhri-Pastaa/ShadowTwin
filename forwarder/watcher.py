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

Rotation while the process is *stopped* is handled too. If the saved inode is
no longer at the path, the rotated-away file is located by inode among its
siblings (``<path>.*``) and an optional configured glob, drained from the
saved offset, and only then is the new file read. If it cannot be found — it
was deleted or compressed — the loss is logged with the exact offset rather
than skipped silently.
"""

from __future__ import annotations

import glob
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
        rotated_glob: str | None = None,
    ) -> None:
        self.name = name
        self.path = path
        self._start_from = start_from
        self._rotated_glob = rotated_glob
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
        opened = self._open()
        if saved is None:
            if not opened:
                return
            if self._start_from == "end":
                self._offset = self._file.seek(0, os.SEEK_END)
                logger.info("%s: no saved state; starting at end of file (offset %d)",
                            self.name, self._offset)
            else:
                logger.info("%s: no saved state; reading from the start", self.name)
            return

        inode, offset = saved
        if opened and inode == self._inode and _at_line_boundary(self._file, offset):
            self._file.seek(offset)
            self._offset = offset
            logger.info("%s: resuming inode %d at offset %d", self.name, inode, offset)
            return

        # The saved file is not at the path: rotated away, deleted, or its
        # inode reused. Anything written to it after the checkpoint is still
        # unforwarded, so find it by inode and drain it before the new file.
        found = self._find_rotated(inode, offset)
        if found is not None:
            rotated_path, handle = found
            if opened:
                self._file.close()
            handle.seek(offset)
            self._file, self._inode, self._offset = handle, inode, offset
            logger.warning(
                "%s: file was rotated while stopped; draining %s (inode %d) from "
                "offset %d before continuing with the new file",
                self.name, rotated_path, inode, offset,
            )
            return  # poll() switches to the path once this handle is drained

        if not opened:
            logger.warning("%s: saved file (inode %d) not found; waiting for %s",
                           self.name, inode, self.path)
        elif inode == self._inode:
            logger.warning(
                "%s: saved offset %d is not a line boundary in inode %d (truncated, or "
                "the inode was reused, while stopped); reading from the start",
                self.name, offset, inode,
            )
        else:
            logger.error(
                "%s: file was rotated while stopped and the previous file (inode %d) "
                "was not found (searched %s); anything written to it after offset %d "
                "was NOT forwarded. Reading the new file from the start",
                self.name, inode, ", ".join(self._rotated_patterns()), offset,
            )

    def _rotated_patterns(self) -> list[str]:
        patterns = [glob.escape(self.path) + ".*"]
        if self._rotated_glob:
            patterns.append(self._rotated_glob)
        return patterns

    def _find_rotated(self, inode: int, offset: int) -> tuple[str, BinaryIO] | None:
        """Locate the saved inode under a rotated name; returns (path, handle).

        Rename-rotation never crosses a filesystem, and inode numbers are only
        unique within one, so candidates on another device are ignored.
        """
        try:
            device = os.stat(os.path.dirname(os.path.abspath(self.path))).st_dev
        except OSError:
            return None
        for pattern in self._rotated_patterns():
            for candidate in sorted(glob.glob(pattern)):
                if os.path.abspath(candidate) == os.path.abspath(self.path):
                    continue
                try:
                    st = os.stat(candidate)
                    if st.st_ino != inode or st.st_dev != device:
                        continue
                    handle = open(candidate, "rb")  # noqa: SIM115 - handed to the tailer
                except OSError:
                    continue
                if (os.fstat(handle.fileno()).st_ino == inode
                        and _at_line_boundary(handle, offset)):
                    return candidate, handle
                handle.close()
        return None

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


def _at_line_boundary(handle: BinaryIO, offset: int) -> bool:
    """True if *offset* is a position this tailer could have checkpointed.

    Checkpoints always fall just after a newline, at 0, or at the end of a
    file whose final line had no newline. Anything else means the inode now
    belongs to a different file (inode reuse) or the file shrank.
    """
    # ponytail: one-byte heuristic. An unrelated NDJSON file passes by chance
    # about once per average line length (~1/400). Upgrade path: store a hash
    # of the file's first bytes in the checkpoint and compare it here.
    size = os.fstat(handle.fileno()).st_size
    if offset > size:
        return False
    if offset in (0, size):
        return True
    handle.seek(offset - 1)
    boundary = handle.read(1) == b"\n"
    handle.seek(0)
    return boundary
