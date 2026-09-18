"""The announcement queue: bounded, and honest about what it dropped.

Announcements come from background work nobody is watching, so this queue is the
one place in the stack a runaway producer would push into. The bounds and the
delivery-once rule are what the rest of the system relies on.
"""

import pytest

from backend.notifications import DEFAULT_CAPACITY, KINDS, NotificationQueue


def test_add_and_consume_in_order():
    queue = NotificationQueue()
    queue.add("first")
    queue.add("second")
    assert [n.text for n in queue.consume()] == ["first", "second"]


def test_consume_removes_what_it_returns():
    """Delivery is take-once, so a poll loop cannot speak the same line forever."""
    queue = NotificationQueue()
    queue.add("only")
    queue.consume()
    assert queue.pending() == []


def test_pending_does_not_remove():
    queue = NotificationQueue()
    queue.add("kept")
    assert [n.text for n in queue.pending()] == ["kept"]
    assert [n.text for n in queue.pending()] == ["kept"]


def test_ids_are_unique_and_increasing():
    queue = NotificationQueue()
    first, _ = queue.add("a")
    second, _ = queue.add("b")
    assert second.id > first.id


def test_empty_text_is_rejected():
    queue = NotificationQueue()
    with pytest.raises(ValueError):
        queue.add("   ")


def test_unknown_kind_falls_back_to_say():
    queue = NotificationQueue()
    note, _ = queue.add("hello", kind="shout")
    assert note.kind == "say"
    assert "say" in KINDS


def test_queue_is_bounded_and_drops_the_oldest():
    """The whole point: a producer that never stops cannot grow this."""
    queue = NotificationQueue(capacity=3)
    for i in range(5):
        queue.add(f"message {i}")

    remaining = [n.text for n in queue.pending()]
    assert remaining == ["message 2", "message 3", "message 4"]
    assert queue.stats()["pending"] == 3
    assert queue.stats()["capacity"] == 3


def test_drops_are_reported_to_the_caller():
    queue = NotificationQueue(capacity=1)
    queue.add("first")
    _note, dropped = queue.add("second")

    assert dropped == 1
    assert queue.stats()["dropped_total"] == 1


def test_capacity_below_one_is_clamped():
    # A zero-capacity queue would silently discard everything.
    queue = NotificationQueue(capacity=0)
    queue.add("still kept")
    assert queue.stats()["pending"] == 1


def test_clear_reports_how_many_were_dropped():
    queue = NotificationQueue()
    queue.add("a")
    queue.add("b")
    assert queue.clear() == 2
    assert queue.pending() == []


def test_limit_takes_only_what_was_asked_for():
    queue = NotificationQueue()
    for i in range(4):
        queue.add(f"m{i}")
    taken = queue.consume(limit=2)
    assert [n.text for n in taken] == ["m0", "m1"]
    assert [n.text for n in queue.pending()] == ["m2", "m3"]


def test_default_capacity_is_a_real_bound():
    assert DEFAULT_CAPACITY > 0
    assert NotificationQueue().stats()["capacity"] == DEFAULT_CAPACITY
