"""Entrypoint: build the engines, serve the app.

Startup does no model loading. Engines load lazily on first use, so the server is
healthy and answerable in under a second and a cold start does not block the
health probe.
"""

import argparse
import os
import signal
import sys

# Allow `python backend/start_server.py` from the voice/ directory as well as
# `python -m backend.start_server`.
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from backend import device  # noqa: E402
from backend.app import create_app  # noqa: E402
from backend.engines.registry import Registry  # noqa: E402

# kokoro-onnx chooses its execution provider from ONNX_PROVIDER. Set it from what
# onnxruntime actually reports rather than baking it into the image, so a CPU
# build does not ask for CUDA, and an explicit setting still wins.
os.environ.setdefault("ONNX_PROVIDER", device.report()["onnx_provider"])


def kill_existing_servers(port: int) -> None:
    """Stop anything already listening on *port*.

    A leftover server from a previous run would otherwise hold the port, and the
    user would be talking to a stale process with an old model set.
    """
    try:
        import psutil
    except ImportError:
        return

    killed = 0
    for proc in psutil.process_iter(["pid", "name", "cmdline"]):
        try:
            connections = proc.net_connections(kind="inet")
        except (psutil.AccessDenied, psutil.NoSuchProcess):
            continue
        if not any(
            c.laddr and c.laddr.port == port and c.status == psutil.CONN_LISTEN
            for c in connections
        ):
            continue
        if proc.pid == os.getpid():
            continue
        print(f"Stopping existing server (pid {proc.pid}) on port {port}...")
        proc.send_signal(signal.SIGTERM)
        killed += 1
    if killed:
        print(f"Stopped {killed} existing server(s) on port {port}")


def main() -> None:
    parser = argparse.ArgumentParser(description="Stables voice server")
    parser.add_argument("--host", default="127.0.0.1",
                        help="Host to bind (use 0.0.0.0 for all interfaces)")
    parser.add_argument("--port", "-p", type=int, default=17493,
                        help="Port to bind (default: 17493)")
    args = parser.parse_args()

    kill_existing_servers(args.port)

    try:
        import uvicorn
    except ImportError:
        print("uvicorn is not installed: pip install -r backend/requirements.txt")
        sys.exit(1)

    registry = Registry()
    print(f"Engines: {', '.join(registry.names())}")
    print(f"Available: {', '.join(registry.available_names()) or 'none'}")
    print(f"Voices: {len(registry.voices())}")

    app = create_app(registry)
    print(f"Listening on {args.host}:{args.port}")
    uvicorn.run(app, host=args.host, port=args.port)


if __name__ == "__main__":
    main()
