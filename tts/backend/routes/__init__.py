"""Route modules. Each exposes `router(registry) -> APIRouter`."""

from . import health, mcp, notifications, tts, voices

__all__ = ["health", "mcp", "notifications", "tts", "voices"]
