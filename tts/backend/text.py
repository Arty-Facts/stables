"""Splitting text into speakable chunks.

Streaming uses these chunks: the first sentence can be spoken while the rest is
still being synthesized. Chunks are also the unit the engines see, and Kokoro's
phoneme batching works better on sentence-sized input than on a whole paragraph.
"""

import re

# Sentence-final punctuation followed by whitespace. The lookbehind keeps the
# punctuation with the sentence it closes.
_BOUNDARY = re.compile(r"(?<=[.!?;:])\s+")

# Short fragments are merged up to this length, so a stream of "Yes." / "No."
# does not become one request per word.
_MIN_CHARS = 80

#: Streaming sends the first chunk before synthesizing the rest, so its length is
#: the listener's wait. Chunks after it are produced while the previous one plays,
#: so they stay sentence-sized: fewer, larger engine calls for the same audio.
_FIRST_CHARS = 150

#: The shortest opening fragment worth an engine call. A call costs about the same
#: whatever the text, so a fragment too short to fill the time it takes to make
#: makes the stream fall behind instead of ahead.
_MIN_HEAD = 40

#: A comma is already a pause, so cutting at one costs nothing audible.
_CLAUSE = re.compile(r",\s+")


def split_sentences(text: str, min_chars: int = _MIN_CHARS) -> list[str]:
    """Split *text* into chunks suitable for speaking one after another.

    Returns at least one chunk for non-empty input, and never a chunk that is
    empty or only whitespace.
    """
    stripped = text.strip()
    if not stripped:
        return []

    chunks: list[str] = []
    pending = ""
    for piece in _BOUNDARY.split(stripped):
        piece = piece.strip()
        if not piece:
            continue
        if not pending:
            pending = piece
        elif len(pending) < min_chars:
            pending = f"{pending} {piece}"
        else:
            chunks.append(pending)
            pending = piece
    if pending:
        chunks.append(pending)
    return chunks


def split_for_streaming(
    text: str,
    first_chars: int = _FIRST_CHARS,
    min_head: int = _MIN_HEAD,
) -> list[str]:
    """Chunks to stream: like `split_sentences`, but the opening chunk is cut short.

    Time to first audio is the number a listener feels, and it scales with the
    length of the first chunk. A sentence that opens with three clauses would
    otherwise keep them waiting for all three. Cutting at a comma costs nothing
    audible, because a comma is already a pause.

    Only the first chunk is cut. The rest are produced while earlier ones play, so
    their length is invisible, and leaving them sentence-sized costs fewer engine
    calls for the same audio.

    No usable pause means no cut: a sentence with nothing to break at is spoken
    whole, which is slower to start and still correct.
    """
    chunks = split_sentences(text)
    if not chunks:
        return []

    opening = chunks[0]
    cut = _clause_cut(opening, first_chars, min_head)
    if cut is None:
        return chunks

    head, tail = opening[:cut].rstrip(), opening[cut:].lstrip()
    rest = chunks[1:]
    if rest and len(tail) < _MIN_CHARS:
        # A short remainder rides along with the next sentence rather than costing
        # an engine call of its own.
        rest[0] = f"{tail} {rest[0]}"
    elif tail:
        rest.insert(0, tail)
    return [head, *rest]


def _clause_cut(sentence: str, first_chars: int, min_head: int) -> int | None:
    """Where to cut *sentence* so speech can start, or None to leave it whole.

    The last comma that leaves a head both within the cap and long enough to be
    worth synthesizing. Cutting at the first comma would start sooner and then
    stall, which is worse than a slightly later start.
    """
    best = None
    for match in _CLAUSE.finditer(sentence):
        end = match.start() + 1  # keep the comma, drop the space
        if min_head <= end <= first_chars:
            best = end
    return best


def detect_language(text: str, supported: tuple[str, ...] = ("en", "sv")) -> str:
    """The language of *text*, narrowed to the engines' supported set.

    Falls back to English when detection fails — very short or symbol-only input
    raises inside langdetect — because speaking something is better than
    returning an error for text the user can see on screen.
    """
    from langdetect import DetectorFactory, detect

    DetectorFactory.seed = 0
    try:
        lang = detect(text)
    except Exception:
        return supported[0]
    return lang if lang in supported else supported[0]
