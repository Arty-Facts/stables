"""The stretch window must cover every sample without growing with the input.

`stretch_bounds` decides how a waveform is cut up before the phase-vocoder
stretch. Getting it wrong either drops audio (a window that stops short of the
end) or reintroduces the unbounded memory growth the chunking exists to prevent,
so the coverage and size guarantees are pinned here.
"""

import numpy as np
import pytest

from backend import device
from backend.stretch import (
    QUALITY_ITERATIONS,
    _STRETCH_CHUNK_SAMPLES,
    _STRETCH_OVERLAP_SAMPLES,
    iterations_for,
    stretch_bounds,
    time_stretch_audio,
)


def test_short_input_is_one_window():
    assert stretch_bounds(1000) == [(0, 1000)]


def test_input_exactly_one_window():
    assert stretch_bounds(_STRETCH_CHUNK_SAMPLES) == [(0, _STRETCH_CHUNK_SAMPLES)]


@pytest.mark.parametrize(
    "total",
    [
        _STRETCH_CHUNK_SAMPLES + 1,
        _STRETCH_CHUNK_SAMPLES * 2,
        _STRETCH_CHUNK_SAMPLES * 3 + 12_345,
        5_000_000,
    ],
)
def test_windows_cover_every_sample(total):
    bounds = stretch_bounds(total)
    assert bounds[0][0] == 0, "the start of the input must be covered"
    assert bounds[-1][1] == total, "the end of the input must not be dropped"
    for (start, end), (next_start, _) in zip(bounds, bounds[1:]):
        assert next_start < end, "consecutive windows must overlap"
        assert next_start > start, "windows must advance"
    assert [(s, e) for s, e in bounds] == sorted(bounds)


@pytest.mark.parametrize("total", [1_000_000, 5_000_000, 20_000_000])
def test_no_window_exceeds_the_memory_bound(total):
    for start, end in stretch_bounds(total):
        assert end - start <= _STRETCH_CHUNK_SAMPLES


def test_overlap_is_at_least_the_crossfade_length():
    bounds = stretch_bounds(_STRETCH_CHUNK_SAMPLES * 4)
    for (_, end), (next_start, _) in zip(bounds, bounds[1:]):
        assert end - next_start >= _STRETCH_OVERLAP_SAMPLES


def test_speed_one_is_a_no_op():
    audio = np.linspace(-1.0, 1.0, 1000, dtype=np.float32)
    assert time_stretch_audio(audio, 1.0) is audio


@pytest.mark.parametrize("speed", [0.5, 2.0])
def test_output_length_scales_with_one_over_speed(speed):
    """Slower speech is longer: duration goes as 1/speed."""
    audio = np.sin(np.linspace(0, 200, 24000)).astype(np.float32)
    stretched = time_stretch_audio(audio, speed)
    assert len(stretched) == pytest.approx(len(audio) / speed, rel=0.15)


# ── quality levels ───────────────────────────────────────────────────────────


@pytest.mark.parametrize(
    "quality,iterations",
    [("low", 8), ("medium", 16), ("high", 32)],
)
def test_levels_map_to_iterations(quality, iterations):
    assert iterations_for(quality) == iterations


def test_named_levels_and_the_table_agree():
    assert set(QUALITY_ITERATIONS) == {"low", "medium", "high"}


def test_a_gpu_gets_high_and_a_cpu_gets_medium(monkeypatch):
    """The default is the whole point: nobody should have to know to ask."""
    monkeypatch.setattr(device, "report", lambda: {"device": "gpu"})
    assert iterations_for(None) == 32

    monkeypatch.setattr(device, "report", lambda: {"device": "cpu"})
    assert iterations_for(None) == 16


def test_an_unknown_level_falls_back_rather_than_failing(monkeypatch):
    # The request models reject these, so reaching here is an internal caller and
    # the safe answer is the default, not a traceback inside a request.
    monkeypatch.setattr(device, "report", lambda: {"device": "gpu"})
    assert iterations_for("ludicrous") == QUALITY_ITERATIONS["medium"]


def test_quality_does_not_change_the_duration():
    """The level buys fidelity, not a different length."""
    audio = np.sin(np.linspace(0, 300, 24000)).astype(np.float32)
    lengths = {q: len(time_stretch_audio(audio, 1.5, q)) for q in QUALITY_ITERATIONS}
    assert len(set(lengths.values())) == 1, lengths
