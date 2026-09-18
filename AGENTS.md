# Agent Guidance — Stables

## What this is (read this first)

Stables is **nothing novel**. It glues three everyday tools into one
single-binary installer:

- **Docker** — isolation. The agent runs in a disposable container that sees
  only the project directory you point it at.
- **tmux** — persistence. Detach, resume, tiling windows; one SSH connection
  for remote work; the agent never sleeps.
- **PyHarness (Pi)** — the agent runtime, extensible with extensions and skills.

The value is the glue, not the parts. Do not add cleverness for its own sake.

## Philosophy

1. **Easy to install, easy to use.** One binary, `stables install <component>`,
   done. A command should be obvious from its name; flags should be rare.
2. **The agent acts, the environment protects.** Pi does not pause for
   permission before every command — that is prompt fatigue. Safety comes from
   the disposable container and Git, not from leashing the agent. If the agent
   breaks something, tear down the container and `git reset --hard`.
3. **Local-first, remote-friendly.** Ollama + Open WebUI + the SSH tunnel are a
   convenience bonus, not the main event. They exist to run models locally and
   chat without leaving the terminal. The runtime (`stab`) is the main event.
4. **Stateless installer.** Nothing is remembered on a remote target. Local
   state is a small JSON manifest (`~/.stables/installs.json`), not a database.

When modifying this repo: favor obvious commands, zero-surprise defaults, and
removals that clean up after themselves. Keep it a good stack of glued tools,
not a framework.

## Repo layout — two Go modules

**Root module** (`module github.com/stables/stables`) — the `stables` CLI:

- `cmd/stables/` — entrypoint.
- `internal/cli/` — command surface (install / remove / ollama / tunnel / fetch / list / version).
- `internal/components/{stab,ollama,webui}/` — per-component installers.
- `internal/tunnel/` — SSH tunnel manager (background `ssh -L`).
- `internal/remote/` — stateless remote install (scp binary + ssh).
- `internal/state/` — the `installs.json` manifest.
- `internal/piagent/` — Pi login persistence (mounts `~/.pi/agent`, merges `models.json`).
- `internal/assets/` + `cmd/embedgen/` — baked assets (compose templates, skills, extensions).

**`stab/` module** (`module github.com/stables/stab`) — the `stab`
runtime, vendored inside this repo:

- `internal/cli/` — the `stab` command surface.
- `internal/project/` — image build + container controller.
- `internal/docker/` — container orchestration.
- `internal/store/` — SQLite registry (projects / deployments).
- `internal/{tmux,mounts,secrets,redact,config}/` — runtime pieces.
- `runtime/` — Dockerfile + entrypoint.

The two modules are separate and communicate only through the staged binary and
the filesystem (`~/.stables/`). `stables` never imports
stab internals, and vice versa.

## Build tiers (critical)

`make build-*` controls what is baked into the binary via Go build tags:

- `build` — **the release build.** Embeds the builder's skills and extensions, which is
  what makes the stack usable the moment someone installs it: they get a working agent,
  not an empty one. It carries no `models.json` or `secrets.env` (that is `build-secrets`)
  and no third-party weights.
- `build-minimal` — the same binary with none of the builder's personal assets. For
  working on the CLI without shipping anyone's skills; it can still install every
  component, because the runtime assets are embedded either way.
- `build` — skills + extensions embedded (default).
- `build-secrets` — also embeds `models.json` + `secrets.env`. **Keys end up in
  the binary. Never ship or commit this.**

The `embedassets` / `embedsecrets` build tags select real assets vs stubs.
Payload dirs (`internal/assets/files/piagent/`, `files/home/`) are gitignored;
the `internal/assets/*_gen.go` embed declarations carry no payload and **are**
committed, so a fresh clone compiles under either tag.

The runtime asset dirs (`files/{stab,ollama,webui}/`) are also generated, but
`internal/assets/assets.go` embeds them **unconditionally** — so each keeps one
tracked `.gitkeep`. Without it `go:embed` matches nothing and the whole module
fails to compile (`pattern all:files/ollama: no matching files found`), which is
what a fresh clone did before the placeholders existed. Keep them. `make assets` deletes the
payload dirs before regenerating them, and `assets_gen.go` embeds an explicit
file list, so a stale `models.json`/`secrets.env` can never ride along.

