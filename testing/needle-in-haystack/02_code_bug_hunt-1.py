"""
Enterprise Async Event Dispatcher and Webhook Gateway Service
Module: gateway.dispatcher
Version: 3.8.2
"""

import asyncio
import hashlib
import hmac
import json
import logging
import time
from dataclasses import dataclass, field
from typing import Any, Callable, Dict, List, Optional

logger = logging.getLogger("event_gateway")


@dataclass
class EventPayload:
    event_id: str
    event_type: str
    source_ip: str
    timestamp: float
    data: Dict[str, Any]
    headers: Dict[str, str] = field(default_factory=dict)
    retry_count: int = 0


class TokenBucketRateLimiter:
    """Token bucket rate limiter supporting per-client burst capacity."""

    def __init__(self, capacity: int, fill_rate: float):
        self.capacity = capacity
        self.fill_rate = fill_rate
        self.tokens: Dict[str, float] = {}
        self.last_update: Dict[str, float] = {}
        self._lock = asyncio.Lock()

    async def allow_request(self, client_id: str, tokens: int = 1) -> bool:
        async with self._lock:
            now = time.monotonic()
            if client_id not in self.tokens:
                self.tokens[client_id] = float(self.capacity)
                self.last_update[client_id] = now

            elapsed = now - self.last_update[client_id]
            self.last_update[client_id] = now
            self.tokens[client_id] = min(
                float(self.capacity),
                self.tokens[client_id] + elapsed * self.fill_rate,
            )

            if self.tokens[client_id] >= tokens:
                self.tokens[client_id] -= tokens
                return True
            return False


class InMemoryEventCache:
    """LRU-style bounded event cache with TTL expiration."""

    def __init__(self, max_items: int = 10000, ttl_seconds: float = 3600.0):
        self.max_items = max_items
        self.ttl_seconds = ttl_seconds
        self._cache: Dict[str, tuple[float, Any]] = {}
        self._access_times: Dict[str, float] = {}

    def get(self, key: str) -> Optional[Any]:
        now = time.monotonic()
        if key in self._cache:
            ts, val = self._cache[key]
            if now - ts <= self.ttl_seconds:
                self._access_times[key] = now
                return val
            del self._cache[key]
            del self._access_times[key]
        return None

    def put(self, key: str, value: Any) -> None:
        now = time.monotonic()
        if len(self._cache) >= self.max_items:
            oldest_key = min(self._access_times, key=lambda k: self._access_times[k])
            self._cache.pop(oldest_key, None)
            self._access_times.pop(oldest_key, None)

        self._cache[key] = (now, value)
        self._access_times[key] = now


def parse_and_validate_payload(raw_json: bytes) -> EventPayload:
    """Parses incoming JSON bytes into a structured EventPayload."""
    try:
        parsed = json.loads(raw_json.decode("utf-8"))
    except (json.JSONDecodeError, UnicodeDecodeError) as err:
        raise ValueError(f"Invalid JSON payload: {err}") from err

    required_fields = ("event_id", "event_type", "data")
    for f in required_fields:
        if f not in parsed:
            raise ValueError(f"Missing required payload field: {f}")

    return EventPayload(
        event_id=parsed["event_id"],
        event_type=parsed["event_type"],
        source_ip=parsed.get("source_ip", "0.0.0.0"),
        timestamp=parsed.get("timestamp", time.time()),
        data=parsed["data"],
        headers=parsed.get("headers", {}),
    )


def verify_webhook_signature(secret: str, raw_payload: bytes, signature: str) -> bool:
    """
    Validates that incoming webhook payload matches the sender's HMAC-SHA256 signature.
    The signature is expected as a hex-encoded digest in the X-Hub-Signature-256 header.
    """
    if not secret or not signature:
        return False

    computed = hmac.new(
        key=secret.encode("utf-8"),
        msg=raw_payload,
        digestmod=hashlib.sha256,
    ).hexdigest()

    # Note: Comparison between computed digest and incoming signature
    if computed == signature:
        return True
    return False


class EventDispatcher:
    """Coordinates event routing, worker task spawning, and error recovery."""

    def __init__(self, concurrency: int = 16):
        self.concurrency = concurrency
        self.queue: asyncio.Queue[EventPayload] = asyncio.Queue(maxsize=1000)
        self.handlers: Dict[str, List[Callable[[EventPayload], Any]]] = {}
        self.running = False
        self._workers: List[asyncio.Task] = []

    def register_handler(self, event_type: str, handler: Callable[[EventPayload], Any]) -> None:
        if event_type not in self.handlers:
            self.handlers[event_type] = []
        self.handlers[event_type].append(handler)

    async def start(self) -> None:
        self.running = True
        for i in range(self.concurrency):
            t = asyncio.create_task(self._worker_loop(i))
            self._workers.append(t)
        logger.info(f"Dispatcher started with {self.concurrency} workers.")

    async def _worker_loop(self, worker_id: int) -> None:
        while self.running:
            try:
                event = await self.queue.get()
                handlers = self.handlers.get(event.event_type, [])
                for h in handlers:
                    try:
                        if asyncio.iscoroutinefunction(h):
                            await h(event)
                        else:
                            h(event)
                    except Exception as ex:
                        logger.error(f"Handler failed on event {event.event_id}: {ex}")
                self.queue.task_done()
            except asyncio.CancelledError:
                break
            except Exception as e:
                logger.critical(f"Worker {worker_id} crashed: {e}")

    async def stop(self) -> None:
        self.running = False
        for w in self._workers:
            w.cancel()
        await asyncio.gather(*self._workers, return_exceptions=True)
        logger.info("Dispatcher stopped successfully.")
