"""The execution provider is chosen before anything can load a model.

onnxruntime-gpu advertises a TensorRT provider whether or not TensorRT is installed.
Anything that creates a session without ONNX_PROVIDER set makes onnxruntime try it,
fail to load libnvinfer, and print an error that reads like a broken install. The
server avoids that by choosing the provider in start_server.py, at import, before the
registry or any engine exists.

These run in a subprocess on purpose. Importing start_server in-process sets a
process-wide variable that other tests then see — which is exactly how an earlier
version of this file broke `device`'s tests while claiming to check something.
"""

import os
import pathlib
import subprocess
import sys

BACKEND = pathlib.Path(__file__).resolve().parents[1]
ROOT = str(BACKEND.parent)

CODE = (
    "import sys, os;"
    f"sys.path.insert(0, {ROOT!r});"
    "import backend.start_server;"
    "print(os.environ.get('ONNX_PROVIDER'))"
)


def _provider_after_import(env_value: str | None) -> str:
    env = {k: v for k, v in os.environ.items() if k != "ONNX_PROVIDER"}
    if env_value is not None:
        env["ONNX_PROVIDER"] = env_value
    done = subprocess.run(
        [sys.executable, "-c", CODE], env=env, capture_output=True, text=True, timeout=300
    )
    assert done.returncode == 0, done.stderr[-800:]
    return done.stdout.strip()


def test_importing_the_server_chooses_a_provider():
    chosen = _provider_after_import(None)
    # Whichever it is, it comes from the report rather than being a constant, and it is
    # never TensorRT (which the image does not ship).
    assert chosen in {"CUDAExecutionProvider", "CPUExecutionProvider"}, chosen


def test_an_explicit_provider_is_not_overridden():
    assert _provider_after_import("CPUExecutionProvider") == "CPUExecutionProvider"
