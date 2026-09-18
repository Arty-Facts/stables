"""The device report has to be honest, because the header shows it.

onnxruntime lists its execution providers whether or not a GPU is present, so a
provider list alone would let a CPU fallback display as "GPU". These tests pin the
rule: say gpu only when both the stretch (torch) and the engines (onnxruntime) can
actually reach it.

The probes are replaced rather than the modules they import. Faking `torch` in
`sys.modules` leaks — the replacement outlives the test and every test that runs
afterwards imports the fake, which is how this file first broke fourteen
unrelated tests.
"""

import pytest

from backend import device


@pytest.fixture(autouse=True)
def _reset():
    device.reset_cache()
    yield
    device.reset_cache()


def _probes(monkeypatch, *, torch_cuda: bool, providers: list[str], name="Fake GPU 4090"):
    monkeypatch.setattr(device, "_torch_gpu", lambda: (torch_cuda, name if torch_cuda else None))
    monkeypatch.setattr(device, "_onnx_providers", lambda: providers)


def test_gpu_when_both_halves_have_it(monkeypatch):
    _probes(monkeypatch, torch_cuda=True, providers=["CUDAExecutionProvider", "CPUExecutionProvider"])
    report = device.report()
    assert report["device"] == "gpu"
    assert report["gpu_name"] == "Fake GPU 4090"
    assert report["onnx_provider"] == "CUDAExecutionProvider"


def test_cpu_when_onnxruntime_is_cpu_only(monkeypatch):
    # The trap: torch sees the GPU, onnxruntime does not, and Kokoro is ONNX.
    _probes(monkeypatch, torch_cuda=True, providers=["CPUExecutionProvider"])
    report = device.report()
    assert report["device"] == "cpu", "a provider list must not be mistaken for working CUDA"
    assert report["onnx_provider"] == "CPUExecutionProvider"


def test_cpu_when_torch_is_cpu_only(monkeypatch):
    _probes(monkeypatch, torch_cuda=False, providers=["CUDAExecutionProvider", "CPUExecutionProvider"])
    report = device.report()
    assert report["device"] == "cpu"
    assert report["gpu_name"] is None


def test_cpu_when_nothing_is_available(monkeypatch):
    _probes(monkeypatch, torch_cuda=False, providers=[])
    report = device.report()
    assert report["device"] == "cpu"
    assert report["onnx_provider"] == "CPUExecutionProvider"


def test_gpu_name_is_only_reported_with_a_gpu(monkeypatch):
    _probes(monkeypatch, torch_cuda=True, providers=["CUDAExecutionProvider"], name="")
    assert device.report()["gpu_name"] == ""


def test_the_answer_is_cached(monkeypatch):
    _probes(monkeypatch, torch_cuda=True, providers=["CUDAExecutionProvider"])
    first = device.report()
    # Second call must not probe again: /health is polled every few seconds and
    # the first CUDA call costs about a second.
    monkeypatch.setattr(device, "_torch_gpu", lambda: pytest.fail("probed twice"))
    assert device.report() is first
