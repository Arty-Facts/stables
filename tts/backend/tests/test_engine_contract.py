"""Every engine must be callable the way the routes call it.

The routes call `engine.synthesize(text, voice, lang, size)` positionally. An
engine that does not accept that signature fails only when a real request reaches
it, which the stub-based route tests cannot catch — so the signature is checked
here, for each engine the registry actually builds.
"""

import inspect

import pytest

from backend.engines.base import TTSEngine
from backend.engines.kokoro import KokoroEngine
from backend.engines.piper import PiperEngine
from backend.engines.qwen import QwenEngine
from backend.engines.registry import default_engines


ENGINE_CLASSES = [KokoroEngine, PiperEngine, QwenEngine]

#: The call the routes make, positionally.
ROUTE_CALL_ARGS = ("text", "voice", "lang", "size")


def test_default_engines_are_covered_by_this_test():
    built = {type(e).__name__ for e in default_engines()}
    assert built == {cls.__name__ for cls in ENGINE_CLASSES}, (
        "a new engine was registered without a contract check"
    )


@pytest.mark.parametrize("engine_class", ENGINE_CLASSES)
def test_synthesize_accepts_what_the_routes_pass(engine_class):
    signature = inspect.signature(engine_class.synthesize)
    params = list(signature.parameters)[1:]  # drop self

    assert params[0] == "text"
    assert params[1] == "voice"
    for name in ("lang", "size"):
        assert name in signature.parameters, f"{engine_class.__name__} lacks {name}"
        assert signature.parameters[name].default is None or name == "lang", (
            f"{name} must be optional on {engine_class.__name__}"
        )


@pytest.mark.parametrize("engine_class", ENGINE_CLASSES)
def test_engines_declare_the_protocol(engine_class):
    engine = engine_class()
    assert isinstance(engine, TTSEngine)
    assert isinstance(engine.name, str) and engine.name
    assert isinstance(engine.languages, tuple)
    assert isinstance(engine.available(), bool)
    assert isinstance(engine.voices(), list)


@pytest.mark.parametrize("engine_class", ENGINE_CLASSES)
def test_engine_names_are_unique(engine_class):
    names = [cls().name for cls in ENGINE_CLASSES]
    assert len(names) == len(set(names))
