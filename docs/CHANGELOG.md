# Changelog

Every big change gets an entry here, newest first. An entry answers three
questions:

1. **Observed behavior** — what actually happened, with the exact error or
   command output when there was one. This is the issue we found.
2. **Intended behavior** — what the tool should do instead.
3. **Change** — what was done, where, and how it was verified.

"Big" means anything a user could hit and be confused by: runtime templates,
secrets/credentials handling, install/remove, networking, or a bug whose cause
is not obvious from the symptom. Routine refactors and typo fixes do not need
an entry. See `AGENTS.md` → "Change log discipline".

---

## 0.2.0 — the voice stack

Stables can speak. `stables install tts` deploys a local voice backend (Kokoro,
Piper, and Qwen for zero-shot cloning) and installs a terminal client. Weights are
mounted from `~/.stables`, so replacing the container or rebuilding the image never
re-downloads them.

What someone upgrading from 0.1.0 will notice:

- **`stables install` now requires a component.** It used to install everything when
  nothing was named; it prints what can be installed, and `all` still works by name.
- **A voice is a file:** `~/.stables/voices/<name>.<lang>.wav`. A file with no
  language still works and reads as `auto`.
- **The Voicebox component is gone**, replaced by the vendored backend and client.
  `stables install voicebox` is not a command any more, and its container and image
  are not removed for you.
- **Weights are mounted, not baked.** `--no-models` skips fetching them, and an
  install whose weights are missing fails naming the directory rather than waiting
  five minutes for a health check.
- **The release is `make build`, with skills and extensions embedded**, so an install
  arrives with a working agent rather than an empty one. It carries no `models.json`,
  no `secrets.env` and no weights, and — checked with the scanners AGENTS.md names —
  no keys, no private endpoints: the only hosts referenced anywhere in the payload are
  loopback and public ones.
- **Release builds carry nothing of the builder.** `-trimpath -buildvcs=false` for
  Go, and `--remap-path-prefix` for the client baked into it: without them a shipped
  binary contains the builder's home directory, its crate registry and Go module
  cache, plus the commit it was built from. AGENTS.md records the rule and how to
  check it.
- **`stables remove tts` removes the client too, and `--clean` takes the weights.**
  `ratts-cli` is a binary in `~/.local/bin`, outside the compose project, so compose
  never knew about it; removal now deletes it, and a hard removal also takes
  `~/.stables/tts` (weights and Qwen cache, both re-downloadable). Your cloned voices
  are never removed.
- **Tunnels are per component** — `stables ollama tunnel`, `stables tts tunnel` —
  both can be up at once, and tunnelling stops the local stack whose port it needs.
  Closing the tunnel starts it again.

Everything below this entry is the detail behind those.

## One tunnel per component, and tunnelling frees the port it needs

**Observed behavior.** There was one tunnel, in one state file
(`~/.stables/tunnels/current`), defaulting to ollama's port 11434. Forwarding the
voice backend therefore meant `stables tunnel remote host --port 17493`, and because
`Up` cleared whatever was there, doing so silently closed the ollama tunnel. "Where is
this computed" was a single choice at a time, and the voice stack had no lifecycle
commands at all: `stables tts` launched the TUI and nothing could stop or start the
container.

There was also a conflict the tool created and left to the user. A forward binds the
same local port the component listens on, and the ollama component's own comment said
so: *"if the tunnel holds the ollama port, Docker fails with 'address already in use'
and the caller should run `stables tunnel down` first"*.

**Intended behavior.** One tunnel per component, on that component's own port, both up
at once. Tunnelling stops the local stack holding the port and records that it did, so
closing the tunnel puts it back. A tunnel that fails to start restores the local stack
immediately — ending up with neither the remote service nor the local one is the one
outcome worth going out of the way to avoid.

**Change.**

- `internal/tunnel`: kinds (`ollama`, `tts`) with their own state files; `Port(kind)`
  bound to each component's real port; `Active`, `Down(kind)`, `DownAll`, and
  `Up(home, State)` carrying `LocalStopped`. `List` reports every tunnel. The pre-kind
  `current` file is still read *and cleared*, so an upgrade neither forgets a tunnel
  that is already up nor resurrects one that was closed.
- `internal/cli`: `stables tts tunnel|up|down|list` — intercepted before the TUI, which
  would otherwise read "up" aloud — and `stables ollama tunnel`. The single pre-kind spelling — `stables tunnel remote`, and the `--port` flag that
  only it used — was removed rather than kept alongside the new ones: two documented
  ways to do one thing is how they drift apart. and `stables tunnel down` closes
  everything and restores what it stopped. `runDocker` and `localStackRunning` are
  package variables so the decision can be tested without Docker.
- Tunnelling is handled *before* the install check in both components: the point of a
  tunnel is to reach a service that is not here, so closing one must not require a
  local install.

**Verification.** 13 new tests across the two packages: each kind has its own port and
its own file, closing one leaves the other, `DownAll`, the local stop is recorded, the
legacy file is read and cleared, tunnelling stops a running local stack, a tunnel that
fails to start restarts it, a stopped stack is left alone, and `down` restarts only a
stack the tunnel had stopped. The port binding is asserted *against the component's own
default* (17493), so a changed port cannot leave a tunnel pointing at nothing.

```
$ stables tunnel list
no tunnels
$ stables tts tunnel
usage: stables tunnel <command>
  ...
$ stables tts tunnel down
stables: tts tunnel down
```

**Not verified: the tunnel itself against a real host.** Establishing one needs an SSH
target, and nothing in this environment can reach one, so `ssh -L` and the
reachability check are covered only by the code path that fails. The behaviour around
it — the port, the state, the stop and the restore — is tested.
## The weights live on the host, not in the image

**Observed behavior.** Kokoro's 458 MB and the Piper voices were copied into the image
at build time (`COPY models/ /app/models/`). The image therefore carried third-party
weights, every image rebuild re-sent all of it as build context, and the weights could
not be inspected or replaced without rebuilding. The Qwen weights and the user's own
voices were already bind-mounted; the small weights were the last thing baked in.

**Intended behavior.** Everything the stack needs lives under `~/.stables` and is
mounted into the container, so removing the container or rebuilding the image never
re-downloads anything. The image stops carrying weights at all.

**Change.**

- `tts/backend/Dockerfile`: `COPY models/ /app/models/` is gone, as is the argument
  for it. The comment now records why baking was chosen once and why it is not needed:
  the empty-bind-source failure it avoided (Docker creates a missing bind source as an
  empty directory, the backend then answers `/health` with "healthy" and reports zero
  voices) is now *caught* instead of avoided, by `verifyVoices`.
- `assets/tts/docker-compose.yml.tmpl`: mounts `{{ .ModelsDir }}:/app/models:ro` and
  sets `STABLES_VOICE_MODELS` explicitly, so a wrong mount is a missing directory
  rather than a silently different one.
- `assets/tts/.dockerignore`: keeps `models/` out of the build context. Without it every
  build ships ~460 MB to the daemon and discards it.
- `internal/components/tts/tts.go`: `ensureDirs` (extracted, and `ModelsDir` now created
  even with `--no-models`, so the bind source exists as the user's own directory rather
  than being substituted by Docker as root) and `requireModels`, which fails the install
  before the stack starts when the directory holds no weights.

**Verification.** The image falls from 7.48 GB to 7.07 GB — the weights were 458 MB of
it. Speech through the mount: `kokoro: 2.62s at 24000 Hz`. Then the acceptance test:

```
$ docker rm -f stables-tts && docker compose up -d
health: {"status":"healthy","model_loaded":true,"device":"gpu",...}
voices after recreate: 58
same md5 checksums across the recreate:
  9579b435abd94a5af3d00d3cd39df548  ./kokoro-v1.0.onnx
  46398d70bbb12d033e15e601a92cd711  ./piper/sv_SE-lisa-medium.onnx
  1a768f208277aa835e203a969df71345  ./piper/sv_SE-lisa-medium.onnx.json
  20266cf58e93ca2140444b77398aea04  ./piper/sv_SE-nst-medium.onnx
  3a95d9b88bb3214bf4d0d7fcf8a1aea9  ./piper/sv_SE-nst-medium.onnx.json
  29895103243c07818862043c051e1db0  ./voices-v1.0.bin
```

**A limit worth recording.** When the installer runs in a container that is not the
Docker host — as it does in this development environment — `fetchModels` writes to the
*client's* `~/.stables/tts/models` while the container mounts the *daemon's* path of the
same name, so the mount can be empty and the weights are downloaded twice at best. On
one machine, where the installer and the daemon share a filesystem, the two paths are
the same directory and none of this applies. `requireModels` turns the resulting
five-minute wait for `/health` into an error naming the directory.

## Voice files carry their language, and `install` stops choosing everything

**Observed behavior.** Two faults, and the second one caused real damage.

1. A cloned voice was `~/.stables/voices/<name>.wav` and reported `lang: "auto"` —
   "a clone speaks whatever it is asked to". But the client groups voices by
   language, so the clones that the previous change verified landed in no language
   group at all: audible if you knew the id, invisible in the menu.

2. `stables install` with nothing named installed **everything** — stab, ollama, webui
   and tts — because the component argument defaulted to `all`. So a test written to
   check the no-argument path performed a real installation on the shared daemon: it
   pulled and started three containers, created two Docker networks, and *recreated
   the running `stables-ollama`* bound to a directory inside its own temporary home,
   which the test framework then deleted. `ollama list` came back empty and the
   container was writing to a path that no longer existed. The incident is why this
   change exists.

**Intended behavior.** A voice file says what it is. `stables install` on its own is a
question, so it answers with what can be installed.

**Change.**

- `tts/backend/voices.py`: the convention is `<name>.<lang>.<suffix>` —
  `morgan.sv.wav`, `narrator.en.flac`. The language is the last dot-separated part
  when it looks like a language tag (two letters, optionally regional), so
  `my.voice.wav` is a voice called "my.voice" rather than one called "my" in language
  "voice". The id is the stem, so `morgan.sv` and `morgan.en` are two voices and
  asking for `morgan` is not ambiguous. A file with no language still works and reads
  as `auto`, which is what the directory held before. `.flac` is accepted alongside
  `.wav`, the transcript keeps the same stem (`morgan.sv.txt`), and `display_name`
  turns dots into spaces because `str.title()` capitalises after a dot too.
- `internal/cli/cli.go`: `cmdInstall` requires a component and prints
  `usageInstall()` — the components with a line each — when none is given. `all`
  remains, but only when asked for by name. `cmdUpdate` still defaults to everything
  *installed*, which is its job.

**Verification.** 154 backend tests (8 new, covering the parse, the regional tag, the
dotted name, two languages of one name, the address including the language, the
stem-shared transcript and `.flac`), and the Go suite passes in milliseconds instead
of 492 seconds, which is itself the evidence that no test can install anything now.

**Daemon state, since the incident touched shared resources.** The two containers the
test created (`stables-open-webui`, `stables-tts`) and the networks it and the
removed voicebox component left behind (`stables-webui`, `stables-tts_default`,
`stables-voicebox`) were removed. `stables-ollama` was reinstalled, bound to a real
install path, and lists the user's `qwen2.5:0.5b` again:

```
NAME            ID              SIZE      MODIFIED
qwen2.5:0.5b    a8b0c5157701    397 MB    13 days ago
bound to: /home/coder/.stables/ollama/data
```

Worth recording for the next bind-mount change: the daemon host reaches this tree as
`/home/arty/claude-workspace/stables_oss`, not `/workspace/stables_oss` — `/workspace`
exists only *inside* the containers, so a bind source written as `/workspace/...`
resolves to an empty directory on the host.

## Voice cloning verified end to end, in the real container

**Observed behavior.** The cloning path had never been run. The engine's use of the
`qwen-tts` API was tested against a fake package, and `--preload` was tested at the
level of the command it runs and the Dockerfile order it depends on. The chain from a
reference WAV to the API listing a selectable voice was unproven, so "cloned voices
work" rested on inference.

**Intended behavior.** A WAV in the voices directory is a selectable voice, and the
cloning engine can speak with it.

**Change.** `agent_tools/check_clone.py` runs inside the image and checks the chain in
the order it can break: the base engine speaks (so the reference audio is real speech
rather than a fixture, and no third-party audio enters the repository), both reference
files land where the registry looks, `preload()` fetches the checkpoint through the
installer's own code path, and each name clones and produces audio that is neither
silent nor the reference played back.

**Verification.** The image was built with `WITH_QWEN=1` — `stables-tts-qwen:local`,
8.17 GB against 7.01 GB without the extra — and both runtimes see the GPU
(`torch.cuda.is_available()` true, `CUDAExecutionProvider` offered). Measured on the
4090:

