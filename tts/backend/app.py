"""FastAPI application factory. Wiring only — no engine logic, no path literals.

Usage:
    from backend.app import create_app
    app = create_app()                      # real engines
    app = create_app(Registry([fake]))      # tests substitute engines
"""

from fastapi import FastAPI

from .engines.registry import Registry
from .notifications import DEFAULT_CAPACITY, NotificationQueue
from .routes import health, mcp, notifications, tts, voices


def create_app(
    registry: Registry | None = None,
    queue: NotificationQueue | None = None,
) -> FastAPI:
    """Build the application around *registry*, or the default engines.

    `queue` holds messages agents leave for the user; it is bounded, and one
    instance is shared by the REST and MCP routes so there is a single place
    messages wait.
    """
    registry = registry if registry is not None else Registry()
    queue = queue if queue is not None else NotificationQueue(DEFAULT_CAPACITY)

    app = FastAPI(title="Stables TTS API")
    app.state.registry = registry
    app.state.queue = queue
    for module in (health, voices, tts):
        app.include_router(module.router(registry))
    for module in (notifications, mcp):
        app.include_router(module.router(queue))
    return app
