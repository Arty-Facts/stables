> **Historical design note — not implemented.** This was written while planning a
> future full-Rust rewrite: a `ratts-server` binary running Kokoro ONNX directly,
> with `crates/ratts-core` and `crates/ratts-server`. None of it exists in this
> checkout. The TUI is a single client crate and the server is the Python one in
> `../backend/`. Paths below (`ratts/`) refer to the pre-vendoring layout.
>
> Kept for reference. See `README.md` for how the code actually works.

# ratts — Future Full-Rust Kokoro TTS Design Note

> **Current status:** This file describes a future Rust server rewrite design, not the current implementation. Today `ratts/` is a single Rust TUI client crate (`ratts-cli` + `ratts_lib`) that connects to the Python backend in `../backend/`. The `crates/ratts-core` and `crates/ratts-server` workspace shown below is not implemented in this checkout.
>
> For current operation, start the Python backend (`python backend/start_server.py` or `docker compose up -d`) and run the TUI with `cd ratts && cargo build --release && ./target/release/ratts-cli`.

The planned design below explores a zero-Python TTS system where Kokoro ONNX would run directly in Rust, with one server binary and one TUI client binary. PCM would stream over HTTP from model to speakers.

```
ratts-server  ──(PCM stream)──►  ratts-cli ──► cpal ──► speakers
     │                                │
  kokoro-v1.0.onnx              ratatui TUI
  voices-v1.0.bin               arboard clipboard
```

---

## Workspace Layout

```
ratts/
├── Cargo.toml                     workspace (resolver = "2")
├── README.md                      this file
├── scripts/
│   └── inspect_voices.py          one-shot: verify voices-v1.0.bin shape/dtype
├── crates/
│   ├── ratts-core/                lib: TTS engine shared by server + future tools
│   │   └── src/
│   │       ├── lib.rs
│   │       ├── tts/
│   │       │   ├── mod.rs
│   │       │   ├── vocab.rs       VOCAB map + tokenize() — hardcoded ~160 symbols
│   │       │   ├── model.rs       KokoroModel: ort session, run inference
│   │       │   ├── phonemizer.rs  text → IPA → token IDs (espeak-ng subprocess)
│   │       │   ├── voices.rs      VoiceBank: load voices-v1.0.bin (NPZ)
│   │       │   └── time_stretch.rs phase vocoder (rustfft, only for speed outside 0.5-2.0)
│   │       └── config/
│   │           └── mod.rs         ModelConfig: paths, default voice, speed
│   ├── ratts-server/              bin: axum HTTP server
│   │   └── src/
│   │       ├── main.rs            clap CLI, tokio runtime
│   │       ├── state.rs           Arc<AppState> with KokoroModel
│   │       └── routes.rs          axum handlers
│   └── ratts-cli/              bin: ratatui TUI (already implemented, 70 tests)
│       ├── src/   (existing)
│       └── tests/ (existing)
```

---

## Key Technical Findings

Sourced directly from `kokoro_onnx` Python package source.

### voices-v1.0.bin format

Loaded with `np.load(path)` — **standard NumPy NPZ** (zip of .npy files).
Each key is a voice name; each value is `float32 [max_len, 1, 256]`.

```python
voices = np.load("voices-v1.0.bin")      # NpzFile (dict-like)
style  = voices["af_heart"][len(tokens)]  # → shape [1, 256]
```

**Rust**: use `ndarray-npy::NpzReader` to load each voice array directly.

### ONNX model inputs / outputs

| Name     | dtype   | shape      | notes                                        |
|----------|---------|------------|----------------------------------------------|
| `tokens` | int64   | `[1, T+2]` | `[0, *phoneme_ids, 0]` (pad token 0 on ends) |
| `style`  | float32 | `[1, 256]` | `voices[name][len(tokens)]` — length-indexed |
| `speed`  | float32 | `[1]`      | range 0.5–2.0 (model constraint)             |

**Output**: first output tensor, float32, shape `[N]` — mono audio at 24000 Hz.

### Phonemizer pipeline

```
raw text
  │  normalize_text()    numbers, abbreviations, punctuation (pure regex)
  ▼
normalized text
  │  espeak-ng -v en-us -q --ipa   (same flags as Python phonemizer lib)
  ▼
raw IPA string
  │  substitutions:
  │    ʲ→j  r→ɹ  x→k  ɬ→l
  │    "kəkˈoːɹoʊ" → "kˈoʊkəɹoʊ"   (kokoro brand name fix)
  │    trailing-z rules, en-us: "ninety" ti→di
  │  filter: keep only chars present in VOCAB
  ▼
phoneme string  (max 510 chars)
  │  tokenize(): VOCAB[char] for each char, skip unknowns
  ▼
Vec<i64>  (prepend + append pad token 0)
```

### VOCAB — hardcoded in Rust