```
Kokoro reference   4.03s at 24000 Hz, rms 0.0785
preload("0.6B")    27s (checkpoint fetched into the mounted cache)
clone heart-a      1.76s of audio in 9.5s  (cold: includes the load)
clone heart-b      2.08s of audio in 1.7s  (warm)
served voices      [('heart-a', 'qwen', 'auto'), ('heart-b', 'qwen', 'auto')]
```

A warm clone runs at 0.85x real time, which is why the client streams rather than
waiting for a whole utterance.

**Two things this found, both left open:**

1. **Corrected:** this check first reported that every start logs an onnxruntime
   error —

   ```
   Failed to load library libonnxruntime_providers_tensorrt.so with error:
   libnvinfer.so.10: cannot open shared object file: No such file or directory
   ...Falling back to ['CUDAExecutionProvider', 'CPUExecutionProvider'] and retrying.
   ```

   — and that was wrong about the product. The server never does this:
   `start_server.py` sets `ONNX_PROVIDER` from `device.report()` before any engine
   loads a model, so TensorRT is not in the list it hands to onnxruntime. The error
   came from ad-hoc `docker run --entrypoint python3` sessions, which bypass that file
   and leave the variable unset, so onnxruntime tries its TensorRT provider, cannot
   load `libnvinfer`, and retries with CUDA.

   The trap is worth keeping: any diagnostic that creates a session of its own
   reproduces an alarming error that the stack does not have, so this script now makes
   the same provider choice the server makes.

2. Cloned voices report `lang: "auto"` — deliberate, "a clone speaks whatever it is
   asked to" — while the client's language tabs filter on `voice.lang`. So the clones
   this verification created may not appear under any language tab.

## The usage text promised flags that do not exist, and the client hid its notices

**Observed behavior.** `stables install` finds its flags by scanning the argv rather
than by parsing it, so an unrecognised flag was ignored without a word. The usage
line then advertised `--no-preload` — the real flag is `--preload` — and `--image`:

```
$ stables install tts --no-preload
stables: install complete          # nothing was preloaded, nothing was said
```

Two more dead flags, `--image` and `--voice`, were listed in `flagTakesValue`, so
their values were skipped for the component lookup while nothing ever read them.
Separately, the client kept the three most recent announcements and displayed only
the newest, so the bound bought nothing.

**Intended behavior.** The usage lists what the command accepts. An unknown flag is an
error, because a flag that silently does nothing is worse than one that does not
exist. Every notice that is kept is shown.

**Change.**

- `internal/cli/cli.go`: the install usage is corrected (`--no-preload` →
  `--preload`, `--image` dropped, `--no-container` and `--no-models` added), update
  gains `--build`, and `knownInstallFlags` + `rejectUnknownFlags` make an unknown
  install flag exit 2 with the usage.
- `internal/cli/cli.go`: `--image` and `--voice` leave `flagTakesValue`. `--voice` was
  a leftover of the component that was removed.
- `internal/cli/cli_test.go`: the test that pinned the dead `--image` behaviour now
  uses a real flag, the "unknown component" case no longer spells the removed
  component's name, and a new test covers the ghost flag directly.
- `tts/tui/src/ui/main_view.rs`: `render_notices` gives announcements their own rows,
  up to `NOTICE_ROWS`, and the status bar is the status again rather than the newest
  notice.
- `tts/tui/src/main.rs`: the announcement poll waits while the server is unreachable,
  on a watch channel published by the health check, instead of collecting a refused
  connection every two seconds.
- `docs/specs/2026-09-17-voice-stack-design.md` deleted: it was the plan for work that
  is finished. The changelog entry recording the removal stays, because it is the
  record of why the component went, and "remove references" did not obviously mean
  history.
- `stab/internal/project/controller.go`: gofmt, which it had been failing.

**Verification.** Both Go modules and the TUI build and test clean, and the new test
fails if the ghost flag is ever accepted again:

```
--- PASS: TestInstallRejectsAFlagThatDoesNotExist
```

## The client reads announcements aloud

**Observed behavior.** Nothing consumed the announcement queue. `notify` — over REST
or MCP — queued a message and it stayed there. The queue had a producer and no
consumer, so "an agent can tell the user something" was a place to put messages.

**Intended behavior.** The client asks for waiting messages every two seconds
**without taking them**, and speaks them only when nothing else is happening: never
over an answer, never with a menu open, never two at once. The message is shown in
the status line as it is spoken, because on headphones an announcement that is only
spoken is lost. Which voice reads it is its own setting, defaulting to the voice in
use so a fresh install sounds consistent.

**Change.**

- `src/client/types.rs`, `src/client/http.rs`: `Notification`,
  `NotificationsResponse`, and `notifications(consume)`. A failed read is an
  **error, not an empty list**: told they are the same, the client would quietly
  stop announcing anything.
- `src/main.rs`: a poll task (peeks, and wakes early if the server URL changes so a
  repointed client does not poll the old one for the rest of the interval), the
  `NoticesWaiting`/`Notice` events, and the announce path, which **drains the queue
  only at the moment it speaks** — a notice arriving during a task is not read into
  the void. The PCM forwarding is extracted into `forward_stream` and shared with
  ordinary speech, so a notice cannot take a different path through the audio and the
  chunk-alignment bug cannot exist in two places.
- `src/app.rs`: `notice_voice`, a bounded `notices` list (3), `announcing`,
  `ready_for_notice()`, `push_notice`, `notice_voice_id/label`,
  `cycle_notice_voice`. The settings action is **appended** (index 8), so no existing
  row renumbers.
- `src/config/prefs.rs`, `src/ui/settings.rs`, `src/ui/main_view.rs`: the persisted
  setting, the "Notice Voice" row, and the status line preferring the newest notice.
- `tts/tui/README.md`: an Announcements section, including how to try it.

**Verification.** 161 tests, 17 of them new: the notice-voice cycle and its labels,
the stale-voice fallback, the bounded notice list, and `ready_for_notice` refusing to
interrupt Generating, Streaming, Settings, VoiceMenu or Help — or a notice already
being spoken, or an unreachable server. Against a mock server: the `consume` parameter
on the wire, and that a 503 surfaces as an error rather than an empty list. That mock
caught a real misunderstanding too — mockito matches a query-less mock as *no query at
all*, so the failure was in the test, not the client.

**Not verified: the sound.** This environment has no audio device and no terminal for
the TUI, so the loop is verified at the level of its parts and its HTTP contract, not
by ear. To hear it:

```
stables install tts          # or: cd ~/.stables/tts && docker compose up -d
stables tts                  # in one terminal
stables tts-mcp-ping "the deploy finished"   # in another
```

## `stables tts-mcp-ping` leaves a message for the user

**Observed behavior.** The announcement queue had exactly one producer — an MCP
client — and no way to reach it from a shell. Trying the loop meant hand-writing
JSON-RPC, so the path an agent uses had been exercised by nothing but the probe.

**Intended behavior.** A command that queues a message, and that drives the *real*
path rather than a parallel one. It calls the MCP endpoint an agent will call, not a
private REST shortcut, so a ping and an agent's message cannot drift apart.

**Change.**

- `internal/cli/ping.go`: `parsePingArgs`, `ttsPing`, `pingQueue`. The text is
  positional and may be unquoted — refusing to send a message over quoting is a poor
  reason to fail. The port comes from the install manifest, falling back to the
  component default; `--url` overrides it for a server elsewhere. `--kind` defaults
  to `ping`, `--source` to the hostname.
- `internal/cli/cli.go`: registered as `tts-mcp-ping`. `stables tts` and this command
  were both missing from the usage text, so both are now listed.
- `internal/cli/ping_test.go`: eight tests.

**Verification.** The tests pass, and so does a live run against a real backend:

```
$ stables tts-mcp-ping "hello from the shell, the deploy finished"
queued #1 (ping) — the voice client reads it aloud when you are idle

$ curl -s "localhost:17493/v1/notifications?peek=true"
{"items":[{"id":1,"text":"hello from the shell, the deploy finished",
           "kind":"ping","source":"hacuru",...}], "pending":0, "capacity":50}
```

Bad input exits 2 with the usage and **never reaches the server** — asserted with a
handler that fails the test if it is called, because a validation bug that still
queues the message is worse than one that does not. A refused connection exits 1 and
names the fix rather than only the symptom:

```
stables: Post "http://127.0.0.1:1/mcp": dial tcp 127.0.0.1:1: connect: connection refused
  nothing is answering there — start the server with: stables install tts
```

## Streaming starts speaking sooner

**Observed behavior.** Streaming was already sentence-by-sentence, so audio started
before the rest of the text was synthesized — but the opening *sentence* was sent
whole. Time to first audio therefore scaled with the length of that sentence: an
opener with three clauses kept the listener waiting for all three, roughly 1.4 s of
synthesis before any sound for a 200-character sentence, and worse for a longer one.

**Intended behavior.** First audio is bounded by a cap, not by how long the opening
sentence happens to be. The opening chunk is cut at a comma, which costs nothing
audible because a comma is already a pause. Only the opening chunk is cut: later
ones are synthesized while earlier ones play, so their length is invisible, and
leaving them sentence-sized costs fewer engine calls for the same audio.

Two fail-safes, both deliberate. A sentence with no usable pause is spoken whole —
slower to start, still correct. And a remainder too short to fill its own engine call
rides along with the next sentence, because a call costs about the same whatever the
text, and a fragment of less audio than the call takes makes the stream fall behind.

**Change.**

- `tts/backend/text.py`: `split_for_streaming` and `_clause_cut`, with
  `_FIRST_CHARS` (150) and `_MIN_HEAD` (40). The cut is the *last* comma that
  satisfies both, not the first: cutting at "Well," would start sooner and then
  stall.
- `tts/backend/routes/tts.py`: the streaming endpoint calls it. The one-shot
  endpoint is untouched — it has no first-audio problem.
- `tts/backend/tests/test_stream_chunks.py`: seven tests, including that cutting
  drops nothing (whitespace-normalised join equals the input) and that a 1000-char
  opener costs the listener no more than a 178-char one.
- `test_routes.py`: the streaming route must send the cut opening.

**Verification.** 146 backend tests pass. The cut is bounded rather than
proportional, measured:

```
 178 chars -> first chunk 143 chars, 2 chunks
 617 chars -> first chunk 143 chars, 2 chunks
1024 chars -> first chunk 143 chars, 2 chunks
```

The route test asserts the whole text still reaches the engine, in order, because a
dropped clause is the failure this could plausibly have introduced.

## `stables install tts --preload` fetches the cloning weights at install time

**Observed behavior.** The Qwen engine could preload a checkpoint — `preload()` exists
and is tested — and nothing called it. On demand means the first request for a cloned
voice waits for one to three gigabytes, in the middle of a sentence, which is the
one moment that wait is least welcome. The installer's flags were `--port`,
`--listen`, `--gpu`, `--name`, `--no-container` and `--no-models`: there was no way
to ask for the download at a time when waiting is expected.

**Intended behavior.** `--preload` does it at install time, and says so, because it
is gigabytes. It also puts the engine in the image, since fetching weights for an
engine that is not installed would achieve nothing.

**Change.**

- `tts/backend/Dockerfile`: `ARG WITH_QWEN=0` and the extra installed behind it,
  **before** `onnxruntime-gpu`. That order is not cosmetic: `qwen-tts` depends on
  plain `onnxruntime`, so installing it afterwards would replace the GPU build with
  the CPU one — undoing the pin that cost 10× to get right.
- `assets/tts/docker-compose.yml.tmpl`: `WITH_QWEN` in the build args, from the
  same `QwenEnabled` that already gates the container's environment.
- `internal/components/tts/tts.go`: `Options.Preload` sets it, and after the install
  verifies it can speak, `preloadQwen` runs the image's own engine through
  `compose run --rm --no-deps` — the same mounts the server uses, so what it fetches
  is exactly what the server will find. It fetches without loading: the server
  starts cold regardless, and holding a checkpoint in a container that is about to
  exit would only slow the exit.
- `internal/cli`: `--preload`, off by default.

**Verification.** The Go suites pass (8 packages), including three new: the compose passes
`WITH_QWEN` both ways, the cloning install sits behind its guard **and before
onnxruntime-gpu**, and cloning is off unless asked for. The rendered Dockerfile
order was read as well as asserted:

```
41  RUN sed ... > requirements-docker.txt        base requirements
48  pip install -r /tmp/requirements-qwen.txt    cloning, behind WITH_QWEN
    pip install "onnxruntime-gpu==1.20.0"        last, still
```

