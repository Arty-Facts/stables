# Dev containers

Two Docker-capable dev setups are provided.

## Host Docker socket

Use `.devcontainer/socket/devcontainer.json` when host already runs Docker.

Pros:
- fastest
- normal networking/ports
- closest to user install behavior

Tradeoff:
- container controls host Docker daemon

## Docker-in-Docker

Use `.devcontainer/dind/devcontainer.json` when you want isolated Docker state.

Requires privileged container.

Pros:
- isolated Docker images/containers
- good for CI-style tests

Tradeoff:
- slower
- needs `--privileged`

## Restricted containers

`scripts/dind-dev.sh` can start a degraded daemon with `vfs`, no bridge, and no iptables. Some hosted coding containers still block `unshare`, so image pulls/runs fail. In that case use one of the devcontainer configs above.