```python
_pad         = "$"                              # index 0
_punctuation = ';:,.!?¡¿—…"«»"" '             # indices 1-15
_letters     = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
_ipa         = "ɑɐɒæɓʙβɔɕçɗɖðʤəɘɚɛɜɝɞɟʄɡɠɢʛɦɧħɥʜɨɪʝɭɬɫɮʟɱɯɰŋɳɲɴøɵɸθœɶʘɹɺɾɻʀʁɽʂʃʈʧɯɰŋ...ˈˌːˑʼʴʰʱʲʷˠˤ˞↓↑→↗↘'̩'ᵻ"
# VOCAB[char] = index in concatenated string
```

### Speed handling

- Model accepts **0.5–2.0×** natively — pass speed directly to the `speed` input tensor, no post-processing needed.
- For speeds **outside** that range (e.g. 0.1×, 3.0×): generate at 1.0× then apply phase-vocoder time-stretch post-inference (`tts/time_stretch.rs`).
- The phase vocoder is an exact port of `torchaudio.functional.phase_vocoder` + `torch.stft/istft` (center=True, n_fft=2048, hop=512, Hann window).

---

## Implementation Plan

### Phase 1 — vocab + voices (no ONNX yet)

**`tts/vocab.rs`**
```rust
// Build VOCAB once at startup
static VOCAB: LazyLock<HashMap<char, i64>> = LazyLock::new(|| { ... });

pub fn tokenize(phonemes: &str) -> Vec<i64> {
    // [0, *ids, 0]
}
pub fn vocab_size() -> usize
```

**`tts/voices.rs`**
```rust
pub struct VoiceBank {
    voices: HashMap<String, Array3<f32>>,  // [max_len, 1, 256]
}
impl VoiceBank {
    pub fn load(path: &Path) -> Result<Self>   // NpzReader
    pub fn style_for(&self, name: &str, token_len: usize) -> Result<Array2<f32>>
    pub fn names(&self) -> Vec<&str>
}
```

Tests: load real file → assert names non-empty, shape `[_, 1, 256]`; `style_for` bounds.

---

### Phase 2 — phonemizer

**`tts/phonemizer.rs`**
```rust
pub struct Phonemizer { lang: String }

impl Phonemizer {
    pub fn new(lang: &str) -> Self
    pub fn phonemize(&self, text: &str) -> Result<String>
    // internals:
    //   normalize_text → espeak_subprocess → apply_substitutions → vocab_filter
}

fn normalize_text(text: &str) -> String   // direct port of Python normalize_text()
fn espeak_ipa(text: &str, lang: &str) -> Result<String>  // Command::new("espeak-ng")
fn apply_substitutions(ipa: &str, lang: &str) -> String
```

**`normalize_text` covers**: year splitting, time ("3:05"→"three oh five"),
money ("$1.50"→"one dollar and fifty cents"), Dr./Mr./Ms. expansion,
yeah→ye'a, digit range ("5-10"→"five to ten"), etc.

Tests: ~10 sentence fixtures validated against Python output.

---

### Phase 3 — ONNX model

**`ort` crate**: verify available version with `cargo search ort` before adding — the crate name and version on crates.io changes across releases. v2 has a cleaner API but may need `features = ["load-dynamic"]` and the `ORT_DYLIB_PATH` env var; v1.16 is the stable fallback.

```rust
// tts/model.rs
pub struct KokoroModel { session: Session }

impl KokoroModel {
    pub fn load(path: &Path) -> Result<Self>
    pub fn generate(
        &self,
        tokens: &[i64],     // un-padded; model.rs adds [0,..,0]
        style: Array2<f32>, // [1, 256]
        speed: f32,
    ) -> Result<Vec<f32>>   // mono f32 at 24 kHz
}
```

Input construction:
```rust
let padded = chain([0], tokens, [0]);           // prepend+append pad
let t = Array2::from_shape_vec((1, n), padded); // [1, T+2]
let s = Array1::from_elem(1, speed);            // [1]
// style already [1, 256] from VoiceBank
```

---

### Phase 4 — time stretching (out-of-range speeds only)

**`tts/time_stretch.rs`** — exact port of `torchaudio.functional.phase_vocoder` with `rustfft`. **Already implemented and tested.**

```rust
pub fn time_stretch(samples: &[f32], speed: f32) -> Result<Vec<f32>>
// n_fft=2048, hop=512, Hann window, center=True
// Only called when speed < 0.5 or > 2.0
```

Algorithm (mirrors torchaudio, fixed analysis+synthesis hop=512):
1. Reflect-pad input by N_FFT/2 on each side (center=True), STFT with Hann window
2. Phase vocoder: output frame `t` reads input at position `t * rate` (floor + linear mag interp); phase accumulator initialised to `phase_0 = arg(spec[0])`; each step: output current `acc[k]`, then `acc[k] += pa[k] + wrap(arg(f1[k]) - arg(f0[k]) - pa[k])`
3. ISTFT: overlap-add at fixed hop=512, normalise by window² sum, strip center padding, trim to `round(len/speed)` samples

