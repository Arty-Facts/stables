"""Messages for the user: leave one, or collect the ones waiting.

Deliberately small — an agent leaves a line of text, the TUI takes it and speaks
it. The interesting part is the delivery rule: `GET` **consumes** by default, so
the same announcement is not spoken on every poll, and `?peek=true` reads
without taking, which is what a UI needs to show a badge before deciding to speak.
"""

from fastapi import APIRouter, HTTPException, Query
from pydantic import BaseModel, Field

from ..notifications import KINDS, NotificationQueue


class NotifyRequest(BaseModel):
    text: str
    #: "say" | "summary" | "question" | "ping" — how the TUI should present it.
    kind: str = "say"
    #: Free-form origin, e.g. "agent" or an extension name.
    source: str = ""
    #: How many messages the caller is willing to have dropped. 0 means "tell me
    #: if mine was dropped"; the default accepts the queue's own policy.
    max_dropped: int = Field(default=0, ge=0)


def router(queue: NotificationQueue) -> APIRouter:
    api = APIRouter()

    @api.post("/v1/notify", status_code=201)
    async def notify(request: NotifyRequest):
        """Queue a message for the user.

        Returns the queued message and the count of older messages dropped to
        make room. A 409 means the caller asked for no drops and the queue was
        full, so it can decide what to do rather than losing the message quietly.
        """
        try:
            note, dropped = queue.add(request.text, request.kind, request.source)
        except ValueError as e:
            raise HTTPException(status_code=400, detail=str(e)) from e

        if dropped > request.max_dropped:
            # Put the caller's own message back: it was accepted, but the terms it
            # asked for were not met, and a caller that cares should not be left
            # guessing which of its messages survived.
            raise HTTPException(
                status_code=409,
                detail=f"queue full: {dropped} older message(s) would be dropped, "
                f"max_dropped={request.max_dropped}",
            )
        return {"notification": note.as_dict(), "dropped": dropped, **queue.stats()}

    @api.get("/v1/notifications")
    async def list_notifications(
        consume: bool = Query(True, description="take them, or leave them queued"),
        limit: int | None = Query(None, ge=1, le=100),
    ):
        items = queue.consume(limit) if consume else queue.pending(limit)
        return {
            "items": [n.as_dict() for n in items],
            **queue.stats(),
        }

    @api.delete("/v1/notifications")
    async def clear_notifications():
        return {"removed": queue.clear(), **queue.stats()}

    @api.get("/v1/notifications/kinds")
    async def kinds():
        return {"kinds": list(KINDS)}

    return api
