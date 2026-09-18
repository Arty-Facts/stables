"""Cloned voices: the voices directory is the registry.

A cloned voice is one audio file, `~/.stables/voices/<name>.<lang>.<suffix>` —
`morgan.sv.wav`, `narrator.en.flac`. The name carries the language because a
clone has none of its own: it speaks whatever it is asked to, and the client
groups voices by language, so without it a cloned voice belongs to no group.

There is no manifest and nothing to keep in sync, so adding a voice is copying a
file and removing one is deleting it. A file with no language — `morgan.wav` —
is still accepted and reports `auto`, which is what this directory held before
languages were part of the name.

The optional `<same stem>.txt` beside the audio is the reference transcript,
which zero-shot cloning uses to reproduce the voice faithfully; without one an
engine has only the speaker's timbre to work from. Preset voices come from the
engines and are never files in this directory.
"""

import pathlib
import re

from . import config
from .engines.base import Voice

# Cloning is what the Qwen engine is for.
CLONE_ENGINE = "qwen"

#: Audio the engines can read. `.wav` is the normal case and what a transcoder
#: should write; the others are accepted because refusing a file that plays is a
#: poor way to be strict.
SUFFIXES = (".wav", ".flac")

TRANSCRIPT_SUFFIX = ".txt"

#: A language tag: `en`, `sv`, or a regional form like `en-gb`. Deliberately not a
#: list of the languages the engines support — a voice for a language no engine
#: knows is still a voice, and the engine is the right place to say it cannot.
_LANGUAGE = re.compile(r"[a-z]{2}(-[a-z]{2})?")

# Filename-safe: no separators, no traversal, nothing that needs quoting.
_UNSAFE = re.compile(r"[^A-Za-z0-9._-]+")


def sanitize_name(name: str) -> str:
    """Turn *name* into something safe to use as a filename.

    Raises `ValueError` when nothing usable is left, rather than writing a file
    with a name the user did not ask for.
    """
    cleaned = _UNSAFE.sub("-", name.strip()).strip("-. ")
    # A leading dot would hide the file; "." and ".." are not names at all.
    if not cleaned or cleaned in {".", ".."}:
        raise ValueError(f"cannot use {name!r} as a voice name")
    return cleaned


def directory() -> pathlib.Path:
    """The voices directory, created on demand."""
    return config.VOICES_DIR


def parse_stem(stem: str) -> tuple[str, str]:
    """Split a voice file stem into (name, language).

    The language is the last dot-separated part when it looks like a language tag,
    so `morgan.sv` is Morgan in Swedish and `my.voice` is a voice called "my.voice".
    No language reads as `auto`.
    """
    name, _, tail = stem.rpartition(".")
    if name and _LANGUAGE.fullmatch(tail):
        return name, tail
    return stem, "auto"


def display_name(name: str) -> str:
    """How a voice name reads in a menu."""
    spaced = name.replace(".", " ").replace("-", " ").replace("_", " ")
    return spaced.strip().title()


def clone_path(name: str, base: pathlib.Path | None = None) -> pathlib.Path:
    """Where a voice for *name* is written. Sanitises first, so callers cannot escape."""
    return (base or directory()) / f"{sanitize_name(name)}{SUFFIXES[0]}"


def audio_path(voice_id: str, base: pathlib.Path | None = None) -> pathlib.Path | None:
    """The file behind *voice_id*, or None when there is no such voice.

    The id is the stem, language included: a voice is addressed by the name it has
    in the directory, so `morgan.sv` cannot be confused with `morgan.en`.
    """
    try:
        stem = sanitize_name(voice_id)
    except ValueError:
        return None
    folder = base or directory()
    for suffix in SUFFIXES:
        candidate = folder / f"{stem}{suffix}"
        if candidate.is_file():
            return candidate
    return None


def transcript_path(name: str, base: pathlib.Path | None = None) -> pathlib.Path:
    """Where the optional reference transcript for *name* lives."""
    return (base or directory()) / f"{sanitize_name(name)}{TRANSCRIPT_SUFFIX}"


def list_cloned(base: pathlib.Path | None = None) -> list[Voice]:
    """Every cloned voice in the directory, newest name order.

    A missing directory is not an error: it just means no voices have been added.
    """
    folder = base or directory()
    if not folder.is_dir():
        return []

    voices: list[Voice] = []
    for path in sorted(folder.iterdir()):
        if not path.is_file() or path.suffix.lower() not in SUFFIXES:
            continue
        name, lang = parse_stem(path.stem)
        voices.append(
            Voice(
                id=path.stem,
                name=display_name(name),
                lang=lang,
                engine=CLONE_ENGINE,
                cloned=True,
                note="cloned",
            )
        )
    return voices


def is_clone(voice_id: str, base: pathlib.Path | None = None) -> bool:
    """Whether *voice_id* names a clone rather than a preset voice."""
    return audio_path(voice_id, base) is not None


def reference(voice_id: str, base: pathlib.Path | None = None) -> tuple[pathlib.Path, str]:
    """The reference audio and transcript for a clone.

    The transcript may be empty: it is an optional companion file, and an engine
    that needs it decides for itself what to do without one.
    """
    audio = audio_path(voice_id, base)
    if audio is None:
        raise FileNotFoundError(f"no reference audio for cloned voice {voice_id!r}")
    script = transcript_path(voice_id, base)
    text = script.read_text(encoding="utf-8").strip() if script.is_file() else ""
    return audio, text
