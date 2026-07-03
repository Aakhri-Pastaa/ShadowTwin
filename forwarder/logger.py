"""Logging setup for ShadowTwin Forwarder.

Configures the root logger with a size-rotating file handler and an
optional console handler. Modules log through ``logging.getLogger(__name__)``.
"""

from __future__ import annotations

import logging
import logging.handlers
import os

from config import LoggingConfig

_FORMAT = "%(asctime)s %(levelname)-8s %(name)-10s %(message)s"
_DATE_FORMAT = "%Y-%m-%dT%H:%M:%S%z"


def setup_logging(cfg: LoggingConfig) -> None:
    """Configure the root logger from *cfg*. Called once at startup."""
    root = logging.getLogger()
    root.setLevel(cfg.level)
    formatter = logging.Formatter(_FORMAT, datefmt=_DATE_FORMAT)

    log_dir = os.path.dirname(os.path.abspath(cfg.file))
    os.makedirs(log_dir, exist_ok=True)
    file_handler = logging.handlers.RotatingFileHandler(
        cfg.file,
        maxBytes=cfg.max_bytes,
        backupCount=cfg.backup_count,
        encoding="utf-8",
    )
    file_handler.setFormatter(formatter)
    root.addHandler(file_handler)

    if cfg.console:
        console_handler = logging.StreamHandler()
        console_handler.setFormatter(formatter)
        root.addHandler(console_handler)