**Not verified:** the download itself. It needs the image rebuilt with `qwen-tts`
(a transformers pin, gradio, and the wheel set) and then gigabytes from Hugging
Face, so the fetch path is tested at the level of the command it runs and the
ordering it depends on, not end to end.

## Stretch quality is selectable in the client

**Observed behavior.** The server gained quality levels and a device-based default,
and the client had no way to reach them: `stables tts` sent nothing, so a CPU
machine was always medium and a GPU machine always high, with no override and
nothing in the UI admitting the trade existed.

**Intended behavior.** A setting the user can reach — `auto`, `low`, `medium`,
`high`. Auto is the default and is sent as *nothing*, because the server is the only
party that knows whether it has a GPU. Choosing a level sends it. The row shows the
cost rather than the bare name (`medium · 16 passes`), because "quality" with no
price attached is not something anyone can make a decision about.

**Change.**

- `config/prefs.rs`: `stretch_quality`, serde-defaulted to `"auto"`. A preferences
  file written before this existed has no field, and an empty value reads as
  automatic too.
- `app.rs`: the state carries it; `SETTINGS_ACTION_COUNT` 7 → 8 with the new row
  **appended**, so no existing action renumbers; `cycle_stretch_quality()` steps
  auto → low → medium → high → auto; `quality_label()` produces the row text.
- `ui/settings.rs`: the rows tuple carries `Option<String>` instead of a count, so
  the row can show a level and the clearing rows can show their numbers unchanged.
- `client/types.rs`, `client/http.rs`, `main.rs`: `quality` on both request types,
  omitted when absent; `generate_opts` and `stream_pcm` accept it; the call sites
  pass `quality_arg()` and the choice is written back to preferences.

**Verification.** 144 TUI tests pass, eight of them new: the cycle order and its
wrap, the label for every level, an unset value reading as automatic, and that an
automatic request serialises *without* the field while a chosen one carries it.
132 backend tests unchanged. The diff was read rather than trusted: both request
bodies forward the argument and both call sites pass it, which is the failure this
change could plausibly have — looking right and sending nothing.

**Process note.** The first attempt at this used regexes to insert struct fields,
and `TtsRequest {` also matches `pub struct TtsRequest {`, so it injected a field
into the type definition. That attempt was reverted rather than repaired. This one
edits by exact anchor or by brace matching, and `cargo fmt` was kept out of the files
the change does not touch, so the diff is the change.

## Piper reloaded its model on every request

**Observed behavior.** Two identical Swedish requests, back to back:

```
request 1: 783 ms
request 2: 823 ms      ← nothing was warmed
cache contents: (empty)
```

The engine cached the model *path*, and synthesis shelled out to the `piper` CLI,
which reads the 63 MB of weights from disk every time. `PiperVoice.load()` alone
measures ~600 ms, so an ordinary sentence was mostly model loading. Kokoro does not
work this way — it loads once (991 ms first request, then 330 ms) and keeps the
model — and neither does Qwen, which holds one checkpoint resident by design. Piper
was the odd one out, on the engine whose voices the user reaches for whenever the
text is Swedish.

**Intended behavior.** One loaded model per voice, kept for as long as it is the one
in use, and replaced when the voice changes — with a bound, because each is ~63 MB
and an unbounded cache is how a server grows into memory it never gives back.

**Change.**

- `tts/backend/engines/piper.py`: synthesis runs through the Python API
  (`PiperVoice.synthesize`, returning chunks whose `audio_float_array` is
  concatenated), with the loaded voice held in an `OrderedDict` bounded by
  `VOICES_IN_MEMORY` (2): the least recently used voice is dropped when a new one
  arrives.
- The CLI stays as the fallback rather than being deleted — it needs no Python API
  and it is what worked before — and it is what runs if the API is missing or
  returns no audio, with one line on stderr the first time so a broken fast path
  cannot go unnoticed.
- `available()` is satisfied by either path, since requiring the CLI would report a
  working install as unavailable.
- The loader goes through `_load_piper_voice`, so tests replace that function
  instead of faking the `piper` module in `sys.modules` — which outlives the test
  and breaks the ones after it, as happened once already.

**Verification.** Real voice, real model:

```
in-process   request 1  729 ms   (loads the voice, once)
             request 2   82 ms
             request 3   82 ms
CLI          request 1  842 ms
             request 2  899 ms
```

So ~10× on warm requests, and the audio is 2.7-3.3 s of speech rather than silence
— the first attempt at this API passed arguments in the wrong positions and
returned zero samples, which is why a non-empty result is now asserted both in the
timings and in the tests. 137 backend tests pass, five of them new: loaded once for
many requests, non-empty audio, chunks joined with the rate kept, the bound holding
under eviction, and the silent-API fallback.

## Stretch quality is a level, and the device picks the default

**Observed behavior.** The Griffin-Lim iteration count was one number for
everybody. Measured on speech, 32 iterations cost a GPU 26 ms and a CPU 2.84 s for
the same clip — so the same setting was free on one machine and a visible wait on
the other. There was no way to ask for the cheaper one, and no way to know that the
choice existed.

**Intended behavior.** Quality is a level the caller can name — `low` (8
iterations), `medium` (16), `high` (32) — and when nobody names one the *server*
picks by the hardware it is running on: high where there is a GPU, because the
difference is free there, and medium on a CPU, where those iterations are 2.84 s of
somebody's time. Everyone wants the best quality; not everyone wants to pay for it
in waiting, so it is a choice rather than a constant.

**Change.**

- `tts/backend/stretch.py`: `QUALITY_ITERATIONS` (low 8, medium 16, high 32),
  `default_quality()` choosing by `device.report()`, and `iterations_for()` which
  falls back to the default rather than raising — an unknown level reaching it
  means an internal caller, and a traceback inside a request thread is a worse
  answer than the default.
- The level is threaded through `time_stretch_audio`, `time_stretch_tensor`,
  `_time_stretch_torch` and the chunked path, so a long clip is stretched at the
  requested quality throughout.
- `tts/backend/routes/tts.py`: `quality` on the request, typed as
  `Literal["low", "medium", "high"] | None` so a typo is a validation error naming
  the allowed values rather than a silent fallback.
- `agent_tools/bench_stretch.py`: takes `--quality`, and measures whatever the
  backend would choose when it is not given.

**Verification.** `pytest backend/tests` → 132 pass, including the level table, the
device default for both a GPU and a CPU, the unknown-level fallback, and that the
level changes fidelity but not duration. The level reaches the stretch from a
request. At 1.0× nothing is stretched and nothing is spent, whatever the level.

## The stretch uses 16 Griffin-Lim iterations instead of 32

**Observed behavior.** Every stretched request ran 32 Griffin-Lim iterations. On
speech (`af_heart`, ~13 s at 1.2×) that is **2.84 s on CPU** and 55 ms on a GPU.
Measuring the parts showed where it goes: roughly 80 ms per iteration on CPU,
about two thirds of the total, with the STFT and phase-vocoder step taking the
remaining ~0.3 s. 32 was more than the reconstruction needs.

**Intended behavior.** Halve the cost, and say what the halving changes rather than
claiming it changes nothing. The magnitude difference is measurable; whether it is
audible is a judgement only listening makes, so samples for both settings are kept.

**Change.** `GRIFFIN_LIM_ITERATIONS = 16` in `tts/backend/stretch.py`, named and
documented with the numbers that justify it. `agent_tools/bench_stretch.py` now
measures whatever the backend is set to, instead of a copy of the value.

**Verification.** Best of three, same speech, both devices:

```
                    cpu              cuda
x16 (now)        1.55 s           29.3 ms
x32 (before)     2.84 s           54.9 ms      spectrum 2.8 dB off
x8               0.89 s           16.4 ms      spectrum 3.3 dB off
```

So 1.8× on CPU and 1.9× on the GPU. Level and dropouts are unchanged
(rms 0.0764 against 0.0767; longest near-silent run 21-26 ms against 27-46 ms). At
1.0× the stretch still costs nothing at all, so this only affects speeds other
than 1.0×.

**Scope, stated plainly:** this is a CPU-side win. On a GPU the stretch was already
tens of milliseconds, and `torch.compile`, measured in the entry above, remains the
wrong tool — it is slower on CPU and only matches fewer iterations on GPU while
paying seconds of warmup. The larger prize is still the vocoder's own phase, 14×
faster, which currently produces a quarter-level signal and needs its normalisation
worked out.

## Measuring the stretch: `torch.compile` is the wrong tool here

**Observed behavior.** Nothing was known beyond one number — 13 ms at 1.2× on a
4090 — which says nothing about the machines that matter, where there is no GPU at
all. The question was whether `torch.compile` could make the phase-vocoder stretch
cheaper, since it runs on every request whose speed is not 1.0.

**Intended behavior.** A number for CPU and for GPU, per candidate, with something
said about quality rather than speed alone — a stretch that is fast and wrong is
not an optimisation.

**Change.** `agent_tools/bench_stretch.py`: times the current implementation against
alternatives on both devices, and for each reports duration, mean log-spectral
distance from the current output, whether the level collapses, and the longest
near-silent run (which is what a broken reconstruction sounds like). Writes a WAV
per variant, because these proxies cannot tell you whether it sounds right.

Speech sample, 3.84 s at 22.05 kHz, best of three:

```
cpu                                            cuda
griffin-lim x32 (current)     332 ms          12.6 ms
griffin-lim x8                127 ms           3.6 ms     spectrum 3.4 dB off
istft (vocoder phase)          29 ms           0.7 ms     spectrum 12.6 dB off, rms 0.067 vs 0.240
current + inference_mode      351 ms          12.3 ms
torch.compile of current      547 ms           3.2 ms     first call 8.8 s / 6.6 s
speed = 1.0                     0 ms           0 ms
```

**What this says.**

- **`torch.compile` does not pay.** On the CPU, which is the target, it is about
  1.6× *slower* than what is there now. On the GPU it matches simply lowering the
  Griffin-Lim iterations, at the cost of a first call measured in seconds — and
  because the phase vocoder's Python frame loop varies with the length of the
  audio, a compiled graph would be recaptured as requests come in.
- **Griffin-Lim's iterations are the cost.** 32 iterations are roughly two thirds of
  the CPU time; eight of them are 2.6× faster with a 3.4 dB mean log-spectral
  difference. Whether that is audible is a listening question, not a number.
- **The interesting candidate is the one that is broken.** Reusing the phase the
  vocoder already propagated instead of re-deriving it with 32 iterations is 11×
  faster on CPU — and produces a signal at a quarter of the level with 67 ms of
  near-silence in it, so it is currently wrong, not merely different. That is a
  scaling problem rather than an algorithmic one: torchaudio's `TimeStretch`
  expects a spectrogram normalised in a particular way, and reconstructing it
  directly needs that undone.
- `inference_mode` changes nothing measurable. Not worth the line.

**Verification.** Samples for listening are in `.stretch-samples/` (gitignored).
`cargo`-heavy tooling is not involved; the backend suite is unchanged at 125.

## The GPU was reserved and unused: synthesis ran on the CPU

**Observed behavior.** A four-second clip took about 690 ms on a machine with an
RTX 4090, and every sign said the GPU was in use — the container held an explicit
device reservation, torch reported the card, and onnxruntime listed the CUDA
provider:

```
Failed to create CUDAExecutionProvider. Require cuDNN 9.* and CUDA 13.*.
available: ['TensorrtExecutionProvider', 'CUDAExecutionProvider', 'CPUExecutionProvider']
session providers: ['CPUExecutionProvider']
```

That last line is the only one that matters. `pip install onnxruntime-gpu` now
gives a build made for CUDA 13 and cuDNN 9, while the image takes torch from the
cu124 index, which is CUDA 12.4. The provider could not initialise, said so once, in
a warning, and fell back to the CPU — while remaining in
`get_available_providers()`. So the two things that look like evidence of GPU use
were both true, and the work was on the CPU.

Worth recording how this was found: not by reading the code, but by timing the
things I had assumed were slow. The phase-vocoder stretch costs **13 ms** at 1.2×
on this GPU and **0 ms** at 1.0×, where it returns early — so the stretch was never
the cost, and the suspicion fell on synthesis instead.

**Intended behavior.** The pinned versions agree with each other, and the session
gets the provider that was asked for. A provider that appears in a list is not
evidence that it works.

**Change.**

- `tts/backend/Dockerfile`: `onnxruntime-gpu` pinned to **1.20.0**, the CUDA 12
  line, with the coupling written down next to it — bump this and the torch index
  together, and check the session's providers rather than the list.
- The torch, requirements and onnxruntime installs are now three separate layers,
  so changing a pin in one does not re-download gigabytes from another.

**Verification.** The same clip, in the same image, on the same GPU:

