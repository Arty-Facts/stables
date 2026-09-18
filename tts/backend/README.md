# Voice backend

FastAPI server that turns text into audio. Kokoro ONNX is the always-available
engine, Piper supplies extra per-language voices, and a phase-vocoder time
stretch provides speed control. A small OpenAI-compatible shim lets existing
clients post a chat request and receive audio.

There is no speech-to-text here. The transcription engines, the VAD streamer,
and the WebSocket route they needed are not part of this server.

The Rust TUI in `../tui` is the interactive front end; other HTTP clients can
use the same API.

## Files

| File | Purpose |
|---|---|
| `start_server.py` | Entrypoint — builds the engines, starts uvicorn. |
| `app.py` | FastAPI app factory (`create_app`). Wiring only. |
| `config.py` | Paths, environment variables, speed limits, language defaults. |
| `routes/health.py` | `/health`, `/`, `/v1/capabilities`. |
| `routes/voices.py` | `/v1/voices`, `/v1/voices/languages`, `/v1/piper/voices`. |
| `routes/tts.py` | `/v1/tts`, `/v1/tts/stream/pcm`, `/v1/chat/completions`. |
| `engines/base.py` | The `TTSEngine` contract and the `Voice` type. |
| `engines/registry.py` | Chooses the engine for a voice; reports capabilities. |
| `engines/kokoro.py` | Kokoro ONNX engine. |
| `engines/piper.py` | Piper engine, driven through the `piper` CLI. |
| `voices.py` | Cloned voices: `~/.stables/voices/<name>.wav` is the registry. |
| `stretch.py` | Phase-vocoder time stretch and PCM conversion. |
| `audio.py` | WAV/PCM encoding and resampling. |
| `text.py` | Sentence splitting and language detection. |
| `quick_start_server.sh` | Downloads models if missing, activates the venv, starts the server. |
| `activate_tts.sh` | Activates an existing virtualenv. |
| `check_setup.py` | Environment validator (models, imports, GPU). |
| `test_connection.py` | Connectivity and TTS generation tester. |
| `test_system.py` | Quick server/TTS smoke test. |
| `requirements.txt` | Runtime dependencies. |
| `requirements-dev.txt` | Test and script dependencies. |
| `Dockerfile` | Container image definition. |

## Running

Inside Stables the server is installed and started for you:

```bash
stables install tts
```

To run it by hand from the `tts/` directory:

```bash
python -m venv venv
source venv/bin/activate
pip install -r backend/requirements.txt
bash backend/quick_start_server.sh
```

Direct invocation:

```bash
python backend/start_server.py --host localhost --port 17493
python backend/start_server.py --host 0.0.0.0 --port 17493  # all interfaces
```

Required model files, under `models/`:

- `models/kokoro-v1.0.onnx`
- `models/voices-v1.0.bin`
- `models/piper/*.onnx` (one pair per Piper voice: `.onnx` + `.onnx.json`)

`quick_start_server.sh` downloads the Kokoro files if they are missing. The
upstream release these come from:

    https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/kokoro-v1.0.onnx
    https://github.com/thewh1teagle/kokoro-onnx/releases/download/model-files-v1.0/voices-v1.0.bin

Piper voices are fetched per voice from `rhasspy/piper-voices` when first used,
and cached under `models/piper/`.

## Voices

Two kinds, and they are resolved the same way.

**Preset voices** come from the engines: Kokoro ships a set of English voices,
Piper provides whichever models are cached under `models/piper/`. Nothing is
installed to add one; it appears when its model file does.

**Cloned voices** are files. `~/.stables/voices/<name>.wav` is all it takes —
the directory is the registry, so there is no database to keep in sync and
`rm` removes a voice. An optional `<name>.txt` beside it holds the reference
transcript that zero-shot cloning uses. A cloned voice shadows a preset with the
same id, because a file you added yourself was deliberate.

`GET /v1/voices` returns both, each entry carrying the `engine` that will
synthesize it.

## Request models

### `TTSRequest` for `/v1/tts`

```json
{
  "text": "Hello world",
  "tts": "auto",
  "speed": 1.0,
  "engine": "kokoro",
  "lang": "auto"
}
```

| Field | Default | Notes |
|---|---|---|
| `text` | required | Text to synthesize. |
| `voice` | `auto` | `auto` selects by language; otherwise a specific Kokoro or Piper voice ID. |
| `speed` | `1.0` | Valid range `0.1` to `4.0`. Applied as a time stretch. |
| `engine` | `kokoro` | `kokoro` or `piper`; language auto-detection can override when `voice` is `auto`. |
| `lang` | `auto` | `auto`, `en`, or `sv` in the current language mapping. |

### `StreamTTSRequest` for `/v1/tts/stream/pcm`