Tests (11, all green): passthrough identity, empty passthrough, 2× halves length, 0.5× doubles length, 1.5× fractional length, output not silence, RMS preserved, STFT-ISTFT roundtrip MSE < 1e-5, Hann window shape, reflect-pad PyTorch semantics, phase_vocoder rate=1 length.

---

### Phase 5 — ratts-server (axum)

**Full pipeline per chunk**:
```
chunk → Phonemizer → tokenize → VoiceBank::style_for → KokoroModel::generate
      → [time_stretch if needed] → f32→i16 → Bytes → stream to client
```

**Endpoints**:
```
POST /v1/tts              WAV base64 JSON response (parity with Python server)
POST /v1/tts/stream/pcm   StreamBody: raw i16 LE chunks at 24 kHz
GET  /v1/voices           JSON array of voice names
GET  /health              { status, engine: "kokoro-onnx-rs" }
```

**Streaming handler sketch**:
```rust
async fn stream_pcm(State(s): State<Arc<AppState>>, Json(req): Json<Req>)
    -> impl IntoResponse
{
    let chunks = smart_chunk(&req.text, 512);
    let stream = stream::iter(chunks).then(move |chunk| {
        let s = Arc::clone(&s);
        async move {
            let phonemes = s.phonemizer.phonemize(&chunk)?;
            let ids   = tokenize(&phonemes);
            let style = s.voices.style_for(&req.voice, ids.len())?;
            let audio = s.model.generate(&ids, style, req.speed)?;
            let pcm   = f32_to_i16_le(&audio);
            Ok::<Bytes, Error>(Bytes::from(pcm))
        }
    });
    (pcm_headers(), Body::from_stream(stream))
}
```

**CLI** (clap):
```bash
ratts-server \
  --model  models/kokoro-v1.0.onnx \
  --voices models/voices-v1.0.bin  \
  --host 0.0.0.0 --port 5000
```

---

### Phase 6 — ratts-cli updates

Existing TUI works; only one change:
- `GET /v1/voices` to populate voice list dynamically instead of `default_voices()`

---

### Phase 7 — tests + coverage

```bash
# All unit + integration tests
cargo test

# Coverage (non-UI code)
cargo install cargo-tarpaulin
cargo tarpaulin -p ratts-core \
  --out Html --output-dir coverage/
```

Target: ≥70% on ratts-core.

---

## Build & Run

```bash
# Everything
cargo build --release

# Server
./target/release/ratts-server \
  --model models/kokoro-v1.0.onnx \
  --voices models/voices-v1.0.bin

# TUI client
./target/release/ratts-cli

# Tests — core (no model files needed)
cargo test -p ratts-core

# Tests — client (no model files needed)
cargo test -p ratts-cli

# Coverage
cargo tarpaulin -p ratts-core --out Html --output-dir coverage/
```

---

## Dependency Plan

| Crate         | Version | Purpose                       |
|---------------|---------|-------------------------------|
| `ort`         | 2.x     | ONNX Runtime (check crates.io)|
| `ndarray`     | 0.16    | tensor math                   |
| `ndarray-npy` | 0.9     | read voices NPZ               |
| `zip`         | 2       | NPZ = zip of npy files        |
| `rustfft`     | 6       | phase vocoder FFT             |
| `axum`        | 0.8     | HTTP server                   |
| `tokio`       | 1       | async runtime                 |
| `clap`        | 4       | CLI parsing                   |
| `regex`       | 1       | text normalization            |
| `hound`       | 3       | WAV encoding for /v1/tts      |
| `base64`      | 0.22    | WAV base64 for /v1/tts        |

---

## TODO

### ratts-core
- [ ] `tts/vocab.rs` — VOCAB map + tokenize()
- [ ] `tts/voices.rs` — NpzReader, style_for()
- [ ] `tts/phonemizer.rs` — espeak-ng subprocess + normalize_text + substitutions
- [ ] `tts/model.rs` — ort session + generate()
- [x] `tts/time_stretch.rs` — phase vocoder (out-of-range speeds) — **11 tests green**
- [ ] Unit tests: vocab, voices, phonemizer (time_stretch done)
- [ ] Integration smoke test: phonemize → tokenize → style → generate (if model present)

### ratts-server
- [ ] `state.rs` — Arc<AppState> loading model + voices + phonemizer
- [ ] `routes.rs` — all 4 endpoints
- [ ] `main.rs` — clap CLI + axum router
- [ ] Integration tests (mock or real model)

### ratts-cli
- [x] TUI (70 tests green)
- [ ] Dynamic voice list from `GET /v1/voices`

### Infra
- [x] `scripts/inspect_voices.py` — print NPZ keys + shapes for verification
- [ ] Update CLAUDE.md with `ratts-server` commands