**Template version (must bump).** `stab/runtime/embed.go` carries
`const TemplateVersion`. Any change to the embedded runtime files
(`stab/runtime/{Dockerfile,Dockerfile.gpu,entrypoint.sh,tmux.conf}`) MUST bump
it. It is the cache-buster: `stab .` rebuilds an image only when that number
moves forward. Miss it and existing projects keep running images with the old
embedded files (env vars, paths, entrypoint behavior), producing confusing,
hard-to-trace errors for users.

## Hard rules

### Secrets and release hygiene

- **No company model names, provider endpoints, or `sk-*` strings** anywhere in
  tracked files. This repo ships to the public.
- Company-specific provider sync lives in a separate internal repo **outside
  this one** — never reference it or its endpoints from here.
- `secrets.env` stays mode `0600`, never committed, and secret values are
  redacted from errors.

### Shipping binaries and embedded assets

- Everything baked into a shipped binary is public. `make build` must never
  embed `models.json`/`secrets.env`; only `build-secrets` does, and those
  binaries are never distributed.
- Never archive `.git` in the embedgen tarballs. It carries private repo names,
  remote URLs, author emails, and full history — proven to leak into a published
  `stables` binary. `skipPath` skips `.git`; keep it that way.
- Release builds use `-trimpath -buildvcs=false` (the `Makefile` sets both) and the
  embedded client is built with `--remap-path-prefix`: without them a shipped binary
  carries the builder's home directory, crate registry and Go module cache, plus the
  commit it was built from. Verified with `go version -m ./stables` (no `vcs.*` lines)
  and `strings stables | grep -E "/home/|/Users/"` (nothing).
- Never commit a built binary (`stables`, `stab/stab`, `stab/stb`). `.gitignore`
  must cover every name a build can produce.
- Before publishing a binary, a commit, or a link people can download, scan it:
  `python3 agent_tools/scan_secrets.py --providers <path>` for text and
  `python3 agent_tools/scan_binary_archives.py --providers <binary>` for the
  embedded `.tgz` payloads (a plain `strings` scan cannot see inside them).

### Docker networking and E2E tests (do not weaken)

- Treat `/var/run/docker.sock` as host-level administrative access. Containers
  using it can create host Docker networks, routes, firewall rules, ports, and
  containers.
- Never rely on Docker's automatic subnet allocation for E2E or development
  networks. Explicitly configure a private subnet and verify it does not overlap
  host routes, VPN/LAN ranges, remote server addresses, or existing Docker
  networks.
- Check every network created by the test, including networks created later by
  Docker Compose. Validate subnets before starting containers.
- Use unique, test-specific resource names or labels. Cleanup must remove only
  resources created by the current test and must run on success, failure,
  timeout, and interruption.
- Never force-remove arbitrary `stab-*` or other development containers during
  test cleanup. Killing an attached container can close the terminal and
  produce exit status 137.
- When a test changes Docker networking, verify important remote hosts before
  and after the test with `ip route get <address>` and an SSH connectivity
  check.
- Prefer a separate Docker daemon for E2E tests. If the host daemon must be
  used, document the risk and apply explicit network and resource limits.

### SSH script gotcha (proven, non-obvious)

- OpenSSH (v26) joins multi-word argv unquoted: `ssh host bash -lc '<multi-line
  script>'` breaks — the remote runs a bare `bash -lc` and then executes the
  script lines separately (produces `bash: -c: option requires an argument` and
  silently defeats `|| exit 1` guards).
- Working pattern: pass the multi-line single-quoted script as the **sole** ssh
  command (no `bash -lc` wrapper), with every line self-guarded `|| exit 1`.
- `sh -c '... "$1"'` gotcha: positional args inside the inner shell are empty;
  pass data via `-e VAR` env vars instead.
- Compose volume names get a project-dir prefix (e.g.
  `ollama_e2e-open-webui-data`); teardown must handle the prefixed form.
- `all-minilm` is an embedding model and cannot serve `/v1/chat/completions`.
  Use a real chat model for E2E (e.g. `qwen2.5:0.5b`, overridable via
  `E2E_MODEL`).

### Resource behaviour (no unbounded growth)

Stables and the voice stack run for weeks. Nothing may grow with uptime:

