"""What this process can actually compute on, for the UI to show.

The distinction this exists to make: onnxruntime ships several execution
providers and *lists* them whether or not there is a GPU to run them on, so a
provider list proves nothing. What does prove something is whether the CUDA
runtime came up (torch's answer) and which provider the server asked for. The
header shows that, rather than letting a CPU fallback look like a GPU.

Computed once and cached: the first torch call initialises CUDA (about a second),
and /health is polled every few seconds.
"""

import os

_cache: dict | None = None


def _torch_gpu() -> tuple[bool, str | None]:
    """Whether torch can use CUDA, and the card's name.

    Kept as its own function so it can be replaced in a test. Faking `torch` in
    `sys.modules` instead looks equivalent and is not: the replacement outlives
    the test and other tests then import the fake.
    """
    try:
        import torch

        if torch.cuda.is_available():
            return True, torch.cuda.get_device_name(0)
    except Exception:
        # No torch, no driver, or a broken CUDA install: CPU either way.
        pass
    return False, None


def _onnx_providers() -> list[str]:
    """The execution providers onnxruntime offers here."""
    try:
        import onnxruntime

        return [p for p in onnxruntime.get_available_providers() if p.endswith("ExecutionProvider")]
    except Exception:
        return []


def report() -> dict:
    """Describe the compute this container can use.

    `device` is "gpu" only when CUDA is genuinely usable, never merely because a
    provider appears in a list.
    """
    global _cache
    if _cache is not None:
        return _cache

    torch_cuda, gpu_name = _torch_gpu()
    providers = _onnx_providers()
    onnx_cuda = "CUDAExecutionProvider" in providers

    # Both halves have to be able to reach the GPU. The stretch is torch; Kokoro
    # and Piper synthesis is onnxruntime. A machine where only one of them has
    # CUDA would report "GPU" while half the work runs on the CPU, which is the
    # kind of thing this field exists to prevent.
    device = "gpu" if (torch_cuda and onnx_cuda) else "cpu"

    # What the engines will be told to use. An explicit ONNX_PROVIDER wins when
    # it is actually available, so forcing CPU is respected; otherwise this is
    # what the server sets at startup (see start_server.py).
    requested = os.environ.get("ONNX_PROVIDER", "")
    if requested and requested in providers:
        active = requested
    else:
        active = "CUDAExecutionProvider" if onnx_cuda else "CPUExecutionProvider"

    _cache = {
        "device": device,
        "gpu_name": gpu_name,
        "onnx_provider": active,
        "onnx_providers": providers,
    }
    return _cache


def reset_cache() -> None:
    """Forget the cached answer. Used by tests."""
    global _cache
    _cache = None
