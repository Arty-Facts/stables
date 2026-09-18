"""Make the `backend` package importable.

Tests live in `backend/tests/`, but the package is imported as `backend.*`, so
the `voice/` directory has to be on `sys.path`.
"""

import pathlib
import sys

VOICE_ROOT = pathlib.Path(__file__).resolve().parents[2]
if str(VOICE_ROOT) not in sys.path:
    sys.path.insert(0, str(VOICE_ROOT))
