"""Where things live, and the limits the backend enforces.

Every path is resolved here so there is exactly one reader of the environment
variables the installer sets — `STABLES_VOICE_MODELS`, `STABLES_VOICES_DIR` and
`HF_HOME` — and one place to look when a mount is wrong.
"""

import os
import pathlib

BACKEND_DIR = pathlib.Path(__file__).resolve().parent
VOICE_DIR = BACKEND_DIR.parent


def _path_from_env(name: str, default: pathlib.Path) -> pathlib.Path:
    value = os.environ.get(name)
    return pathlib.Path(value).expanduser() if value else default


# Weights baked into the image (Kokoro, Piper) or mounted beside it.
MODELS_DIR = _path_from_env("STABLES_VOICE_MODELS", VOICE_DIR / "models")
PIPER_MODELS_DIR = MODELS_DIR / "piper"

# Cloned voices. The directory *is* the registry: one WAV per voice, no manifest.
VOICES_DIR = _path_from_env(
    "STABLES_VOICES_DIR", pathlib.Path.home() / ".stables" / "voices"
)

# Qwen weights are fetched on demand; pinned so a recreate does not re-download
# them into a container-local cache.
HF_HOME = _path_from_env("HF_HOME", pathlib.Path.home() / ".stables" / "tts" / "hf")

KOKORO_MODEL = MODELS_DIR / "kokoro-v1.0.onnx"
KOKORO_VOICES = MODELS_DIR / "voices-v1.0.bin"

# Speed is applied as a time stretch, so these bounds are about how far the
# phase vocoder can be pushed before it stops sounding like speech.
SPEED_MIN = 0.1
SPEED_MAX = 4.0

# What a request means by `lang: "auto"` and `voice: "auto"`.
LANGUAGE_VOICES = {
    "sv": {"engine": "piper", "voice": "sv_SE-lisa-medium"},
    "en": {"engine": "kokoro", "voice": "af_heart"},
}


def language_default(lang: str) -> dict:
    """The engine and voice to use for *lang*, falling back to English."""
    return LANGUAGE_VOICES.get(lang, LANGUAGE_VOICES["en"])
