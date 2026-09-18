# Voice TUI

A terminal client for the voice backend in `../backend/`. Type or paste text,
press a key, hear it. Playback streams to the default output device, and the
clipboard can auto-play whatever is copied.

The binary is currently named `ratts-cli`; it is a single Rust client crate
(`ratts-cli` + `ratts_lib`) that talks HTTP to the Python server. There is no
Rust server — the design note for one is in `DESIGN-NOTE.md` and was never
implemented.

## Build and run

From the `voice/` directory:

```bash
cd tui
cargo build --release
./target/release/ratts-cli
```

The backend should be running on the URL in the preferences file; the default is
`http://127.0.0.1:17493`. Inside Stables, `stables install tts` handles both halves
and `stables tts` opens this UI — but the binary is the client, so running
`ratts-cli` directly does the same thing. That is the name to look for if you want
to start it from a menu, a script, or a process list.

## Keys

| Key | Action |
|---|---|
| `←→↑↓` | Move the cursor |
| `Ctrl+P` / `Enter` | Play the text |
| `Ctrl+Z` / `Ctrl+N` | Undo / redo |
| `Ctrl+D` | Delete the current line |
| `Ctrl+L` | Clear all text |
| `Ctrl+←` / `Ctrl+→` | Speed down / up |
| `Ctrl+↑` / `Ctrl+↓` | Previous / next favourite voice |
| `Ctrl+V` | Voice browser |
| `Ctrl+A` | Toggle auto-play on copy |
| `Ctrl+Y` | Copy the text |
| `Ctrl+S` | Settings |
| `Ctrl+H` | Help |
| `Ctrl+Q` | Quit |

Speed is applied by the backend as a pitch-preserving stretch, so the range is
`0.1x` to `4.0x` with `1.0x` meaning unchanged.

## Preferences

Stored in `~/.stables/tts/config.toml` and written on change:

- `server_url` — where the backend is listening
- `primary_voice`, `favorite_voices` — the startup voice and the cycle order
- `default_speed`, `voice_speeds` — speed overall, and per voice
- `recent_voices` — last ten voices used
- `voice_ratings` — marks set from the voice browser
- `auto_play_on_copy` — read the clipboard out loud when it changes
- `lang_mode`, `lang_voices` — language selection

## Long text

Text past a few hundred characters is played through the backend's PCM streaming
endpoint, so audio starts without waiting for the whole clip to be synthesized.

## What this does not do

There is no speech-to-text. No microphone capture, no recording, no
transcription: the input engines were removed when this code was vendored, along
with the VAD streamer and the WebSocket they used. Text comes from the keyboard,
a paste, or the clipboard.

## Tests

```bash
cargo test
```

Audio is verified by decoding what the server returns; no real output device is
needed.

## Announcements

An agent, a script, or a shell can leave a message for the user instead of
interrupting them. The client asks for messages every couple of seconds **without
taking them** and speaks them only when nothing else is happening — after a piece of
speech finishes, or while you are idle with nothing playing — so a notice cannot
talk over an answer. A message that arrives mid-task waits; the queue keeps it.

Messages are shown in the status line as they are spoken. Which voice reads them is
the **Notice Voice** row in settings: the voice in use by default, or a voice of its
own so notices sound distinct from answers.

To try it:

```bash
stables tts-mcp-ping "the deploy finished"     # queues a ping through the server
```

`--kind question|summary|say` and `--source NAME` are optional; the spoken text is
always the message itself.