Same fields as `TTSRequest`. The response is always i16 little-endian mono at
24 kHz regardless of the engine's own rate, because the TUI opens its output
device once and plays what arrives. Sentences are synthesized and sent one at a
time, so audio starts before the whole text is ready.

## API reference

All endpoints are on `http://localhost:17493` by default.

| Method | Path | Description |
|---|---|---|
| `GET` | `/` | Basic server message. |
| `GET` | `/health` | Server status, model-loaded flag, engine name. |
| `GET` | `/v1/voices` | Kokoro voice metadata, or an empty list if the engine is not loaded. |
| `GET` | `/v1/voices/languages` | Voices grouped by language and engine. |
| `GET` | `/v1/piper/voices` | Piper voices available to the configured backend/cache. |
| `GET` | `/v1/capabilities` | Engines, which are available, their languages, and the voices directory. |
| `POST` | `/v1/tts` | Single-shot TTS. Returns base64 WAV plus metadata. |
| `POST` | `/v1/tts/stream/pcm` | Raw i16 little-endian PCM stream at 24 kHz mono. |
| `POST` | `/v1/chat/completions` | OpenAI-compatible shim: the last user message becomes TTS audio fields. |

## Endpoint details

### `GET /health`

```json
{ "status": "healthy", "model_loaded": true, "engine": "kokoro" }
```

### `GET /v1/voices`

```json
{
  "voices": [
    { "id": "af_heart", "name": "Heart", "lang": "en-us", "gender": "f", "grade": "A" }
  ]
}
```

### `POST /v1/tts`

```json
{
  "audio": "<base64 WAV>",
  "sampling_rate": 24000,
  "duration": 1.23,
  "text": "Hello world",
  "tts": "af_heart",
  "speed": 1.0
}
```

### `POST /v1/tts/stream/pcm`

Returns PCM bytes with:

- `X-Sample-Rate: 24000`
- `X-Channels: 1`

### `POST /mcp`

Model Context Protocol over HTTP: a client POSTs JSON-RPC 2.0 and gets JSON back.
This is the direction *toward* the user — the rest of the stack is one-way (the user
types, the machine speaks), and this is how a Pi extension or a background agent
leaves something to say. Three tools over the same queue the REST routes use, so
messages have one home, one set of bounds, and no second place to pile up.

| Tool | Does |
| --- | --- |
| `notify` | Queue a message. `text` (required), `kind` — `say`, `summary`, `question`, `ping` — and `source`. |
| `get_notifications` | Read what is waiting. Takes them off the queue unless `consume=false`; `limit` caps how many. |
| `clear_notifications` | Discard everything waiting. |

The queue is bounded (50) and drops the oldest when full, reporting how many it
dropped, so a caller learns its message never arrived instead of assuming it did.
Reads are take-once, because a message spoken twice is worse than one not spoken.
REST and MCP share the queue: `GET /v1/notifications?peek=true` shows what
`notify` queued.

```bash
B=http://127.0.0.1:17493

# Not the protocol: just proof the endpoint is there, and which tools it has.
curl -s $B/mcp

curl -s -X POST $B/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"curl"}}}'

curl -s -X POST $B/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"notify","arguments":{"text":"the tests are green","kind":"summary"}}}'

# Look without taking, then take.
curl -s -X POST $B/mcp -H 'content-type: application/json' \
  -d '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"get_notifications","arguments":{"consume":false}}}'
```

Two things to expect: `notifications/initialized` answers **202 with an empty body**
(a JSON-RPC notification must not be answered — `null` would be an answer), and a
batch is answered in order with the notifications dropped from the reply. A probe
covers the whole surface, including the error codes and the take-once behaviour:

```bash
python3 agent_tools/probe_mcp.py                       # 127.0.0.1:17493
python3 agent_tools/probe_mcp.py --url http://host:17493 --keep
```

## Speed control

`speed` is applied after synthesis as a phase-vocoder stretch, so pitch is
preserved and the engines stay at their natural rate. `speed == 1.0` is a no-op
that returns the audio untouched; any other value needs torch and torchaudio,
which are imported lazily.

## Diagnostics

With the venv active, from the `tts/` directory:

```bash
python backend/check_setup.py
python backend/test_connection.py
python backend/test_system.py
```

While the server is running:

```bash
curl -s http://localhost:17493/health
curl -s http://localhost:17493/v1/voices/languages
curl -s http://localhost:17493/v1/piper/voices
```

## Docker

The build context is the `tts/` directory, because the image copies
`backend/` from it:

```bash
docker build -t stables-tts -f backend/Dockerfile .
```

Models are mounted at `/app/models` by default. Stables manages the container,
image, and model location; see the repository README.
