# 🐴 Stables 🦄

*Contains your horses and unicorns so they behave!*

A Docker-first wrapper with a **single-binary installer** for [PyHarness](https://pi.dev).

Stables installs the `stab` runtime — the main event — from one
`stables` binary. The agent runs inside a container that sees **your project**,
your Pi agent dir (skills, extensions, logins), and nothing else from the
machine.

For convenience, the same binary also carries a Dockerized Ollama, an Open
WebUI, and an SSH tunnel manager — a small bonus for running models locally and
chatting without leaving the terminal.

---

## What this is — and isn't

There is nothing novel here. Stables does not invent anything: it glues together
the tools you already use every day.

- **Docker** provides the restriction — a disposable container that sees your
  project and your Pi agent setup, and nothing else.
- **tmux** keeps the session alive — detach, resume, tiling windows.
- **PyHarness** is the agent — extensible with extensions and skills, scriptable,
  and full of tools for the user.

The value is the glue, not the parts: one binary that assembles a good stack so
your workflow stays simple.

---

## Quick start

Prefer not to build? Download a prebuilt binary and install it:

```sh
curl -L "https://drive.google.com/uc?export=download&id=13qJFUfLAvylxhuSJpqs4c9Oo_TbDA36P" -o stables \
  && chmod +x stables \
  && ./stables install
```

Or build from source:

```sh
git clone https://github.com/Arty-Facts/stables.git
cd stables
make build          # the release: skills and extensions embedded

./stables install stab
./stables install ollama
./stables ollama pull qwen2.5:0.5b

cd /path/to/project
stab .                        # PyHarness in a container, your project + agent dir mounted
```

Open WebUI: `http://localhost:3000` 

Ollama API: `http://<machine>:11434` 

---

## The safety model — why the agent runs without asking

`stab` is built on a simple trade: **the agent acts, the environment
protects.** Pi does not pause for an "Approve?" on every command, and that is
deliberate.

**Prompt fatigue is the enemy of flow.** A human-in-the-loop agent that makes
you confirm every file read, edit, and command turns you into a button-pusher
instead of a strategic director. An agent diagnosing a test failure may run
fifteen commands in sequence; approving all fifteen destroys the very flow you
are trying to keep.

**Isolate the environment, not the tool.** Rather than leashing the AI inside
your production environment with a heavy, bypassable prompt-sandbox, Pi shifts
the safety boundary outwards — into a disposable container:

- Your project is mounted at `/workspace`; the only other host paths in the
  container are `~/.pi/agent` (skills, extensions, logins) and the project's
  own state.
- **No `~/.ssh`, no host home directory, no other projects or files.**
- Only the secrets a project's `models.json` actually references are injected,
  and secret values are redacted from errors.

If the agent breaks something, you tear down the container and start again —
nothing else on the host was ever at risk.

**Let it fail.** Real engineering is a `trial → error → fix` loop. A speculative
command that fails teaches the agent something; a command it had to ask
permission for breaks its chain of thought. Free execution lets it read the
error logs and self-correct in seconds.

**Git is the guardrail.** Work inside a repository and Git is your undo: if the
agent changes ten files and breaks the build, `git reset --hard` brings you
back. That safety net is what lets the agent be fast and aggressive.

```
                        ┌──────────────────────────────────────┐
  your project  ───────►│  container: stab-<project>            │
  (~/code/app)          │                                      │
                        │  /workspace    ← your project        │
                        │   └ .stables ← Dockerfile, tmux,   │
                        │      models.json, sessions           │
                        │  /stab-state     ← project state       │
                        │  ~/.pi/agent   ← skills, extensions, │
                        │      auth (persistent)               │
                        │                                      │
                        │  ✗ ~/.ssh    ✗ $HOME                 │
                        │  ✗ other projects                    │
                        └──────────────────────────────────────┘
   only these three paths are mounted
```

Two host paths are bind-mounted so your setup survives `stab update` and
container recreation:

- **`~/.pi/agent`** — the whole agent dir: `skills/`, `extensions/`, installed
  packages, `auth.json` (Pi logins), and `models.json`. Edit or add an
  extension on the host and it is live inside every workspace; a Pi login
  survives without re-authenticating.
- **the project's `.stables/`** — inside your project mount, so it is visible
  in the container: the `Dockerfile`, `tmux.conf`, the project's `models.json`,
  and the `sessions/` that let you resume a conversation.

The host's global `~/.stables/` itself is **not** mounted — it is config the
`stab` CLI reads. Its `models.json` is seeded into the project and merged into
`~/.pi/agent/models.json`; its secret values are injected as environment
variables (only the keys a project's `models.json` references).

Sub-container spawning is **opt-in**: normal `stab .` cannot start sibling
containers. `stab . --super` mounts the host Docker socket so
the agent can, deliberately, manage containers.

---

## How it works

```
┌─ LOCAL INSTALL ───────────────────────────────────────────────────┐
│                                                                   │
│   make build                                                        │
│   ./stables install stab                                     │
│        │                                                          │
│        ▼                                                          │
│   ~/.local/bin/stables      (the stables binary, on PATH)         │
│   ~/.local/bin/stab           (the workspace runtime)               │
│   ~/.stables/             (config, models.json, secrets.env)    │
│   ~/.pi/agent/              (skills, extensions, auth)            │
└───────────────────────────────────────────────────────────────────┘

┌─ START A WORKSPACE ───────────────────────────────────────────────┐
│                                                                   │
│   cd ~/code/app                                                   │
│   stab .                                                            │
│        │                                                          │
│        ▼                                                          │
│   build image  →  run container  →  mount the directory           │
│        │                                                          │
│        ▼                                                          │
│   PyHarness in tmux, inside the container, inside your project    │
└───────────────────────────────────────────────────────────────────┘

┌─ REMOTE INSTALL ──────────────────────────────────────────────────┐
│                                                                   │
│   ./stables remote install stab user@host                    │
│        │                                                          │
│        ▼                                                          │
│   scp the binary  ──►  ssh user@host  ──►  ~/.local/bin/stab        │
│                                                                   │
│   ./stables tts tunnel user@server                                │
│        │                                                          │
│        ▼                                                          │
│   ssh -L 0.0.0.0:11434:localhost:11434 user@host                  │
│   (a remote Ollama now looks local)                               │
└───────────────────────────────────────────────────────────────────┘
```

---

## A stable for your agents — they don't need to sleep

`stab` is not just a launcher — it is a **persistent runtime**. PyHarness
runs inside a long-lived tmux session in the container, so the workspace keeps
living even when you disconnect. Detach, close the laptop, come back tomorrow:
the agent kept working the whole time.

```
┌─ tmux session "stab" (inside the container) ────────────────────┐
│  ┌─ main ──────┐   ┌─ shell ───────┐                          │
│  │  PyHarness  │   │  pytest,      │                          │
│  │  (agent)    │   │  go build …   │                          │
│  └─────────────┘   └───────────────┘                          │
│  ┌─ logs ──────┐   ┌─ help ────────┐                          │
│  └─────────────┘   └───────────────┘                          │
└───────────────────────────────────────────────────────────────┘
```

- **Resume without killing anything** — `stab .` re-attaches to the running
  session; the agent is never torn down.
- **Exit and re-enter freely** — detaching is harmless; nothing stops.
- **Tiling windows** — a terminal next to the harness, plus logs and help,
  all in one container.
- **Remote-work friendly** — one SSH connection is all you need: tile more
  terminals inside it, detach, reconnect from anywhere, and your windows come
  back exactly as you left them.

| Key | Action |
|-----|--------|
| `F4` / `Ctrl-b d` | detach (the agent keeps running) |
| `F2` / `F3`       | split horizontally / vertically |
| `M-1`…`M-4`       | jump to main / shell / logs / help |
| `Ctrl-b <arrow>`  | switch pane · `Ctrl-b z` zoom · `Ctrl-b ?` help |

`stab bash` drops you straight into the shell window next to the harness.

---

## A bonus: local models, a chat UI, and a voice

`stab` is the point of Stables. But for convenience — and so you can be more
autonomous and run models locally — the same binary also carries a Dockerized
Ollama and an Open WebUI.

**Ollama, one line.** `stables install ollama` deploys it from the baked-in
compose template, exposed on `0.0.0.0:11434`. Then:

```sh
stables ollama pull qwen2.5:0.5b   # fetch a model
stables ollama up                  # start the container
stables ollama down                # take it down
```

**Open WebUI, for when you want to chat.** `stables install webui` gives you a
chat-friendly interface at `http://localhost:3000`. It is local-first: it points
at the local Ollama, and because a tunnel makes a remote Ollama look local, it
follows whatever tunnel is active with zero reconfiguration.

**Tunnels, one per component.** A tunnel reaches a private server over SSH and makes
its service look local, so a machine without a GPU can use one that has:

```sh
stables ollama tunnel user@server   # that server's Ollama appears at your local 11434
stables tts tunnel user@server      # and its voice backend at 17493
stables tunnel list                 # what is up
stables tunnel down                 # close them all
```

A tunnel forwards that component's own port, so it takes the component's place:
tunnelling stops the local stack it would collide with, and closing the tunnel starts
it again.

**Voice, so you can hear it.** `stables install tts` deploys a small local voice
backend — Kokoro and Piper, plus Qwen if you want to clone a voice — and installs a
terminal client:

```sh
stables install tts                          # weights, image, container
stables tts                                  # the client
stables tts up | down | list                 # start it, stop it, look at it
stables tts tunnel user@server               # or speak through another machine's GPU
stables tts-mcp-ping "the deploy finished"   # leave a message to be read aloud
```

The weights live in `~/.stables/tts/models` and are mounted read-only, so replacing
the container or rebuilding the image never re-downloads them; `--preload` adds the
cloning engine and fetches its weights. A cloned voice is a file —
`~/.stables/voices/<name>.<lang>.wav`, so `morgan.sv.wav` is Morgan speaking Swedish
— and an optional `<name>.<lang>.txt` beside it is the reference transcript. Speech is
stretched by a phase vocoder (0.1×–4×) rather than resampled, so a faster voice does
not sound like a chipmunk. `stables tts-mcp-ping` goes through the same MCP endpoint an
agent uses, which is how something with no voice of its own tells you it has finished.
The API is in [tts/backend/README.md](tts/backend/README.md), the keys in
[tts/tui/README.md](tts/tui/README.md).

None of this excludes other providers. `~/.stables/models.json` is plain Pi
provider config, so bring OpenAI / Anthropic / DeepSeek / any OpenAI-compatible
endpoint too. Ollama, the WebUI, and the tunnel are conveniences, not the main
event.

### Adding other providers

Models and credentials live in two files under `~/.stables/`:

- `models.json` — provider definitions (endpoint, models). Keys are referenced
  as `$VAR`, never pasted in.
- `secrets.env` — the actual key values, one per line, mode `0600`.

Three steps:

1. Put the keys in `~/.stables/secrets.env`:

   ```sh
   cat >> ~/.stables/secrets.env <<'EOF'
   DEEPSEEK_API_KEY=sk-your-key
   OPENAI_API_KEY=sk-your-key
   ANTHROPIC_API_KEY=sk-ant-your-key
   EOF
   ```

2. Add the providers to `~/.stables/models.json`:

   ```json
   {
     "providers": {
       "deepseek": {
         "api": "openai-completions",
         "apiKey": "$DEEPSEEK_API_KEY",
         "authHeader": true,
         "baseUrl": "https://api.deepseek.com/v1",
         "models": [
           { "id": "deepseek-chat", "name": "DeepSeek Chat" }
         ]
       },
       "openai": {
         "api": "openai-completions",
         "apiKey": "$OPENAI_API_KEY",
         "authHeader": true,
         "baseUrl": "https://api.openai.com/v1",
         "models": [
           { "id": "gpt-4o", "name": "GPT-4o" }
         ]
       },
       "anthropic": {
         "api": "anthropic-messages",
         "apiKey": "$ANTHROPIC_API_KEY",
         "baseUrl": "https://api.anthropic.com/v1",
         "models": [
           { "id": "claude-sonnet-4-6", "name": "Claude Sonnet" }
         ]
       }
     }
   }
   ```

   The same shape works for any OpenAI-compatible endpoint — change `baseUrl`
   and the model list. (Anthropic uses `"api": "anthropic-messages"`; OpenAI and
   most others use `"api": "openai-completions"`.)

3. Refresh the workspace so the agent and Open WebUI see it:

   ```sh
   stab fetch           # refresh models (stops a running container; stab . reloads)
   # or in a fresh project: stab .
   ```

   `stables fetch webui` (or reinstall) exposes the new endpoint to Open WebUI.

The secret stays in `secrets.env`; only the `$DEEPSEEK_API_KEY` reference is
injected into the container, and only when a model actually uses it.

---

## Commands

### Install / update / remove

```sh
stables install stab        # the workspace runtime
stables install ollama           # Dockerized Ollama (0.0.0.0:11434)
stables install webui            # Open WebUI, local-first (localhost:3000)
stables install all              # stab + ollama + webui

stables remote install stab user@host
stables remote install ollama user@host
stables remote clean-install stab user@host   # wipe + reinstall on the target

stables update stab         # rebuild the workspace runtime
stables update ollama            # compose pull + up

stables remove ollama            # tear down (removes data volumes)
stables remove webui
stables clean-install stab  # remove + reinstall, self-healing home dirs
```

`remote` installs are stateless: Stables `scp`s its own binary to the target and
runs the install there over SSH. Nothing about the remote is remembered locally —
you remember where your servers are.

### Ollama

```sh
stables ollama pull qwen2.5:0.5b
stables ollama list
stables ollama up               # start the local container
stables ollama down             # stop it (frees 11434 for a tunnel)
```

### Voice

`stables install tts`, the client, its lifecycle and its tunnel are spelled out with
the other bonuses in [A bonus: local models, a chat UI, and a
voice](#a-bonus-local-models-a-chat-ui-and-a-voice).

### Tunnel

A tunnel forwards a component's own port, so it takes the place of that component
locally: tunnelling stops the local stack it would collide with, and closing the
tunnel starts it again. Both tunnels can be up at once.

```
stables ollama tunnel user@host     # the model server (11434)
stables tts tunnel user@host        # the voice backend (17493)
stables ollama tunnel down
stables tts tunnel down
stables tunnel list                 # every tunnel that is up
stables tunnel down                 # close them all, restoring what they stopped
```

### Refresh models (fast)

```sh
stables fetch stab          # refresh models.json from the active Ollama (no rebuild)
stables fetch webui              # re-pull models + re-read models.json connections
```

`fetch stab` is the lightweight alternative to `stab update` — use it when
only the model list changed, not the runtime.

### State

```sh
stables list                     # what's installed locally
stables version
```

### stab

```sh
stab .                 start or attach to the current project
stab . --super         start with the Docker socket (sub-container spawning)
stab bash              attach to the project's shell window
stab update            rebuild the image from its Dockerfile (--pull --no-cache)
stab fetch             refresh models.json from the active Ollama (fast)
stab list / status     known projects and containers
stab kill [all]        stop a workspace
stab check             verify prerequisites (--repair self-heals home dirs)
```

---

## Build tiers

All three targets build locally — the runtime assets (the `stab` binary and the
compose templates) are generated by `make assets-common` and **never committed**.
Build with `make`, not a bare `go build`.

`make build` bakes the build machine's personal assets into the binary so a
remote install carries them. Three tiers:

| Target | Skills/extensions | models.json + secrets.env |
|--------|-------------------|---------------------------|
| `make build-minimal` | — | — |
| `make build`         | ✅ | — |
| `make build-secrets` | ✅ | ✅ **sealed with an installer password** |

- `build` — the release build: skills and extensions embedded, so the install works out of the box. No `models.json`, no `secrets.env`
- `build` — carries your skills and extensions.
- `build-secrets` — embeds `models.json` + `secrets.env`, **encrypted** with a
  password you type at build time. Install with `stables install --password
  <pw>` (or the interactive prompt) to unlock them; a wrong/missing password
  gives the base install without secrets. Only `scp` this to machines you own.

**Template version.** The runtime embeds four files — `Dockerfile`,
`Dockerfile.gpu`, `entrypoint.sh`, `tmux.conf` — that seed each project. Change
any of them and bump `TemplateVersion` in `stab/runtime/embed.go`; `stab .` only
rebuilds a project's image when that number moves forward. Forgetting the bump
leaves existing projects on images with the old embedded files.

### Password-locked secrets

`build-secrets` does not bake your keys in the clear. It prompts for an
**installer password** and seals the baked `models.json` + `secrets.env` before
embedding them. The password itself is never stored — only ciphertext rides in
the binary.

```
make build-secrets
Installer password: ********     ← typed once, no echo
  → models.json + secrets.env encrypted → baked into the binary

stables install                  ← on any target machine
Installer password: ********     ← same password
  → correct   : decrypted + merged into ~/.stables/
  → wrong/missing : "incorrect password — installing without models/secrets"
                    → base install (extensions, no secrets)
```

Details:

- **Encryption** — AES-256-GCM; the key is derived from the password with
  PBKDF2-HMAC-SHA256 (200,000 iterations, random salt). The sealed blob is
  `STB1:<base64(salt|nonce|ciphertext)>`.
- **Providing the password at install** — three ways, in order of precedence:
  `stables install --password <pw>`, the `STABLES_PASSWORD` environment
  variable, or the interactive no-echo prompt (shown only when the binary has
  sealed assets).
- **Wrong password** — fails GCM authentication, so nothing is ever written
  with bad keys. You get the base install; the secrets stay sealed in the
  binary until you reinstall with the correct password (or `--force`).

A note on strength: the lock is only as good as the passphrase you choose. Pick
a long, random one — the sealed secrets travel with the binary.

---

## Reinstall & uninstall

Reinstall is **soft by default**: containers are recreated but data volumes
(Ollama models, WebUI logs) survive. Add `--clean` to wipe them first for a
fresh system.

```sh
stables reinstall webui           # recreate, keep chat history + connections
stables reinstall --clean webui   # wipe the data volume, then fresh install
stables reinstall --clean all     # full wipe + fresh (stab + ollama + webui)
```

`stables remove` is also soft (takes containers down, keeps models + logs).
`stables clean-install` is the hard equivalent (wipe + reinstall).

For a full clean slate (including the stables binary itself):

```sh
rm -f ~/.local/bin/stables ~/.local/bin/stab
rm -rf ~/.stables ~/.pi/agent
```

---

## Changelog

User-visible behavior changes and non-obvious bug fixes are recorded in
[docs/CHANGELOG.md](docs/CHANGELOG.md) — what was observed, what the tool is
supposed to do instead, and what changed.

---

## License

MIT — see [LICENSE](LICENSE).
