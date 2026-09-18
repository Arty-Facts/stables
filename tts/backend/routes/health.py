"""Liveness and capability reporting.

`/health` answers without loading any model, so a container can be probed while
models are still cold. It reports which engines *could* run rather than claiming
a specific one is active, because a request picks the engine per voice.
"""

from fastapi import APIRouter

from .. import device
from ..engines.registry import Registry


def router(registry: Registry) -> APIRouter:
    api = APIRouter()

    @api.get("/health")
    async def health():
        available = registry.available_names()
        return {
            "status": "healthy",
            "model_loaded": bool(available),
            "engine": available[0] if available else "none",
            "engines": available,
            # Whether this is really running on a GPU, so a client can say so
            # instead of leaving a CPU fallback invisible.
            **device.report(),
        }

    @api.get("/")
    async def root():
        return {
            "message": "Stables voice server",
            "status": "running",
            "engines": registry.available_names(),
        }

    @api.get("/v1/capabilities")
    async def capabilities():
        """What this server can speak, and where its voices live.

        The TUI uses this to describe the choices it offers — which engines are
        usable, and which languages each covers — without guessing.
        """
        return registry.capabilities()

    return api
