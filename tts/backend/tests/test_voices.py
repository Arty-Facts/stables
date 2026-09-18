"""Cloned voices are files, and the registry picks the right engine for them.

The voices directory is the registry, so these tests cover what the rest of the
system trusts: names that cannot escape the directory, a missing directory being
normal rather than an error, and a clone taking precedence over a preset that
happens to share its id.
"""

import pathlib

import pytest

from backend import voices
from backend.engines.base import EngineUnavailable, Voice, VoiceNotFound
from backend.engines.registry import Registry

from .test_routes import StubEngine


@pytest.fixture
def voices_dir(tmp_path, monkeypatch) -> pathlib.Path:
    monkeypatch.setattr("backend.config.VOICES_DIR", tmp_path)
    return tmp_path


def _add_voice(folder: pathlib.Path, name: str, transcript: str | None = None):
    (folder / f"{name}.wav").write_bytes(b"RIFF....WAVE")
    if transcript is not None:
        (folder / f"{name}.txt").write_text(transcript)


# ── naming ───────────────────────────────────────────────────────────────────


@pytest.mark.parametrize(
    "given,expected",
    [
        ("morgan", "morgan"),
        ("Morgan Freeman", "Morgan-Freeman"),
        ("  spaced  ", "spaced"),
        ("a/b/c", "a-b-c"),
        ("../../etc/passwd", "etc-passwd"),
        ("dots.and_underscores-ok", "dots.and_underscores-ok"),
    ],
)
def test_sanitize_name_keeps_it_a_filename(given, expected):
    assert voices.sanitize_name(given) == expected


@pytest.mark.parametrize("bad", ["", "   ", "..", ".", "///", "..-.."])
def test_sanitize_name_rejects_nothing_usable(bad):
    with pytest.raises(ValueError):
        voices.sanitize_name(bad)


def test_clone_path_cannot_escape_the_directory(voices_dir):
    # The traversal is neutralised, so the result stays inside the directory.
    path = voices.clone_path("../../evil")
    assert path.parent == voices_dir
    assert voices_dir in path.parents


# ── discovery ────────────────────────────────────────────────────────────────


def test_missing_directory_is_not_an_error(tmp_path, monkeypatch):
    monkeypatch.setattr("backend.config.VOICES_DIR", tmp_path / "nope")
    assert voices.list_cloned() == []


def test_list_cloned_finds_wavs_only(voices_dir):
    _add_voice(voices_dir, "morgan")
    _add_voice(voices_dir, "ada", transcript="hello")
    (voices_dir / "notes.txt").write_text("not a voice")
    (voices_dir / "sample.mp3").write_bytes(b"\x00")

    found = {v.id for v in voices.list_cloned()}
    assert found == {"morgan", "ada"}


def test_cloned_voice_reports_its_engine(voices_dir):
    _add_voice(voices_dir, "morgan")
    voice = voices.list_cloned()[0]
    assert voice.cloned is True
    assert voice.engine == voices.CLONE_ENGINE


def test_reference_returns_audio_and_optional_transcript(voices_dir):
    _add_voice(voices_dir, "morgan", transcript="  the words spoken  ")
    audio, text = voices.reference("morgan")
    assert audio.is_file()
    assert text == "the words spoken"


def test_reference_without_transcript_is_empty_not_an_error(voices_dir):
    _add_voice(voices_dir, "morgan")
    audio, text = voices.reference("morgan")
    assert audio.is_file()
    assert text == ""


def test_reference_for_an_unknown_voice_raises(voices_dir):
    with pytest.raises(FileNotFoundError):
        voices.reference("nobody")


def test_is_clone_distinguishes_files_from_presets(voices_dir):
    _add_voice(voices_dir, "morgan")
    assert voices.is_clone("morgan") is True
    assert voices.is_clone("af_heart") is False
    # A name that cannot be a filename cannot be a file, and must not raise.
    assert voices.is_clone("../nope") is False


# ── registry resolution ──────────────────────────────────────────────────────


def test_clone_shadows_a_preset_with_the_same_id(voices_dir):
    """A file the user added deliberately wins over a shipped voice."""
    _add_voice(voices_dir, "stub_voice")  # same id as StubEngine's preset

    clone_only = Registry([StubEngine(), _CloneEngine()])
    engine = clone_only.resolve("stub_voice")
    assert engine.name == voices.CLONE_ENGINE


