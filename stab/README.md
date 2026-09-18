# stab

stab starts a reproducible AI coding workspace for any local project. It builds a per-project Docker image, runs a container with the host user's UID/GID, opens a tmux workspace, and launches the Pi coding agent in the main window.

stab is intentionally small: Docker runs the workspace, tmux manages terminal layout, SQLite records known projects, and plain files hold configuration. No provider accounts, bundled models, or API endpoints ship with this package.

## Quick start

```sh
make build
./stab install
cd /path/to/project
stab .
```

Common commands:

```text
stab .                 start or attach to the current project
stab list              list known projects and containers
stab bash              attach to the shell window
stab attach [project] [window]
stab status [project]
stab kill [all]
stab settings          edit local configuration
stab update            rebuild with Docker --pull --no-cache, then recreate
stab check             verify Docker and stab state dirs
stab version
```

`stab .` is idempotent. Running it again from the same project attaches to the existing container instead of creating another one.

## Architecture

```text
host
├── project/
│   ├── source files
│   └── .stables/
│       ├── Dockerfile        image recipe for this project
│       ├── Dockerfile.gpu    GPU variant
│       ├── entrypoint.sh     uid/gid alignment + startup glue
│       └── tmux.conf         tmux layout and keybindings
│
├── ~/.stables/
│   ├── config.json           stab settings
│   ├── stab.db                 SQLite registry
│   ├── models.json           Pi model config; user supplied
│   ├── secrets.env           API keys; mode 0600
│   └── projects/<id>/state/  per-project persistent state
│
└── ~/.pi/agent/
    ├── skills/
    ├── extensions/
    ├── npm/
    └── git/

Docker container: stab-<project-id>
├── /workspace                bind mount of project/
├── /stab-state                 bind mount of ~/.stables/projects/<id>/state/
├── /home/coder/.pi/agent     bind mounts for Pi skills/extensions/packages
├── code-server               browser IDE on localhost:<port>
└── tmux session "stab"
    ├── main                  Pi coding agent
    ├── shell                 interactive bash for pytest/build/debug
    ├── logs                  runtime log tail
    └── help                  tmux keybinding help
```

Data flow:

```text
stab .
  ├─ resolve current project path -> stable project id
  ├─ seed project/.stables defaults when needed
  ├─ build project image when missing or template changed
  ├─ create ~/.stables/projects/<id>/state before Docker sees it
  ├─ docker run stab-<id>
  ├─ verify coder can write ~/.pi/agent inside container
  ├─ prewarm Pi npm packages when node_modules is missing
  └─ create or attach tmux session
```

`stab update` is the heavy refresh path. It rebuilds with `--pull --no-cache`, so Docker fetches the latest base image and reruns install layers, then stab recreates the container.

## tmux workspace

stab creates one tmux session named `stab` per project container. Multiple terminals attach through grouped sessions, so they share windows but keep independent cursor/window focus.

Default windows:

| Window | Purpose |
|--------|---------|
| `main` | Pi coding agent |
| `shell` | project shell for commands such as `pytest`, `go test`, or builds |
| `logs` | runtime logs |
| `help` | tmux shortcut reference |

Useful keys:

| Key | Action |
|-----|--------|
| `F1` | toggle help window |
| `F2` | split horizontally |
| `F3` | split vertically |
| `F4` | detach |
| `F5` | kill current pane |
| `F6` | kill current window |
| `M-1`..`M-4` | jump to main/shell/logs/help |
| `C-b <arrow>` | switch pane |
| `C-b z` | zoom pane |
| `C-b ?` | full tmux help |

## Configuration and state

Global state lives under `~/.stables` by default:

- `config.json` controls the runtime image, harness command, code-server port, GPU setting, network mode, resource limits, and mount policy.
- `stab.db` records projects, deployments, containers, tmux sessions, and audit events.
- `models.json` is copied into each project's `.stables/` directory when the project starts or updates.
- `secrets.env` stores API keys and is written with mode `0600`.

Pi resources live under `~/.pi/agent` and are mounted into each container so skills, extensions, and installed packages are shared across workspaces.

Project defaults live under `<project>/.stables/`. Edit the Dockerfile or tmux config there, then run:

```sh
stab update
```

## Isolation model

stab runs commands inside Docker, not on the host. The default profile is practical rather than locked down: it uses Docker bridge networking so code-server is reachable, gives the container a writable root filesystem, allows the entrypoint to align the `coder` user to the host UID/GID, and mounts the host Docker socket when available so tools inside stab can start sibling containers.

Safety checks still apply:

- project paths are canonicalized before mounting
- home directories and credential directories such as `.ssh`, `.gnupg`, `.docker`, and Docker sockets are rejected
- only referenced secrets are injected into a container
- secret values are redacted from stab errors
- per-project state is isolated under `~/.stables/projects/<id>/state`

When the Docker socket is mounted, subcontainers are created by the host Docker daemon. Use `$STAB_HOST_PROJECT_DIR` for bind mounts that need the host project path; `/workspace/...` is only the stab container path.

Use `stab settings` to tighten network, GPU, CPU, memory, and mount behavior for stricter environments.

## Build and test

```sh
make build      # build ./stab
make test       # run unit tests
make vet        # run go vet
make install    # install stab to ~/.local/bin
```

Run directly during development:

```sh
go run ./cmd/stab --help
```

## Provider configuration

stab ships no model providers or API keys. Bring your own Pi `models.json` and secrets:

```text
~/.stables/
├── models.json
└── secrets.env
```

Use environment references in `models.json`, for example `$OPENAI_API_KEY`, and put real values in `secrets.env`. stab injects only variables referenced by the active project's model config.

## Release checklist

Before publishing:

```sh
rm -f stab
rm -rf .stables
make test
```

Confirm no private endpoints, model names, or API keys are present in source files.
