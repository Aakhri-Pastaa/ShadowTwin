"""Kafka producer wrapper built on confluent-kafka (librdkafka).

Delivery guarantees, in combination with :class:`state.AckTracker`:

* ``acks=all`` + idempotence — a message is acknowledged only once the full
  in-sync replica set has it, with no reordering or duplication within a
  producer session.
* ``message.timeout.ms=0`` (default) — librdkafka retries forever, so
  delivery callbacks fail only for non-retriable errors.
* Backpressure — when the local queue is full, :meth:`KafkaWriter.send`
  blocks while serving delivery callbacks instead of dropping events, which
  naturally pauses the file watchers until the broker catches up.
"""

from __future__ import annotations

import logging
from typing import Callable

from confluent_kafka import KafkaError, KafkaException, Message, Producer

from config import KafkaConfig

logger = logging.getLogger(__name__)

# Called with (file name, tracker sequence number) once a line is settled —
# either acknowledged by Kafka or dropped as undeliverable.
AckCallback = Callable[[str, int], None]


class KafkaWriter:
    """Produce NDJSON lines to Kafka and report settled file offsets."""

    def __init__(self, cfg: KafkaConfig, on_ack: AckCallback) -> None:
        self._on_ack = on_ack
        self._fatal = False
        self._broker_down = False
        self.produced = 0
        self.acked = 0
        self.failed = 0
        self.reconnects = 0  # recoveries after an all-brokers-down outage
        self._producer = Producer(
            {
                "bootstrap.servers": ",".join(cfg.bootstrap_servers),
                "client.id": cfg.client_id,
                "acks": "all",
                "enable.idempotence": True,
                "compression.type": cfg.compression,
                "linger.ms": cfg.linger_ms,
                "batch.size": cfg.batch_size,
                "message.timeout.ms": cfg.message_timeout_ms,
                "queue.buffering.max.messages": cfg.queue_max_messages,
                "socket.keepalive.enable": True,
                "reconnect.backoff.ms": 250,
                "reconnect.backoff.max.ms": 10_000,
                "error_cb": self._on_error,
                "logger": logging.getLogger("librdkafka"),
            }
        )
        logger.info("Kafka producer created for %s", ", ".join(cfg.bootstrap_servers))

    @property
    def fatal(self) -> bool:
        """True after an unrecoverable producer error; the caller should exit."""
        return self._fatal

    @property
    def queued(self) -> int:
        """Messages sitting in the local producer queue."""
        return len(self._producer)

    def send(
        self,
        topic: str,
        value: bytes,
        name: str,
        seq: int,
        end_offset: int,
        abort,
    ) -> bool:
        """Queue one message for *topic*.

        Blocks (serving delivery callbacks) while the local queue is full.
        Returns False only when *abort* (a ``threading.Event``) is set while
        waiting, i.e. during shutdown.
        """
        callback = self._delivery_callback(name, seq, end_offset)
        while True:
            try:
                self._producer.produce(topic, value=value, on_delivery=callback)
            except BufferError:
                # Local queue full: the broker is unreachable or slower than
                # the log source. Wait for deliveries; do not drop events.
                if abort.is_set():
                    return False
                self._producer.poll(0.5)
                continue
            except KafkaException as exc:
                # Non-retriable produce error, e.g. the line is larger than
                # message.max.bytes. Log it, settle the offset, keep going.
                logger.error("%s: dropping undeliverable line ending at offset %d "
                             "(topic %s): %s", name, end_offset, topic, exc)
                self.failed += 1
                self._on_ack(name, seq)
                return True
            self.produced += 1
            return True

    def poll(self, timeout: float = 0.0) -> int:
        """Serve queued delivery callbacks; returns the number served."""
        return self._producer.poll(timeout)

    def flush(self, timeout: float) -> int:
        """Wait up to *timeout* seconds for in-flight messages; returns leftovers."""
        remaining = self._producer.flush(timeout)
        if remaining:
            logger.warning("flush timed out with %d message(s) still queued", remaining)
        return remaining

    def _delivery_callback(self, name: str, seq: int, end_offset: int):
        def _on_delivery(err: KafkaError | None, _msg: Message) -> None:
            if err is not None:
                # The offset is NOT settled: it stays uncommitted, and the
                # line is re-read and re-sent after the next start.
                self.failed += 1
                logger.error("%s: delivery failed for line ending at offset %d: %s",
                             name, end_offset, err)
                if err.fatal():
                    self._fatal = True
                return
            self.acked += 1
            if self._broker_down:
                self._broker_down = False
                self.reconnects += 1
                logger.info("Kafka connection restored; deliveries flowing again")
            self._on_ack(name, seq)

        return _on_delivery

    def _on_error(self, err: KafkaError) -> None:
        """librdkafka client-level error callback (invoked from poll/flush)."""
        if err.fatal():
            logger.critical("fatal Kafka error: %s", err)
            self._fatal = True
        elif err.code() == KafkaError._ALL_BROKERS_DOWN:
            if not self._broker_down:  # log the transition once, not every retry
                self._broker_down = True
                logger.warning("all Kafka brokers are down; buffering locally and "
                               "reconnecting in the background")
        else:
            logger.warning("Kafka error (retried automatically): %s", err)
