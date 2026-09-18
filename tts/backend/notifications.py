"""Messages an agent wants to tell the user, when the user is ready to hear them.

The rest of the voice stack is one-way: the user types, the machine speaks. This
is the other direction — a Pi extension, a script, or a background agent leaves a
message ("the tests are green", "I need a decision about X") and the TUI speaks
it when the user is not in the middle of something.

**Bounded on purpose.** Announcements come from background work nobody is
watching, which is exactly the kind of producer that fills memory if a queue is
allowed to grow. The queue holds `capacity` messages, drops the oldest when full,
and reports how many it dropped, so a caller can tell that its message never
arrived instead of assuming it did. This is fail-safe by design: a dropped
announcement is not an error to raise in the user's face, and callers that care
check the returned count.
"""

import itertools
import threading
import time
from collections import deque
from dataclasses import dataclass, field

#: How many undelivered messages are kept before the oldest are dropped.
DEFAULT_CAPACITY = 50

#: What the message is for. The TUI uses this to decide how to present it — a
#: question waits for an answer, a ping is spoken and forgotten.
KINDS = ("say", "summary", "question", "ping")


@dataclass(frozen=True)
class Notification:
    id: int
    text: str
    kind: str = "say"
    source: str = ""
    created_at: float = field(default_factory=time.time)

    def as_dict(self) -> dict:
        return {
            "id": self.id,
            "text": self.text,
            "kind": self.kind,
            "source": self.source,
            "created_at": self.created_at,
        }


class NotificationQueue:
    """A bounded, thread-safe queue of messages waiting to be spoken.

    Thread-safe because the HTTP layer serves requests on several threads while
    the TUI polls from another connection.
    """

    def __init__(self, capacity: int = DEFAULT_CAPACITY):
        self._capacity = max(1, int(capacity))
        self._items: deque[Notification] = deque()
        self._lock = threading.Lock()
        self._ids = itertools.count(1)
        self._dropped = 0

    @property
    def capacity(self) -> int:
        return self._capacity

    def add(self, text: str, kind: str = "say", source: str = "") -> tuple[Notification, int]:
        """Queue a message.

        Returns `(notification, dropped)` where `dropped` counts older messages
        discarded to make room. Callers that must not lose a message check it.
        """
        text = (text or "").strip()
        if not text:
            raise ValueError("notification text is empty")
        if kind not in KINDS:
            kind = "say"

        with self._lock:
            dropped = 0
            while len(self._items) >= self._capacity:
                self._items.popleft()
                dropped += 1
                self._dropped += 1
            note = Notification(id=next(self._ids), text=text, kind=kind, source=source)
            self._items.append(note)
        return note, dropped

    def pending(self, limit: int | None = None) -> list[Notification]:
        """Messages waiting, oldest first, without removing them."""
        with self._lock:
            items = list(self._items)
        return items[:limit] if limit else items

    def consume(self, limit: int | None = None) -> list[Notification]:
        """Take messages for delivery, removing exactly the ones returned.

        Consumption happens here rather than on a later acknowledgement so a
        message cannot be spoken twice if the client never acks. The cost is the
        other side of that trade: a client that dies mid-delivery loses what it
        took, which is acceptable for announcements and is why `peek` exists.
        """
        with self._lock:
            count = min(limit, len(self._items)) if limit else len(self._items)
            taken = [self._items.popleft() for _ in range(count)]
        return taken

    def clear(self) -> int:
        """Drop everything queued. Returns how many were removed."""
        with self._lock:
            removed = len(self._items)
            self._items.clear()
        return removed

    def stats(self) -> dict:
        with self._lock:
            pending = len(self._items)
        return {
            "pending": pending,
            "capacity": self._capacity,
            "dropped_total": self._dropped,
            "kinds": list(KINDS),
        }