def test_preset_resolves_to_the_engine_that_has_it(voices_dir):
    engine = Registry([StubEngine()]).resolve("stub_voice")
    assert engine.name == "stub"


def test_unknown_voice_raises(voices_dir):
    with pytest.raises(VoiceNotFound):
        Registry([StubEngine()]).resolve("not_a_voice")


def test_clone_without_a_cloning_engine_explains_itself(voices_dir):
    _add_voice(voices_dir, "morgan")
    with pytest.raises(EngineUnavailable) as err:
        Registry([StubEngine()]).resolve("morgan")
    assert "cloned" in str(err.value)


def test_unavailable_engine_contributes_no_voices(voices_dir):
    """A machine without Kokoro's weights must not advertise Kokoro's voices."""
    engine = StubEngine()
    engine.available = lambda: False
    registry = Registry([engine])
    assert registry.voices() == []
    assert registry.available_names() == []


class _CloneEngine(StubEngine):
    """Stands in for the cloning engine the registry routes clones to."""

    name = voices.CLONE_ENGINE

    def voices(self) -> list[Voice]:
        return []


# ── the file convention: <name>.<lang>.<suffix> ───────────────────────────────


def _found(tmp_path) -> dict:
    """Voices in *tmp_path* by id, so a test names the id it means."""
    return {v.id: v for v in voices.list_cloned(base=tmp_path)}


def test_a_voice_file_carries_its_language(tmp_path):
    for name in ("morgan.sv.wav", "narrator.en.flac"):
        (tmp_path / name).write_bytes(b"x")
    found = _found(tmp_path)
    assert set(found) == {"morgan.sv", "narrator.en"}
    assert found["morgan.sv"].lang == "sv"
    assert found["morgan.sv"].name == "Morgan"
    assert found["narrator.en"].lang == "en"


def test_a_name_with_no_language_reads_as_auto(tmp_path):
    # What this directory held before the language was part of the name.
    (tmp_path / "morgan.wav").write_bytes(b"x")
    (voice,) = voices.list_cloned(base=tmp_path)
    assert (voice.id, voice.name, voice.lang) == ("morgan", "Morgan", "auto")


def test_only_a_language_tag_is_taken_as_a_language(tmp_path):
    # "my.voice" is one name, not a voice called "my" in language "voice".
    (tmp_path / "my.voice.wav").write_bytes(b"x")
    (tmp_path / "my.voice.en.wav").write_bytes(b"x")
    found = _found(tmp_path)
    assert found["my.voice"].name == "My Voice"
    assert found["my.voice"].lang == "auto"
    assert found["my.voice.en"].lang == "en"


def test_the_same_name_in_two_languages_is_two_voices(tmp_path):
    for name in ("morgan.sv.wav", "morgan.en.wav"):
        (tmp_path / name).write_bytes(b"x")
    found = _found(tmp_path)
    assert {v.lang for v in found.values()} == {"sv", "en"}
    assert len(found) == 2


def test_a_voice_is_addressed_with_its_language(tmp_path):
    """The id is the stem, so the language is part of the address.

    Asking for "morgan" would otherwise be ambiguous the moment the same name
    exists in two languages.
    """
    (tmp_path / "morgan.sv.wav").write_bytes(b"x")
    assert voices.is_clone("morgan.sv", base=tmp_path)
    assert not voices.is_clone("morgan", base=tmp_path)


def test_a_regional_language_parses(tmp_path):
    (tmp_path / "solo.en-gb.wav").write_bytes(b"x")
    (voice,) = voices.list_cloned(base=tmp_path)
    assert (voice.name, voice.lang) == ("Solo", "en-gb")


def test_the_transcript_sits_beside_the_audio_with_the_same_stem(tmp_path):
    (tmp_path / "morgan.sv.wav").write_bytes(b"x")
    (tmp_path / "morgan.sv.txt").write_text("hej, det här är en röst")
    audio, text = voices.reference("morgan.sv", base=tmp_path)
    assert audio.name == "morgan.sv.wav"
    assert text == "hej, det här är en röst"


def test_a_flac_reference_is_found_too(tmp_path):
    (tmp_path / "narrator.en.flac").write_bytes(b"x")
    audio, text = voices.reference("narrator.en", base=tmp_path)
    assert audio.suffix == ".flac"
    assert text == ""
