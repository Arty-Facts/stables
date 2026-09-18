"""The opening chunk is cut short so speech starts before the whole sentence is made.

Time to first audio is what a listener feels, and it scales with the length of the
first chunk. The invariant that matters most is the boring one: cutting must not
drop a clause, because a listener cannot tell a missing clause from a stutter.
"""

from backend import text as text_module

# Three clauses before it reaches the point, which is the case this exists for.
LONG_OPENER = (
    "I have been thinking about this for a while, and I keep coming back to the "
    "same conclusion, which is that the answer depends entirely on what you are "
    "trying to do."
)
SECOND_SENTENCE = (
    "The first thing to check is the network, because everything else depends on "
    "it, and it fails in ways that look like other problems."
)


def _normalized(s: str) -> str:
    return " ".join(s.split())


def test_the_opening_chunk_is_cut_short():
    chunks = text_module.split_for_streaming(LONG_OPENER)
    assert len(chunks) > 1, "a three-clause opener must not be one chunk"
    assert len(chunks[0]) <= text_module._FIRST_CHARS
    assert chunks[0].endswith(","), chunks[0]


def test_nothing_is_lost_when_the_opening_is_cut():
    for sample in (LONG_OPENER, f"{LONG_OPENER} {SECOND_SENTENCE}", "One. Two. Three."):
        chunks = text_module.split_for_streaming(sample)
        assert _normalized(" ".join(chunks)) == _normalized(sample)


def test_first_audio_waits_for_the_cap_and_not_the_sentence():
    """The property that matters: a longer sentence must not delay the start.

    A sentence ten times as long costs the listener no more than the cap allows,
    because the cut is bounded rather than proportional.
    """
    long_opener = ", ".join(f"clause number {i} runs on for a while" for i in range(12))
    chunks = text_module.split_for_streaming(long_opener)
    assert len(long_opener) > 300, len(long_opener)
    assert len(chunks) > 1
    assert len(chunks[0]) <= text_module._FIRST_CHARS, chunks[0]
    assert len(chunks[0]) < len(long_opener) / 3


def test_a_sentence_with_no_usable_pause_is_left_whole():
    # Nothing to break at: slower to start, still correct.
    opener = (
        "This sentence runs on for a considerable distance without offering any "
        "punctuation at all until it finally reaches its end."
    )
    assert text_module.split_for_streaming(opener) == [opener]


def test_a_comma_too_early_to_be_worth_a_call_is_not_used():
    # "Well," is not enough audio to cover the call that makes it.
    opener = (
        "Well, this clause goes on and on well past any sensible opening length "
        "without a pause that is worth taking."
    )
    chunks = text_module.split_for_streaming(opener)
    # Spoken whole: the only pause is too early to pay for an engine call, and
    # cutting there would start sooner and then stall.
    assert len(chunks) == 1, chunks
    assert chunks[0] == opener


def test_only_the_opening_chunk_is_cut():
    # Later chunks are synthesized while earlier ones play, so their length is
    # invisible and sentence-sized keeps engine calls down.
    chunks = text_module.split_for_streaming(f"{LONG_OPENER} {SECOND_SENTENCE}")
    # The second sentence survives intact, at the end of whatever it was merged
    # into: it is spoken while the first is playing, so it is not worth cutting.
    assert chunks[-1].endswith(SECOND_SENTENCE), chunks


def test_a_short_remainder_rides_with_the_next_sentence():
    chunks = text_module.split_for_streaming(f"{LONG_OPENER} {SECOND_SENTENCE}")
    # Short fragments are never their own chunk: they cost a call and buy less
    # audio than the call takes.
    for chunk in chunks[1:-1]:
        assert len(chunk) >= text_module._MIN_CHARS, chunks


def test_text_shorter_than_the_cap_is_untouched():
    assert text_module.split_for_streaming("Okay, that is fine.") == ["Okay, that is fine."]