- **No log files of our own, and no unbounded container logs.** Docker's
  json-file driver does not rotate by default, so every `docker run` in this repo
  passes `--log-driver json-file --log-opt max-size=10m --log-opt max-file=3`
  (`stab/internal/docker/options.go`), and every compose template sets the same
  under `logging:`. Never add a log file that is opened in append mode.
- **Per-request memory is returned; only models stay resident.** Anything
  allocated while serving a request must be freed when it is done, including
  device memory — call `release_accelerator_memory()` in `tts/backend`. It is
  fine (intended) to keep loaded models and imported libraries resident: the goal
  is to avoid repeated cold starts, not to unload them.
- **Work must be bounded by input size, not total input length.** A phase-vocoder
  stretch over a whole multi-minute waveform needs gigabytes of device memory and
  fails; `tts/backend/tts_engine.py` stretches in bounded, overlapping windows
  instead. Apply the same rule to any new per-request transform.
- **Temp files are removed on success, failure and timeout** — use
  `try/finally`, not a success path.
- **Caches on long-lived objects hold paths and metadata, not payloads.** A
  `63 MB` ONNX file belongs on disk; caching its bytes retains them forever for
  nothing.

Verify with `python3 agent_tools/check_tts_memory.py`, which starts the real
backend, serves a run of requests and fails if RSS drifts or a temp file is left
behind.

### Change log discipline (docs/CHANGELOG.md)

- Every **big change** gets an entry in `docs/CHANGELOG.md`, newest first. Big
  means anything a user could hit and be confused by: runtime templates
  (`stab/runtime/`), secrets/credentials handling, install/remove, networking,
  `stab` state or config behavior — and any bug whose cause is not obvious from
  the symptom. Routine refactors, typo fixes, and internal-only churn do not.
- An entry always answers three questions, in this order:
  1. **Observed behavior** — what actually happened. Quote the exact error text
     or command output when there is one. This is the issue we found.
  2. **Intended behavior** — what the tool should do instead.
  3. **Change** — what was done, which files, and the verification commands and
     their result.
- The entry lands in the **same commit** as the change it describes — never a
  separate docs commit. Because of that the entry must not cite its own commit
  hash (it would change the hash); the file's git history carries it.
- If a fix comes from a diagnosis, record the mechanism (why it broke), not just
  the symptom.
- When a fix is fail-safe-by-design (drop/ignore instead of error), say so
  explicitly under Intended behavior so the next reader does not "fix" it back.
- Bumping `stab/runtime/embed.go`'s `TemplateVersion` is part of a runtime
  change, never a separate one; mention it in the entry.

### Release history (one commit per release)

- **One commit per release.** A release commit holds everything since the previous
  release and nothing else, and its message *is* the release notes: what changed, and
  what someone upgrading will notice. The tag points at that commit.
- **Never fold a previous release into a new one.** Squashing `0.1.0` and `0.2.0` into
  one commit destroys the boundary between them: the older tree is then in no branch,
  no tag and no history, and the changelog is the only record of what changed. This
  happened once here, deliberately, and it is not to happen again.
- **Amending is for an unreleased release.** While a release commit is local — not
  pushed, or pushed but not published — fold follow-up fixes into it with
  `git commit --amend` and move the tag (`git tag -f -a`). Once it is published, stop:
  the next change belongs to the next release, and rewriting a published tag makes the
  Release page and the code disagree.
- **A tag is not a Release.** Pushing a tag does not create a GitHub Release; the notes
  still have to be published from that tag.
- **Check before squashing:** `git log --oneline --decorate` — is the oldest commit
  being folded in already a release? Then stop.

## Verification before claiming done

- Go toolchain: `export PATH=/usr/local/go/bin:$PATH` (or `/usr/bin/go` from
  `golang-go`). Restore with `sudo apt-get install -y golang-go` if it vanishes.
- Both modules build and `go vet` clean: root and `stab/`.
- Reproducible `stab`: build with `-trimpath -buildvcs=false`.
- E2E: `tests/e2e/run-e2e.sh` with explicit subnets (`E2E_SUBNET`,
  `E2E_OLLAMA_SUBNET`, …). Never auto-pick Docker test subnets.
- Big changes also have a `docs/CHANGELOG.md` entry with the observed behavior,
  intended behavior, and change — see "Change log discipline" above.