```
session providers: ['CUDAExecutionProvider', 'CPUExecutionProvider']
create() run 1: 373 ms   (cold, includes CUDA context)
create() run 2:  75 ms
create() run 3:  67 ms
```

against `689 / 684 / 662 ms` before: roughly ten times faster. The test image was
removed afterwards.

**Still wrong, and next:** `/health`'s `device` field derives "gpu" from torch plus
the *available* provider list, so it reported GPU for the whole of this. The
header was therefore wrong in the same way. It needs the loaded engine's session
providers, not the list, which is the next change.

## The voice menu says which engine speaks a voice

**Observed behavior.** The row gave the grade, the name, the region and the gender,
but not the engine — so nothing explained why one voice is a fraction of a second
and another takes minutes. The engine was only visible by asking `/v1/voices`.

**Intended behavior.** The row says it, because it answers the question a user
actually has: a Kokoro or Piper voice is fast either way, while a cloned voice runs
on Qwen, which is minutes per sentence without a GPU.

**Change.** `engine_label` in `tts/tui/src/ui/voice_menu.rs` renders the engine at
the end of the row, dimmed — it is context, not a choice, since the voice decides
it. Qwen on a CPU backend is called out as `Qwen (slow on CPU)`, using the device
the header already gets from `/health`. An engine the backend did not name renders
as nothing rather than a stray separator, so an older backend is unaffected.

**Verification.** `cargo test` → 136 pass, including the naming, the CPU warning and
the empty case. Rows now read:

```
► ♥ 🥈 Lisa            SE ♀  Piper
  · 🥈 Heart           US ♀  Kokoro
  · 🥈 Morgan          —  ♀  Qwen (slow on CPU)
```

## The Swedish voice was labelled "US" in the voice menu

**Observed behavior.** The voice menu showed the Swedish Piper voice as American:

```
│  ♥ 🥈 Lisa            US ♀        │
```

**Intended behavior.** The label says where the voice is from. For a Swedish voice
that is `SE`.

**Change.** `Voice::region_label` in `tts/tui/src/client/types.rs` was
`if self.lang == "en-gb" { "UK" } else { "US" }` — a two-branch assumption that
everything not British English is American, which is the one thing a region label
exists to get right. It now reads the voice: a Piper id carries its locale
(`sv_SE-lisa-medium` → `SE`, `de_DE-thorsten-medium` → `DE`), a Kokoro id says it
in its first letter (`af_*` → `US`, `bm_*` → `UK`), anything else falls back to the
voice's own language code rather than to US, and a cloned voice — which is from
nowhere in particular — shows `—`.

**Verification.** `cargo test` → 133 pass, including a new case for each shape:
`sv_SE-lisa-medium` → SE, `en_US-amy-low` → US, `de_DE-thorsten-medium` → DE,
`af_heart` → US, `bm_george` → UK, an unrecognised id with `lang: "ja"` → JA, and a
clone → —.

## The engine is chosen from the voice, never asked for

**Observed behavior.** The API accepted an `engine` with every request, and the
client filled it in — derived from the language tab's engine, so it was passed on
every generation. Nothing could use it well: a voice belongs to exactly one engine
(Kokoro voices to Kokoro, Piper's to Piper, a clone to the cloning engine), so the
name was either redundant with the voice or, if it disagreed, wrong. The registry
already ignored a hint that named an engine without that voice, which is the tell:
the parameter existed but could not do anything.

**Intended behavior.** A request says what to say and in whose voice. The server
resolves the engine, because it is the only party that knows which one can speak
that voice. The engine used is reported back, as information rather than a knob.

**Change.**

- `tts/backend/routes/tts.py`: `TTSRequest` loses `engine`; `_engine_for` takes only
  the voice and resolves it. An older client still sending the field is unaffected —
  unknown fields are ignored — and gets the engine the voice requires.
- `tts/tui`: `TtsRequest` loses the field, `generate_opts` loses the parameter, and
  `start_generation` no longer unwraps a per-language engine to forward. The request
  cannot name one now, so no future caller can be confused into thinking it can.
- `tts/backend/README.md`: the request table says the voice decides, and that
  `/v1/voices` reports each voice's engine and the response echoes the one used.
- Design doc: the plan for `voice_engines` (and `voice_sizes`) is struck out, with
  the reason. Per-voice *speeds* stay — that is a real preference.
- Tests: a request's serialised body must not contain `engine`, while still naming
  the voice.

**Verification.** Against the real backend and models, with no engine in any
request:

```
English preset     -> engine=kokoro voice=af_heart          24000 Hz
Swedish preset     -> engine=piper  voice=sv_SE-lisa-medium 22050 Hz
Swedish, no lang   -> engine=piper  voice=sv_SE-nst-medium  22050 Hz
```

125 backend and 131 TUI tests pass.

## The voice menu had no language tabs, and its tabs did not filter

**Observed behavior.** The Swedish voices could not be found in the TUI's voice
menu, although the server had them: the image contains
`/app/models/piper/sv_SE-lisa-medium.onnx` and `sv_SE-nst-medium.onnx`, and
`/v1/voices` lists both. The menu's tabs are the languages from
`/v1/voices/languages`, and there were none — only "Favorites" and "All" — so there
was no Swedish tab to look under.

Two causes, and the second was hidden by the first.

The per-language entries were hand-built in the route and carried `{id, name,
gender, engine, cloned}`. The client's `Voice` requires `lang` and `grade`, so
deserialising the response failed, and a failed response means an empty language
list — and with it, no tabs. Every entry was missing `lang`, so this had never
worked, not even before the endpoint was rewritten.

Underneath that, `voices_in_current_category()` ignored the selected tab, with a
comment saying language filtering was handled by the backend — it was not. Had the
tabs appeared, every language tab would have listed all 56 voices, so a Swedish
voice still could not be found by looking under Swedish.

**Intended behavior.** The language list is what the menu shows, every entry is a
complete voice, and a language tab lists that language.

**Change.**

- `tts/backend/routes/voices.py`: the grouped entries are now the same objects as
  `/v1/voices` (via `_as_json`), so they carry `lang`, `grade`, `engine` and
  `cloned` — the fields a voice has, because a client reading them as voices needs
  all of them.
- `tts/tui/src/client/types.rs`: `lang`, `gender`, `grade` and the new `engine`
  default when absent, so one partial entry can no longer take the whole list down.
- `tts/tui/src/app.rs`: a language tab filters by `lang`. "Favorites" and "All" are
  unchanged.
- Tests: the route test asserts every grouped entry carries the full voice shape
  and that its `lang` matches the group; the app test asserts the `sv` tab yields
  exactly the Swedish voices and that "All" still shows everything, so nothing
  became unreachable.

**Verification.** Against the real backend and models, `/v1/voices/languages` now
reports the tabs `['en', 'en-gb', 'sv']`, with `sv` holding both Swedish voices and
each entry complete:

```json
{"id": "sv_SE-lisa-medium", "name": "Lisa", "lang": "sv", "gender": "f",
 "grade": "B", "engine": "piper", "cloned": false}
```

125 backend tests and 129 TUI tests pass. The public binary was rebuilt, since it
carries the client.

## The header says whether it is on the GPU or the CPU

**Observed behavior.** Nothing reported it. The backend happily served from the CPU
with the CUDA execution provider compiled in and listed as available, and the TUI
showed a green dot for "server is up" either way — so a GPU box quietly running on
CPU looked identical to one using its GPU, and the only symptom was slowness.

**Intended behavior.** The client asks the server and shows the answer. "GPU" means
the work can actually run there, not that a provider appears in a list.

**Change.**

- `tts/backend/device.py`: `report()` returns `device` ("gpu"/"cpu"), `gpu_name`,
  and the provider the engines will use. `device` is "gpu" only when **both** halves
  can reach it — the stretch is torch, synthesis is onnxruntime — because either one
  alone would report GPU while half the work ran on the CPU. Cached, since the first
  CUDA call costs about a second and `/health` is polled every few seconds.
- `tts/backend/routes/health.py`: `/health` carries it.
- `tts/tui`: `HealthResponse` gains optional `device` and `gpu_name`, the health poll
  asks for the detail, `AppState` holds it, and the header renders a `GPU` / `CPU` /
  `--` badge beside the server dot. `--` is deliberately distinct: unknown is not
  the same as CPU.
- Tests: five on the device rule, including the trap where torch sees the GPU and
  onnxruntime does not, and two that a health response with and without the field
  still parses.

**Verification.** The probes are replaced rather than the modules they import: an
earlier version of these tests faked `torch` in `sys.modules` and broke fourteen
unrelated tests, because the replacement outlived the test. With them patched,
124 backend tests pass. `/health` now answers
`{'device': 'gpu', 'gpu_name': 'NVIDIA GeForce RTX 4090', 'onnx_provider': 'CUDAExecutionProvider', ...}`.

## The image is built for the machine it runs on: CPU hosts no longer get CUDA

**Observed behavior.** The image installed CUDA torch and `onnxruntime-gpu`
unconditionally and baked `ONNX_PROVIDER=CUDAExecutionProvider` into itself. On a
host with no GPU that was 4.6 GB of CUDA wheels nothing could use —
`stables-tts:local` measured **7.01 GB** — plus an onnxruntime with a CUDA provider
compiled in and no CUDA to run it on, which is exactly the trap the header's device
field exists to expose. The two builds also shared a cache key, so a CPU host could
be handed the CUDA layers.

**Intended behavior.** `stables install tts` already decides whether a GPU is
present (`nvidia-smi`, or `--gpu on/off`) — that is what drives the device
reservation. The build follows the same decision: CUDA torch and `onnxruntime-gpu`
where there is a GPU, CPU torch and the plain onnxruntime where there is not.
`onnxruntime-gpu` stays last so it still replaces the CPU onnxruntime that
kokoro-onnx pulls in. The execution provider is chosen at runtime from what is
actually available instead of being baked, so a CPU image cannot ask for CUDA.

**Change.**

- `tts/backend/Dockerfile`: `ARG USE_GPU=1`; torch comes from the cu124 index or the
  cpu index accordingly; `pip install onnxruntime-gpu` is guarded by
  `[ "$USE_GPU" = "1" ]` and still last; the baked `ENV ONNX_PROVIDER` is gone.
- `assets/tts/docker-compose.yml.tmpl`: `build.args.USE_GPU` set from `GPUEnabled`.
- `tts/backend/start_server.py`: sets `ONNX_PROVIDER` from
  `device.report()["onnx_provider"]` at startup, so kokoro-onnx is told what is
  available and an explicit override still wins.
- `tts/backend/device.py`: the reported provider is the available one, or an
  explicit override — not merely what was requested.
- Tests: the compose passes the right argument for both cases, and the Dockerfile's
  onnxruntime-gpu install sits behind the GPU check and after the requirements.

**Verification.** `docker build --build-arg USE_GPU=0` produced a **2.36 GB** image
with `torch.cuda.is_available()` false and providers
`['AzureExecutionProvider', 'CPUExecutionProvider']`, against **7.01 GB** with CUDA:
4.65 GB not shipped to a CPU host. The test image was removed afterwards. Both Go
modules and the test suites (124 backend, 127 TUI) pass.

## One port for the component, 5000 nowhere

**Observed behavior.** After the default moved off 5000, the published mapping still
read `127.0.0.1:17493:5000`: the host port had changed but the container's own port
had not, so the same number appeared twice in one line and meant two different
things. The Dockerfile's `EXPOSE`, healthcheck and command, `start_server.py`'s
default, the README and the standalone helper scripts all still said 5000 — the
port that another service on the machine actually owns, which is what made the
original failure hard to read in the first place.

**Intended behavior.** One number for this component wherever it appears, so a
mapping is unambiguous and no instruction points at a port something else is
using.

**Change.** 5000 → 17493 in the backend Dockerfile, the compose mapping and
healthcheck, `start_server.py`'s default, `tts/backend/README.md`, `check_setup.py`,
`test_connection.py`, `test_system.py`, `quick_start_server.sh`, `activate_tts.sh`
and the TUI client's test — 29 occurrences. The mapping is now
`{{ .ListenHost }}:{{ .Port }}:17493`, and a comment in the installer records why
the container's port is the same number.

**Verification.** `go build`, `go vet` and `go test` clean; 118 backend and 125 TUI
tests pass; the rendered compose maps 17493 to 17493 with its healthcheck on
17493; a real install came up `stables-tts Up (healthy) 127.0.0.1:17493->17493/tcp`
with working speech over 17493.

**Testing note.** Install tests must not reuse the production project name. A test
container of mine called `stables-tts`, installed from a throwaway `HOME`, was left
running and held 17493 on a live machine — so the next real install failed the
pre-flight check with "port 17493 is already in use on 127.0.0.1". Use `--name` for
test installs, and remove the container when the test ends.

## The default port collided with another service, and the client was never told which port

**Observed behavior.** Installing on a machine that already ran a speech server on
port 5000 failed after building the image:

```
Error response from daemon: failed to set up container networking: driver failed
programming external connectivity on endpoint stables-tts: Bind for
127.0.0.1:5000 failed: port is already allocated
stables: tts: exit status 1
```

Two faults, one symptom. 5000 is the first port a development server reaches for,
so using it as the default was asking for this. And the client and the backend are
installed together, but the client was left on its built-in default
(`http://localhost:5000`): had the container come up on another port, `stables tts`
would have talked to whatever else held 5000 — a different service's API — and the
result would have looked like a broken install rather than a wrong address.

**Intended behavior.** The default does not sit on a port everything else wants.
Whatever port is chosen, the client is pointed at it. A port that is already taken
is reported before Docker is asked to publish on it, with what to do about it.

**Change.**

- `internal/components/tts/tts.go`: `defaultPort` 5000 → **17493**, with the reason
  recorded; a new `portInUse` check runs before the container starts and fails with
  "port N is already in use on HOST ... choose another one, for example: stables
  install tts --port N+1"; a new `writeTuiPrefs` sets `server_url` in
  `~/.stables/tts/config.toml` to the port actually published, replacing that one
  line and leaving voices, speeds and favourites alone.
- `tts/tui/src/config/prefs.rs`: the client's own default is now
  `http://127.0.0.1:17493`, so a hand-built client does not point at 5000 either.

**Verification.** With a listener occupying a port, the install stops early and
prints the actionable message instead of `exit status 1`. A real install with no
`--port` brought up `stables-tts` healthy on `127.0.0.1:17493->5000/tcp`, with
`server_url = "http://127.0.0.1:17493"` written to the client's config, 56 voices
and working speech. New tests cover the default not being 5000, the config write,
that a second install preserves the user's other settings, and `portInUse` for
both a bound and a closed port. 118 backend and 125 TUI tests unchanged.

## `stables install tts` aborted on an unwritable `~/.local/bin`

**Observed behavior.** Installing stopped before any component was touched:

```
stables: self-install: mkdir ~/.local/bin: permission denied
```

`selfInstall` copies the running binary to `~/.local/bin` so a remote host that
only received the scp'd binary can run `stables tunnel` and `stables list`. It runs
first, and its failure was fatal — so *every* install, of any component, ended
there with an error about a convenience copy. Fixing that exposed the same shape
one level down: the tts component installs its client into `~/.local/bin` too, and
that failure was fatal as well, so the container was never created even though the
backend does not care where the client lives.

**Intended behavior.** A convenience copy that cannot be written is a warning, not
a failed install. A component whose client cannot go to `~/.local/bin` installs it
somewhere writable and says where, because a client that exists somewhere useful
beats an install that refuses to finish over a path.

**Change.**

- `internal/cli/cli.go`: a `selfInstall` failure now warns ("stables will not be on
  PATH") and the install continues.
- `internal/components/tts/tts.go`: `installTUI` takes the install base and falls
  back to `<base>/bin` when `~/.local/bin` cannot be created, printing where the
  client went and to add it to `PATH`. If that fails too, the error names both
  paths.

**Verification.** With `HOME` unwritable (`mkdir ~/.local/bin: permission denied`),
`stables install tts --no-container --no-models` now warns, installs `ratts-cli`
to `~/.stables/tts/bin/ratts-cli` (6,436,480 bytes, mode 0755, verified on disk),
tells the user to add it to `PATH`, and completes staging. With a writable `HOME`
the behaviour is unchanged: `~/.local/bin/ratts-cli` and no warning. `go build`,
`go vet` and `go test` clean, with the backend and TUI suites unchanged at 118 and
125.

## The component is `tts`, not `voice`

**Observed behavior.** The component was called `voice` everywhere: `stables install
voice`, `stables voice`, the Go package, the asset directory, the install directory
`~/.stables/voice`, and the state field. A voice is the thing that gets spoken; tts
is the thing that speaks.

Renaming it exposed a second problem. Compose takes the project name from the
directory, and this installs into `~/.stables/tts`, so the project was named `tts`
— the same project name as the RATTS checkout at `/home/arty/tts` that is still
running on this machine. Compose duly reported that server as an orphan of ours:

```
level=warning msg="Found orphan containers ([tts-ratts-server-1]) for this project."
```

The names a project drives are the network and the image tag, so this was not
cosmetic: two projects called `tts` contend for `tts_default`, and a `down` with
`--remove-orphans` would have removed a container belonging to someone else's
running service.

**Intended behavior.** `stables install tts` and `stables tts`. The *data* keeps
the name: cloned voices live in `~/.stables/voices/`, `STABLES_VOICES_DIR`, the
`voice` field in requests and responses, and `/v1/voices` are all about voices,
not about the component. And our compose project has an explicit name, so no two
projects can collide whatever the install directory is called.

**Change.**

- Renamed: `internal/components/voice` → `internal/components/tts` (package `tts`,
  `VoicePort` → `TTSPort`), `assets/voice` → `assets/tts`, the staged
  `files/voice` → `files/tts`, the vendored project `voice/` → `tts/`, the CLI
  files, the `voice` component name and `stables voice` subcommand, the make
  targets and `agent_tools/check_voice_memory.py` → `check_tts_memory.py`.
- The install directory is now `~/.stables/tts` (models, qwen cache, compose file)
  and the TUI's preferences move to `~/.stables/tts/config.toml`.
- `assets/tts/docker-compose.yml.tmpl`: `name: {{ .Name }}-tts` and an explicit
  `image: {{ .Name }}-tts:local`, with a comment saying why the directory name is
  not good enough.
- Left alone deliberately: `/v1/voices`, `/v1/notify`, the `voices` directory and
  the `voice` field. A rename that also renamed those would have broken the API
  and the user's data — and an over-broad pass in this commit did exactly that
  before being caught: `.stables/voices` briefly became `.stables/ttss` and the
  response field `voice` became `tts`, both reverted. The failing tests are what
  found it.

**Verification.** 118 backend tests, 125 TUI tests, both Go modules build, vet and
test clean. `make assets-common` stages the renamed tree (31 backend modules plus
`ratts-cli`). A real install — `stables install tts --port 5100` — built the image
and brought up `stables-tts` healthy on `127.0.0.1:5100->5000/tcp`, with 56 voices
and working speech (`kokoro 24000 Hz 1.47s`). The project is now labelled
`stables-tts` on the network `stables-tts_default`, and the orphan warning is gone
from the install log.

The safety property that motivated the explicit name is verified: `stables remove
tts` removed our container and our network, and `tts-ratts-server-1` — someone
else's service, up 13 days — is still healthy, with `~/.stables/voices/morgan.wav`
preserved as promised.

`stables install voice` now fails with `unknown component "voice"; want stab,
ollama, webui, tts, or all`.

## The weights are baked into the image, and the mount that could go silently empty is gone

**Observed behavior.** Two things. The previous entry had to record that speech in
the container was unverified, because the models bind mount resolved to a
directory Docker creates empty without complaining: the container answered
`/health` with `"healthy"`, reported zero voices, and 404'd every request. And the
Dockerfile declared `VOLUME ["/app/models"]`, which once content is baked in would
place an anonymous volume over that directory and leave one behind on every
recreate — the unbounded-growth pattern this project forbids.

**Intended behavior.** The image contains the weights. `stables install voice`
fetches them into the build context before `docker build`, so at runtime there is
no mount for them at all, and no bind mount that can silently resolve to an empty
directory. Only the user's own data — cloned voices and the Hugging Face cache —
is mounted. No anonymous volumes.

**Change.**

- `voice/backend/Dockerfile`: `COPY models/ /app/models/`, and the `VOLUME`
  declaration is gone with a comment saying why it must not come back.
- `assets/voice/docker-compose.yml.tmpl`: the weights bind mount is removed.
- `internal/components/voice/voice.go`: `fetchModels` documented as running before
  the build, since the directory is now the build context's `models/`.
- Tests: `TestRenderComposeMountsNoWeights` and
  `TestDockerfileBakesTheWeightsWithoutAVolume` pin both halves.

**Verification.** A real install: the image built (`naming to
docker.io/library/voice-voice`), and `stables-voice` came up `Up (healthy)` on
`127.0.0.1:5100->5000/tcp`. Inside the container `/app/models` now holds
`kokoro-v1.0.onnx`, `voices-v1.0.bin` and `piper/` with both Swedish voices.
`/health` reports `engines: ['kokoro', 'piper']` and `/v1/voices` 56 voices. Real
synthesis, in the container:

```
kokoro/en    kokoro 24000 Hz  1.51s in 0.3s
piper/sv     piper  22050 Hz  1.81s in 0.8s
kokoro 1.5x  kokoro 24000 Hz  1.01s in 1.5s   # exactly 1.51/1.5
```

`POST /v1/notify` returned 201 and the client read the message back. The speed
contract therefore holds with the real engines in the real image, not just in
unit tests.

**Known environment note, not a defect:** the install's own host-side health probe
still cannot complete inside this development container, because `stables` runs
where the Docker daemon is not, so `127.0.0.1:5100` on the daemon host is a
different machine's loopback. On a single machine it is the same host, and that is
the case the probe is for.

## `stables install voice`: the container build verified, and an empty models mount caught

**Observed behavior.** The previous entry recorded that the full container build
had not been run, the host having been at 99% disk. It has now been run, and it
exposed a gap. A container whose models directory is empty answers `/health` with
`{"status": "healthy", "engines": []}` and reports **zero** voices, and every
request after that fails:

```
POST /v1/tts {"voice":"af_heart"} -> 404 {"detail":"unknown voice 'af_heart'"}
```

The install reported success into that state, so the first symptom was an unknown
voice, with nothing pointing at the actual cause.

**Intended behavior.** An install that cannot speak fails, and says which
directory it looked in. A misconfigured mount is an install-time error, not the
first request's problem.

**Change.**

- `internal/components/voice/voice.go`: after the health wait, `verifyVoices`
  reads `/v1/voices` and, when the list is empty, fails with the models path and
  the `docker exec <name>-voice ls /app/models` command to check it with.
- `assets/voice/docker-compose.yml.tmpl`: the comment explaining the device
  reservation no longer spells out the short-form GPU key it forbids, and the test
  now looks for that key as a YAML key rather than as a substring — a comment
  mentioning it is not the same as using it.

**Verification, against the real daemon.** `stables install voice --port 5100`
built the image (`#12 naming to docker.io/library/voice-voice`), created the
network and `stables-voice`, and the container came up `Up (healthy)` on
`127.0.0.1:5100->5000/tcp`. The device reservation reaches it —
`docker inspect ... HostConfig.DeviceRequests` is
`[{"Driver":"nvidia","Count":1,"Capabilities":[["gpu"]]}]`, not null — so the
Compose 2.26.1 short-form bug does not recur. Inside the container every route
answers: `/health`, `/v1/voices`, `/v1/notifications`,
`/v1/notifications/kinds`, `/mcp` and `/v1/capabilities` all 200, `POST
/v1/notify` 201, and an MCP `initialize` returns
`{'name': 'stables-voice', 'version': '1.0.0'}`.

**Speech inside the container is still unverified**, for an environment reason
rather than a code one: the Docker daemon here runs on a different machine, so the
models bind mount resolved to a directory this container cannot populate. Docker
creates missing bind sources as empty directories, which is exactly the state the
new guard now reports. On a machine where `stables` and Docker share a
filesystem — the intended case — the mount holds the weights the install fetches.
`TestVerifyVoicesRejectsAnEmptyVoiceList` and `...AcceptsAVoiceList` pin it.

**Follow-up worth doing:** baking the weights into the image removes the bind
mount and this whole failure mode, and the build already runs after the fetch. The
design asked for that originally.

## An endpoint for agents to leave messages the TUI speaks

**Observed behavior.** Nothing existed. The voice stack only ran one way: the user
typed, the machine spoke. There was no way for an extension or a background agent
to tell the user anything — a finished task, a question needing a decision, a
summary — other than hoping the user was looking at a terminal. Worth adding,
given the whole point of the tool is that the user *hears* results instead of
reading them.

**Intended behavior.** An agent leaves a message; the voice client collects it and
presents it when the user is free. Three rules, in order of importance:

1. **The queue is bounded and says what it dropped.** Announcements come from
   background work nobody is watching, which is exactly the producer that fills
   memory. The queue caps at 50, drops the oldest, and returns the count, so a
   caller can see its message was displaced rather than assume it arrived. This
   is fail-safe by design: a dropped announcement is not an error to raise at the
   user, and a caller that cares sets `max_dropped` and gets a 409 instead. The
   queue cannot grow with uptime.
2. **Reading takes by default.** A poll loop must not speak the same line on every
   pass, so `GET /v1/notifications` consumes; `?peek=true` reads without taking,
   which is what a UI needs to show a badge before deciding to speak.
3. **One queue, two ways in.** MCP for agents, REST for the TUI, so there is a
   single implementation of the bounds and no second place messages pile up.

**Change.**

- `voice/backend/notifications.py`: `NotificationQueue`, a thread-safe bounded
  queue (`DEFAULT_CAPACITY` 50) with `add`, `pending`, `consume`, `clear` and
  `stats`, plus the four message kinds (`say`, `summary`, `question`, `ping`).
- `voice/backend/routes/mcp.py`: an MCP endpoint at `POST /mcp` — JSON-RPC 2.0
  over HTTP, the transport's simple mode. Speaks `initialize`,
  `notifications/initialized`, `ping`, `tools/list` and `tools/call`, answers
  batches in order, and returns proper JSON-RPC error codes. Three tools:
  `notify`, `get_notifications`, `clear_notifications`.
- `voice/backend/routes/notifications.py`: `POST /v1/notify`,
  `GET /v1/notifications` (consume or peek), `DELETE /v1/notifications`, and the
  kind list.
- `voice/backend/app.py` builds one `NotificationQueue` and hands it to both route
  groups, the same way the engine registry is shared.

**Verification.** `cd voice && python -m pytest backend/tests -q` → 118 passed.
`tests/test_notifications.py` (12) covers the bound, the drop count, take-once and
the naming rules; `tests/test_mcp.py` (17) covers the handshake, the tool list,
batch ordering, the 202-with-no-body rule for notifications, the error codes, and
that an MCP `notify` call is visible to the REST side of the same queue.
`GET /mcp` reports the endpoint and its tools.

**Not yet done.** The TUI does not collect these messages yet: the client half is
next, alongside `stables voice`.

## A fresh clone did not compile

**Observed behavior.** Cloning the repository and building it failed immediately:

```
$ git clone <repo> && cd stables && go build ./...
internal/assets/assets.go:13:12: pattern all:files/ollama: no matching files found
```

`internal/assets/assets.go` embeds `files/{stab,ollama,webui}` unconditionally,
but those directories only exist after `make assets-common`, and the
`.gitignore` rules that kept them out of history kept them out of a clone too.
`go:embed` fails to compile when a pattern matches nothing, so the whole module
was unbuildable until someone happened to run a make target first. `AGENTS.md`
claimed the opposite — "a fresh clone compiles under either tag" — which is why
this survived.

**Intended behavior.** `go build ./...` works on a clean clone. The runtime
assets are still generated rather than committed, and a binary built without
them says so plainly instead of failing as if a file had been deleted.

**Change.**

- Each runtime asset directory keeps one tracked placeholder, and the ignore
  rules changed from excluding the directory to excluding its contents
  (`/internal/assets/files/ollama/*` with `!/internal/assets/files/ollama/.gitkeep`),
  so the placeholder is visible to git while everything generated beside it
  stays out of history.
- The placeholders survive `make assets-common`, which `rm -rf`s the generated
  directories. ollama's and webui's travel with the copied source directory
  (`assets/{ollama,webui}/.gitkeep`); stab's is `touch`ed by the target, because
  its directory is not replaced. Without this the next `make assets` would
  delete them again and reintroduce the bug.
- `assets.MustRead` now panics with an actionable message — "runtime asset ...
  is not embedded in this binary: run `make assets-common` and rebuild" — rather
  than the raw file-not-found from `embed.FS`, and `assets.Has` lets the
  template-rendering tests skip instead of failing on a checkout nobody has
  built yet.
- `AGENTS.md` corrected, with the reason the placeholders must stay.

**Verification.** `git clone` of the fixed tree to a scratch directory: `go
build ./...`, `go vet ./...` and `go test ./...` all pass there, with nothing
staged. In the working tree, `make assets-common` followed by `git status` shows
no placeholder deletions — the check that caught the first attempt, in which the
target's `rm -rf` removed them. `make assets-common && go build -tags
embedassets` still embeds the real templates (confirmed by finding the template
names inside the binary) and the 15 MB `stab` binary. `gofmt -l` clean.

## Piper voices were named after their quality, not their speaker

**Observed behavior.** With the two Swedish voices installed, `GET /v1/voices`
reported both of them as the same voice:

```json
{"voices": [{"id": "sv_SE-lisa-medium", "name": "Medium", ...},
            {"id": "sv_SE-nst-medium",  "name": "Medium", ...}]}
```

The id was parsed by taking its last dash-separated segment, which for Piper ids
is the *quality*. So every voice was named after its quality, and both were
reported as male.

**Intended behavior.** A voice is named after its speaker — "Lisa", "Nst" — and
its quality is reported separately, because the UI has to tell two installed
voices apart by name and the id alone is unreadable in a menu.

**Change.** `voice_parts()` in `engines/piper.py` splits
`{lang}_{REGION}-{speaker}-{quality}` into speaker and quality, keeping the
speaker's own dashes and underscores intact (`en_US-libritts_r-medium`,
`de_DE-thorsten_emotional-medium`). The quality now travels in the voice's `note`
field, which `/v1/voices` already returns.

**Verification.** `cd voice && python -m pytest backend/tests -q` → 88 passed,
including six id shapes. Against the real models, `PiperEngine().voices()` now
gives `sv_SE-lisa-medium → Lisa (medium)` and `sv_SE-nst-medium → Nst (medium)`.

## voice backend: Qwen engine for cloned voices

**Observed behavior.** Cloned voices could be listed but not spoken: the registry
routed them to an engine named `qwen` that did not exist, so
`GET /v1/voices` advertised voices that produced
`503 the qwen engine is not available` on use. The design had assumed a Qwen3-TTS
integration that the reference checkout never contained — its only Qwen artifact
was `nvidia/canary-qwen-2.5b`, a speech-to-text model.

**Intended behavior.** A voice cloned from `~/.stables/voices/<name>.wav` is
spoken by a real engine, and the engine's limits are visible rather than
discovered: Qwen3-TTS covers ten languages and **not Swedish**, so Piper keeps
that role and the language grouping must not imply otherwise. Cloning costs
1.2-3.4 GB of weights, which is why it is opt-in rather than part of the base
install. Only one checkpoint is resident at a time.

**Change.**

- `voice/backend/engines/qwen.py`: `QwenEngine`, implementing the protocol for
  Qwen3-TTS (`qwen-tts` 0.1.1, Apache-2.0), using the `*Base` cloning
  checkpoints `Qwen/Qwen3-TTS-12Hz-0.6B-Base` and `-1.7B-Base`.
- Cloning mode follows from what the user has. With `<name>.txt` beside the WAV
  it uses in-context learning (`x_vector_only_mode=False`), which copies delivery
  as well as timbre; without one it uses the speaker embedding
  (`x_vector_only_mode=True`), which is slightly less faithful but works from a
  bare WAV. So the transcript stays optional, as `voices.py` already treated it.
- One checkpoint at a time: `_load` unloads the previous size (dropping the
  reference, `gc.collect()`, then `release_accelerator_memory()`) instead of
  holding both. `preload()` loads one eagerly for the installer to use.
- Sizes are chosen per request through a new optional `size` field on
  `TTSRequest`, defaulting to `1.7B` on CUDA and `0.6B` elsewhere. The protocol's
  `synthesize` gained the matching optional `size` argument, which Kokoro and
  Piper accept and ignore.
- `voice/backend/requirements-qwen.txt`: opt-in install path. `qwen-tts` pins
  `transformers==4.57.3` and depends on gradio, so it stays out of the base image
  and `available()` reports false when it is absent.
- `tests/test_qwen.py` (14 tests) fakes the package, so the cloning modes, the
  single-checkpoint rule and the failure paths are covered without a 3.4 GB
  download. `tests/test_engine_contract.py` checks every registered engine's
  signature against the call the routes make.

**Verification.** `cd voice && python -m pytest backend/tests -q` → 82 passed.
The signature test earned its place immediately: passing `size` positionally had
broken Kokoro and Piper, whose `synthesize` did not accept it, and the stub-based
route tests could not see it — the live check below is what confirmed the fix.
`python3 agent_tools/check_voice_memory.py --requests 6` against the real Kokoro
and Piper models → drift +1 MB, 0 temp files, PASS.

Qwen synthesis itself is **not** verified end to end: it needs a 1.2-3.4 GB
download and, realistically, a GPU. The engine reports itself unavailable until
`requirements-qwen.txt` is installed, so this cannot fail silently.

## Voicebox removed

**Observed behavior.** The `voicebox` component and the `stables tts` command were
a second voice stack living alongside the one now vendored under `voice/`: a
prebuilt third-party image (`ghcr.io/jamiepine/voicebox:latest-cuda`, ~4.9 GB) with
a Go client that polled it, played audio and watched the clipboard. Keeping both
meant two answers to "how does Stables speak", and the voicebox side depended on
an image built six months behind its own `main`, with an API whose `/speak` and
event stream did not exist in any published tag.

**Intended behavior.** One voice stack. The replacement is `voice/` in this repo —
the vendored backend and TUI — installed and started by the Stables CLI. Until
that installer is wired up (the next phase), Stables has no voice command at all;
that is a deliberate, temporary gap rather than a fallback to the old stack.

**Change.**

- Deleted `internal/components/voicebox/` (install, compose, API client, probe,
  tests), `internal/tts/` (client, config, lock, player, clipboard, run, tests),
  `internal/cli/tts.go` and its test, `assets/voicebox/`, and
  `tests/e2e/tts-e2e.sh`.
- `internal/cli/cli.go`: `voicebox` removed from `knownComponents` and from the
  install, update, remove and list paths, along with the `tts` subcommand and its
  usage line.
- `internal/state/state.go`: the `VoiceboxPort`, `VoiceboxListen`,
  `VoiceboxBridge`, `VoiceboxImage` and `VoiceboxDigest` manifest fields are gone.
  An existing `installs.json` carrying them still loads: unknown JSON keys are
  ignored.
- `internal/assets/assets.go` no longer embeds `files/voicebox`, and `Makefile`
  no longer stages `assets/voicebox`.
- `docs/specs/2026-09-17-stables-tts-design.md` and `-plan.md` are deleted with
  the feature they describe. The changelog entries below are kept as history.

**Verification.** `go build ./... && go vet ./... && go test ./...` clean in both
modules; `gofmt -l` clean on the packages touched. A repository-wide search for
`voicebox` outside the changelog and the replacement's design document returns
nothing. `stables install voisebox` still fails loudly via `checkComponent`, which
was added for exactly that typo.

## voice backend: engine registry, cloned voices, and three bugs the refactor exposed

**Observed behavior.** Four separate problems, all in the same request path.

Engine choice was inline in the route handler (`if engine == "piper": ...`), so a
third engine meant editing request handling rather than adding a module, and
cloned voices had nowhere to live at all.

`/v1/tts/stream/pcm` ignored the requested voice's engine and hardcoded the
language: `eng.create_stream(..., lang="en-us")` was called on the Kokoro engine
whatever voice was asked for. The TUI streams anything over 300 characters, so a
Swedish Piper voice was read out in English.

The same endpoint advertised `X-Sample-Rate: 24000` while Piper synthesizes at
22050 Hz, and the TUI hardcodes 24000 and never reads the header:

```
$ curl -sD- -o/tmp/out.pcm -d '{"text":"...","voice":"sv_SE-lisa-medium","engine":"piper"}' \
    localhost:5000/v1/tts/stream/pcm | grep -i sample-rate
X-Sample-Rate: 24000        # the audio was 22050, so it played ~9% off-speed
```

The stretch truncated audio. Its STFT ran with `center=False`, so the final
window never reached the last samples and the tail of every request was dropped.
Measured on a 24000-sample input: `speed=0.5` returned 39872 samples where
48000 was expected — 83%, consistently, at every speed. That is the end of a
sentence disappearing, which is audible in a read-aloud tool.

**Intended behavior.** A voice decides its engine, and the registry — not a route
handler — resolves that, so adding an engine is a new module. Streaming sends one
documented format, in the format it claims. `speed` scales the duration by exactly
`1/speed`, because callers add durations up to time playback. Cloned voices are
files in `~/.stables/voices/`: the directory is the registry, adding a voice is
copying a file, and a clone shadows a preset with the same id because a file the
user added was deliberate. Language detection that fails falls back to English
rather than erroring — this is deliberate and fail-safe: speaking the wrong
language beats refusing to speak text the user can see on screen.

**Change.**

- `voice/backend/` split into `app.py` (wiring), `config.py` (paths, limits),
  `routes/{health,voices,tts}.py`, `engines/{base,registry,kokoro,piper}.py`,
  `voices.py`, `stretch.py`, `audio.py` and `text.py`. `server.py`,
  `tts_engine.py` and `piper_tts_engine.py` are gone. Behaviour of the routes
  other than the fixes below is unchanged, so the TUI needs no changes.
- `engines/registry.py` resolves a voice to an engine: clones go to the cloning
  engine, a requested engine is honoured only if it is available and actually has
  the voice, and a machine with no engine available answers 503 instead of 404 —
  the id may exist elsewhere, so "unknown voice" would be misleading.
- `engines/piper.py` returns samples instead of WAV bytes, so the stretch is
  applied once, centrally, for every engine. It also reports itself unavailable
  when the `piper` CLI is missing, rather than failing later.
- `audio.resample_to()` converts streamed audio to 24 kHz, and the stream route
  now resolves the engine from the voice, so a Piper voice streams as itself.
- `stretch.py`: `center=True` so nothing is truncated, plus `_fit_length` to make
  the output duration exactly `source_length / speed`.
- `GET /v1/capabilities` reports each engine, whether it is available, and its
  languages, so the TUI can describe the choices honestly.
- Tests: `tests/test_stretch.py` asserts the duration contract tightly,
  `tests/test_voices.py` covers name sanitising, discovery and shadowing. The
  route tests now read `app.openapi()["paths"]`; the previous helper scanned
  `app.routes`, which in this FastAPI version misses every included router — so
  the "no STT routes" assertion had been passing without checking anything.

**Verification.** `cd voice && python -m pytest backend/tests -q` → 57 passed.

Against a live server with the real Kokoro and Piper models:
`/health` → `engines: ['kokoro', 'piper']`; `/v1/voices` → 55 voices across both
engines; `/v1/voices/languages` → `en` (kokoro, 46), `en-gb` (kokoro, 8), `sv`
(piper, 1). `POST /v1/tts` for `af_heart` → 24000 Hz, 1.41 s, `duration` field
1.41, 0.3 s wall; for `sv_SE-lisa-medium` → 22050 Hz, 1.86 s, 0.8 s wall.
`POST /v1/tts/stream/pcm` for the Swedish Piper voice → `X-Sample-Rate: 24000`,
`X-Engine: piper`, 148750 bytes (3.10 s at 24 kHz mono), i.e. resampled rather
than mislabelled. The stretch now returns exactly the expected length: a
41000-sample input at speeds 0.5/1.5/2.0 gives 82000/27333/20500 samples, ratios
of 1.000. `python3 agent_tools/check_voice_memory.py --requests 8` → drift
+1 MB, 0 temp files, PASS (and a lower plateau than before, 1466 MB against
1488 MB, because the Piper engine no longer holds model bytes).

**Note.** Piper's own output length is not deterministic: the same text gave
44032, 44544 and 45056 samples across three runs, so a request's total duration
varies by a few percent. The stretch is exact; the variation comes from the
engine.

## voice stack: RATTS backend and TUI vendored, speech-to-text removed

**Observed behavior.** The voice feature was an external prebuilt image, so moving
to a self-contained stack meant bringing the backend and TUI into this repo. Their
upstream carried a whole speech-to-text half — NeMo Canary, IBM Granite,
faster-whisper and a Silero VAD streamer — which dragged in `nemo_toolkit`,
`faster-whisper`, `silero-vad`, `librosa`, `transformers` and `peft`, and had left
27 GB of model files on the development machine (`granite-speech-3.3-8b` alone is
17 GB). It also could not work as shipped: `piper_tts_engine.py` did
`from piper_tts import PiperVoice`, and piper-tts installs a module named `piper`,
so every Piper request would have failed at import.

**Intended behavior.** Text-to-speech only: Kokoro and Piper, chosen per voice, with
the phase-vocoder stretch providing speed. No microphone capture, no
transcription, no VAD, no WebSocket. Anything a request does not need is released
when it finishes; only model weights and imported libraries stay resident. Model
weights are fetched, never committed.

**Change.**

- `voice/backend/` and `voice/tui/` vendored from the RATTS project. `server.py`
  went from 588 to 325 lines: the STT request models, `/v1/stt`,
  `/v1/stt/ensemble`, `/v1/stt/ensemble/resolve` and the `/v1/stt/stream`
  WebSocket are gone, along with `stt_engine.py`, `granite_stt_engine.py`,
  `whisper_stt_engine.py` and `vad.py`. `/v1/chat/completions` stays because it is
  a text-to-audio shim, not a transcription route.
- `voice/tui/`: removed the recorder, microphone and STT-backend menus, the
  streaming client, the `Recording`/`Transcribing` modes and the second text pane
  with its focus switching. The text that gets spoken was already `app.text`, so
  nothing needed renaming to compensate.
- Dependencies trimmed on both sides: `tokio-tungstenite` dropped from the TUI (it
  existed only for the STT WebSocket and pulled OpenSSL into the build), and
  `librosa`, `httpx`, `transformers` and `peft` removed from the backend.
- `piper_tts_engine.py`: the dead `piper_tts` import is gone, the live path shells
  out to the `piper` CLI, and that CLI is now resolved next to the running
  interpreter so a virtualenv works without activation. The 30 s CLI timeout
  became 120 s: it was too low for a long paragraph on a CPU-only host.
- Preferences moved from `~/.config/ratts/config.toml` to
  `~/.stables/voice/config.toml`; `make voice-tui`, `make test-voice` and
  `make check-voice-memory` added.
- `voice/tui/README.md` now describes the code. The design note for a Rust server
  rewrite that was never built moved to `voice/tui/DESIGN-NOTE.md`.

**Verification.** `cd voice && python -m pytest backend/tests -q` → 20 passed,
covering the route surface (no path containing `stt`), request handling and the
stretch-window bounds. `cd voice/tui && cargo test` → 125 passed.
`go build ./... && go vet ./...` clean in both modules. A live Piper request
produced 111,660 bytes of 22,050 Hz audio (2.53 s) at `speed=1.0` and 64,556
bytes (1.46 s) at `speed=1.5`, confirming the engine and the stretch both work end
to end. `GET`-ing the app's routes lists `/health`, `/v1/voices`,
`/v1/voices/languages`, `/v1/piper/voices`, `/v1/tts`, `/v1/tts/stream/pcm` and
`/v1/chat/completions`.

## voice backend: unbounded GPU memory on long text, and container logs that never rotate

**Observed behavior.** Two separate ways the project grew without bound.

A 6000-character synthesis request to the vendored Piper path failed outright:

```
HTTP 500: {"detail":"Piper error: CUDA out of memory. Tried to allocate 1.47 GiB.
GPU 0 has a total capacity of 23.52 GiB of which 1.25 GiB is free. Including
non-PyTorch memory, this process has 8.14 GiB memory in use. Of the allocated
memory 6.78 GiB is allocated by PyTorch, and 934.72 MiB is reserved by PyTorch"}
```

The phase-vocoder stretch transformed the entire waveform in one call, so the
intermediate spectrograms — and Griffin-Lim's 32 iterations over them — scaled
with the length of the text. A 3000-character request (137 s of audio) passed and
6000 characters died, meaning the ceiling was the GPU's free memory rather than
anything intentional.

Separately, `stab` ran every container and every compose service with Docker's
default logging configuration, which is a json-file log with **no rotation** — it
grows for as long as the container lives. Nothing in the repo set `max-size` or
`max-file`, and `stab/internal/docker` passed no `--log-driver` at all.

Two smaller leaks were found while checking for the same class of bug: the Piper
engine cached each voice's ONNX bytes in memory (`self._voices[voice_id] =
(model_bytes, config)`, 63 MB per voice used, and nothing ever read them back),
and the TUI's redo stack had no length cap (only `push_tts_undo` was capped).

**Intended behavior.** Work is bounded by the size of one window, not by how long
the text is. A request either succeeds or fails on its own merits, never because
an earlier request left memory around. Logs are capped at 30 MB per container.
Everything belonging to a request — temp files, transient buffers, device memory —
is released when the request finishes; only the models and imported libraries stay
resident, because avoiding a cold start on every request is the point. Caches on
long-lived objects hold paths, not payloads. No unbounded growth anywhere, so the
footprint after a warm-up is flat and predictable.

**Change.**

- `voice/backend/tts_engine.py`: long waveforms are stretched in bounded,
  overlapping windows (`stretch_bounds`, `_time_stretch_chunked`, ~24 s per
  window) and the pieces are crossfaded, so peak memory no longer scales with
  duration. Added `release_accelerator_memory()`, which calls
  `torch.cuda.empty_cache()` and `malloc_trim(0)`, and is called when a request
  finishes (including when a PCM stream ends early).
- `voice/backend/piper_tts_engine.py`: the cache holds `(model_path, config)`
  instead of the model bytes; also made the `piper` CLI resolve next to the
  running interpreter, so a virtualenv works without activation, and raised the
  CLI timeout to 120 s.
- `stab/internal/docker/options.go`: every container gets
  `--log-driver json-file --log-opt max-size=10m --log-opt max-file=3`.
- `assets/{ollama,webui}/docker-compose.yml.tmpl`: the same cap under `logging:`.
- `voice/tui/src/app.rs`: the redo stack is capped like the undo stack.
- `AGENTS.md`: a "Resource behaviour (no unbounded growth)" section so this does
  not come back.
- `agent_tools/check_voice_memory.py`: new check that starts the real server,
  serves a run of requests and fails if RSS drifts or a temp file is left behind.

**Verification.** `python3 agent_tools/check_voice_memory.py --requests 10
--long-text-chars 6000`:

```
RSS after model load: 492 MB
RSS after warm-up: 1413 MB (one-time cost, retained)
measured phase: early max 1488 MB, late max 1489 MB, drift +0 MB
long request (6000 chars): 12711688 bytes audio, 9.3s, RSS 1488 -> 1488 MB
temp .wav files left behind: 0
RESULT: PASS
```

The 6000-character request previously returned HTTP 500 and now produces 12.7 MB
of audio (289 s) in 9.3 s with RSS unchanged. The +922 MB in the warm-up is torch
and torchaudio being imported on the first speed-changed request; it is retained
deliberately so that later requests do not pay it again.

`cd voice && python -m pytest backend/tests -q` → 20 passed (route surface, no
STT routes, bounded stretch windows, temp-file removal on success and failure).
`cd voice/tui && cargo test` → 125 passed. `cd stab && go test ./internal/docker/`
→ ok, including the new log-cap assertion. Also re-ran the run above with 12
requests and no warm-up accounting to confirm the plateau is flat rather than
merely slow-growing.

---

## voicebox: preload the default TTS model

**Observed behavior.** The first generation after a fresh install either sat
for about a minute (measured 62s cold versus 3.2s warm) or failed outright with
`500 {"detail":"202: {'message': 'Model 0.6B is being downloaded. Please wait
and try again.'}"}` while the server fetched weights in the background.

**Intended behavior.** `stables install voicebox` leaves the default voice
model on disk, so the first sentence is as quick as the rest.

**Change.** After the stack is healthy and reachable, an install that selected
the CUDA image fetches the default TTS model and reports progress every five
seconds. `--no-preload` skips it. Only the largest TTS model the server offers
is fetched — that is the default one — and only when it is neither present nor
already downloading: pulling the smaller variants too would add over a gigabyte
to every install for a size nobody asked for. The download endpoint is
best-effort, so a refusal logs a line and lets the install finish; a cold first
generation still works. New `internal/components/voicebox/api.go`.

Verified against a real stack: deleting `qwen-tts-1.7B` and reinstalling
re-downloaded it (2.4 GB, 55s) with `stables: downloading qwen-tts-1.7B (50s
elapsed)` progress and `the first sentence will not have to wait for
qwen-tts-1.7B`; reinstalling with the model already present did nothing;
`--no-preload` skipped it entirely.

**Not done, deliberately.** The obvious reading of "preload the GPU torch" is
the server's `pytorch-cuda` provider (2.4 GB), and that endpoint is dead in the
released image:

```
httpx.HTTPStatusError: Client error '404 Not Found' for url
  'https://downloads.voicebox.sh/providers/v1.0.0/tts-provider-pytorch-cuda-linux.tar.gz'
```

It is also unnecessary: the CUDA image's bundled provider already reports
`"device":"cuda"` from `/providers/active`, which is why warm generations run
in three seconds. Automating a fetch of a missing asset would turn every
install into a guaranteed error.

---

## voicebox re-downloaded its models on every restart

**Observed behavior.** Recreating the stack re-downloaded the ~2.4 GB model
every time. The published compose mounts a named volume at
`/home/voicebox/.cache/huggingface`, but the server actually runs as root with
`HOME=/root`: `docker exec stables-voicebox env` reports `HOME=/root` and
`ps -p 1` shows uvicorn running as `root`, so weights were written to
`/root/.cache/huggingface` — inside the container's writable layer, discarded
on every recreate. Measured after a recreate: the named volume held 4 KB while
`/root/.cache` held the weights.

**Intended behavior.** Model weights live on the host, under
`~/.stables/voicebox/hf`, so they survive recreation and can be inspected. This
matches how generations are already handled.

**Change.** `HF_HOME` is pinned to the mounted path in the compose template, so
the cache location no longer depends on which user the entrypoint leaves the
server running as, and the cache is a bind mount at `~/.stables/voicebox/hf`
instead of a named volume. `--data-dir` moves both `generations/` and `hf/`. The
now-unused `*-voicebox-hf` named volume is left alone by compose; remove it by
hand with `docker volume rm <project>_stables-voicebox-hf`.

Verified by downloading `whisper-base` (281 MB) and then running
`docker compose up -d --force-recreate`: the weights were still in place and the
model still reported as downloaded, where the old layout re-fetched them.

Two things to know. Weights are written by root, so they are owned by root on
the host and deleting them by hand needs `sudo`. And the CPU image is not a
fallback: `--gpu cpu` installs `ghcr.io/jamiepine/voicebox:latest`, whose
`/generate` fails with `{"detail":"No module named 'qwen_tts'"}`.

---

## voicebox web UI could not reach its own backend

**Observed behavior.** Opening the voicebox web UI on the installed stack
showed `Server Status / Server URL http://127.0.0.1:17493 / Connection failed:
Failed to fetch`, and nothing in the UI worked until the URL was retyped. The
container was healthy the whole time.

**Intended behavior.** The web UI works as soon as it is opened.

**Change.** The stack now publishes host port 17493 instead of 17600, so the
address matches the one the shipped frontend already expects: the bundle seeds
its store with `serverUrl: "http://127.0.0.1:17493"` and builds every request
from that value. Upstream's compose publishes 17600 to leave 17493 free for its
desktop app, which has no Linux build; here the container is the only voicebox,
so keeping the two ports aligned removes a mandatory manual step. `--port`
still overrides it, and the failure mode for anyone running the desktop app
alongside is an explicit "address already in use" at start rather than a
silently broken UI. Verified by reinstalling: the stack now publishes
`127.0.0.1:17493->8000/tcp` and `10.240.0.1:17493->8000/tcp`, `GET /` and
`GET /profiles` on that port both return 200, and `stables tts` reaches the API
and reports `no voices yet — open http://<host>:17493 and create one`.

The Server URL field persists in `localStorage`, so anyone who already typed
17600 keeps working; 17493 is only the default.

---

## voicebox: the container port, the published API, and GPU passthrough

Found by installing the stack on a real Docker host for the first time. These
three defects only appear against the published image; unit tests could not see
any of them.

**Observed behavior.** Four separate failures.

1. `stables install voicebox` never became healthy:
   `timeout after 3m0s waiting for http://127.0.0.1:17600/health (last error:
   Get "http://127.0.0.1:17600/health": dial tcp 127.0.0.1:17600: connect:
   connection refused)`, while the container log said
   `Uvicorn running on http://0.0.0.0:8000`.
2. `POST /speak` returned `405 Method Not Allowed`, and `GET /events/speak`
   returned `404`, so the whole push-notification design had nothing to talk to.
3. The container ran on CPU at 400% with 4.7 GB resident and a single sentence
   still unfinished after 15 minutes, despite `gpus: all` in the compose file.
4. The install and `stables tts` both failed with connection refused when run
   from a container rather than the Docker host's shell.

**Intended behavior.** The stack installs, serves on the port it actually
listens on, is reachable from wherever the CLI runs, uses the GPU it was given,
and speaks from the API the published image actually exposes.

**Change.**

- The compose template maps to container port **8000**, not 17493. Upstream's
  own Dockerfile declares 17493 in its CMD, but every published tag
  (`latest`, `latest-cuda`, `dev`, `dev-cuda`, all a February 2026 build)
  runs `uvicorn --port 8000` and exposes 8000/tcp. The healthcheck follows.
- The TTS client targets the released API: `POST /generate` plus `GET /history`
  polling, replacing `POST /speak` and the `/events/speak` SSE subscription,
  neither of which exists in any published image. `/generate` blocks until the
  audio exists. `internal/tts/sse.go` is deleted. One second of polling latency
  is irrelevant for reading text aloud, and `/generate` and `/history` exist in
  both the released and the unreleased API, so this works if those endpoints
  ever ship.
- GPU is requested with the explicit
  `deploy.resources.reservations.devices` form. Docker Compose 2.26.1 accepts
  the shorter `gpus` service key, echoes it from `compose config`, and then
  creates the container with an empty `HostConfig.DeviceRequests` — no GPU and
  no warning. Verified with `docker inspect` on a throwaway stack rendered both
  ways. With the fix: `gpu_available: true, gpu_type: CUDA (NVIDIA GeForce RTX
  4090)`, and the same sentence that took over 15 minutes on CPU finished in
  3.19 seconds.
- `BaseURLs` gives the CLI both addresses — loopback and the recorded docker
  bridge gateway — and `install` polls both, so the stack is usable from the
  host or from inside a container. `STABLES_VOICEBOX_URL` overrides both.
- The clipboard producer no longer calls `/generate` inline: a slow synthesis
  used to stop the clipboard from being read at all. A one-slot queue keeps the
  newest pending text and drops the rest.

**Verified end to end** against `ghcr.io/jamiepine/voicebox:latest-cuda` on
Docker 29.7.2: install succeeded, `agents can reach it at
http://host.docker.internal:17600` was proven by a real container-side probe,
`POST /generate` returned in 3.19s, `GET /audio/{id}` returned a 199,164-byte
WAV, and a full `stables tts` session turned a simulated clipboard change into
playback of a valid 24 kHz, 3.99-second WAV through a stub player.

**Known upstream issues, documented not fixed.** The published image's
`model_size` switching is unreliable: requesting `0.6B` explicitly returns
`500 {"detail":"Sizes of tensors must match except in dimension 1. Expected
size 1024 but got size 2048"}`, and once that has happened the default path
starts failing too until the container is restarted. The client therefore never
sends `model_size`. The first `/generate` after a fresh container also
auto-downloads a 2.4 GB model and returns
`500 {"detail":"202: {'message': 'Model 0.6B is being downloaded...'}"}` if you
ask before it finishes.

---

## manifest written after the health wait

**Observed behavior.** `stables install voicebox` started the container, then
waited on `/health` before recording anything in `~/.stables/installs.json`. An
install interrupted during that wait — Ctrl+C, a closed terminal, a slow first
start — left a running container that `stables tts` refused to use with
`stables: voicebox is not installed — run: stables install voicebox`.

**Intended behavior.** Once the stack exists, the manifest says so. Health is
verification, not installation.

**Change.** `Install` in `internal/components/voicebox/voicebox.go` writes the
manifest immediately after `compose up -d` succeeds, then waits on health and
runs the container reachability probe. A health failure still returns an error
and dumps the container logs, but the recorded install is accurate. Verified by
`TestInstallFailsWhenHealthNeverArrives`, which now asserts the manifest is
present even when the health wait fails.

---

## silent install wait

**Observed behavior.** `stables install voicebox` printed `Container
stables-voicebox Started` and then appeared to hang with no further output for
up to three minutes. It was polling `/health` every two seconds without
printing anything, so a slow first start (torch import) looked identical to a
freeze. The follow-up reachability probe was worse: it runs
`docker run ... curlimages/curl`, which pulls that image on first use with an
unbounded wait.

**Intended behavior.** An install that is waiting says so, and every step that
can block has a bound.

**Change.** `WaitHealthy` in `internal/components/voicebox/probe.go` now prints
`waiting for voicebox at http://127.0.0.1:17600 (30s, connection refused)`
every 15 seconds, including the last error so a crash-looping container reads
differently from a slow one, and reports the last error when it gives up.
`VerifyReachableFromContainer` is bounded to 90 seconds and its failure hint
mentions the helper-image pull. Verified with `go test
./internal/components/voicebox/`, which now covers recovery after a 503, the
timeout message naming the real status code, and context cancellation.

---

## unknown component names

**Observed behavior.** `stables install voisebox` printed `stables: install
complete` and installed nothing. The misspelled name fell through every
`if component == ...` branch in `cmdInstall`, so the command reported success
without doing any work. It also self-installed the binary into `~/.local/bin`
on the way.

**Intended behavior.** An unrecognized component name is an error that names
the valid ones, and nothing is written before the name is checked.

**Change.** `checkComponent` in `internal/cli/cli.go` validates the name
against `knownComponents` (stab, ollama, webui, voicebox, plus `all`) at the
top of `cmdInstall`, `cmdUpdate`, `cmdRemove`, `cmdCleanInstall`, and
`cmdReinstall`, before any filesystem or Docker work. Verified by rebuilding
and running `stables install voisebox`, which now prints `stables: unknown
component "voisebox"; want stab, ollama, webui, voicebox, or all` and exits 2
with the target home still empty, plus unit tests covering the typo paths for
install, remove, and update.

---

## stables tts

**Observed behavior.** Voicebox's container ships the server side only. With
the stack up, `curl -X POST http://127.0.0.1:17600/speak -d '{"text":"hello"}'`
returns a generation id and the request succeeds, but nothing is audible: the
backend generates a WAV and never plays it. Playback lives in the Tauri
desktop app, where `speak_monitor.rs` subscribes to `/events/speak` and plays
through `cpal` — and there is no Linux desktop build. Clipboard capture is
desktop-only for the same reason.

**Intended behavior.** One host-side command plays everything the container
generates: agent announcements and text copied to the clipboard, in whatever
voice the web UI has configured, out of the machine with the speakers.

**Change.** New `stables tts` command with the support package
`internal/tts`: a singleton lock, a `/events/speak` subscriber that plays each
generation, a clipboard poller that posts new text to `/speak`, a one-shot
`--clipboard` mode for a desktop shortcut, and `~/.stables/tts.json` for the
voice, length limits, and skip patterns. Playback uses the first available of
`pw-play`, `paplay`, `ffplay`, `mpv`, or `aplay`.

Three deliberate choices. Playback happens only on the event stream: the
clipboard path posts to `/speak` and lets the stream deliver the audio, so
there is one synthesis path and one playback path. Content already on the
clipboard when the command starts is recorded but not spoken, because it is
not a copy the user just made. A dropped event stream is reconnected with a
backoff rather than treated as fatal, so a voicebox restart does not silently
kill the watcher.

Verified with `go test ./...` including `-race` on the run loop, plus CLI
smoke checks of the failure paths: no voicebox installed, a manifest entry
pointing at a dead port with `--no-start`, and `--status` with no lock file.
A real session with a speaker, a clipboard, and a running container is not yet
exercised; `tests/e2e/tts-e2e.sh` covers the container side of that behind
`STABLES_TTS_E2E=1`.

---

## voicebox install

**Observed behavior.** There was no way to run a local voice studio from
Stables: the only spoken-output path was a hand-rolled script with no voice
management, nothing a container could reach, and no route from the agent to a
speaker.

**Intended behavior.** `stables install voicebox` deploys Voicebox as its own
compose stack with the web UI on loopback, voice profiles persisted in a named
volume, and a port that agent containers can reach through
`host.docker.internal`.

**Change.** New component `internal/components/voicebox` with a committed
compose template at `assets/voicebox/docker-compose.yml.tmpl`. The install
picks `latest-cuda` or `latest` from a GPU probe, records port, listen host,
image, and containers in `~/.stables/installs.json`, and verifies after start
that a throwaway container can reach `/health`. The default listen policy is
loopback plus the docker bridge gateway rather than `0.0.0.0`, because the
Voicebox API is unauthenticated and can read and write voice profiles. Verified
with `go test ./...` (all packages green) and CLI smoke checks: `stables list`
reports the component, `stables install voicebox --gpu rocm` refuses with the
`--image` hint and exit status 1 without touching Docker, and on a host whose
Docker daemon is down the install renders `latest-cuda` with
`127.0.0.1:17600:17493` and then fails with Docker's own connection error.
A full install on a running Docker host, including the container-side
`host.docker.internal` reachability check, is not yet exercised.

`--gpu rocm` is rejected with a pointer to `--image` rather than building
Voicebox from source: upstream publishes no ROCm image, so the alternative was
a long local build inside a stateless installer.
