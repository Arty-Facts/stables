#!/usr/bin/env bash
set -euo pipefail

: "${DOCKER_HOST:=unix:///tmp/stables-docker.sock}"
export DOCKER_HOST

docker info >/dev/null

echo "stables: pulling alpine for nested Docker smoke"
log=$(mktemp)
if ! docker pull alpine:3.20 >"$log" 2>&1; then
  cat "$log" >&2
  if grep -qiE 'unshare: operation not permitted|operation not permitted|permission denied' "$log"; then
    cat >&2 <<'EOF'
stables: Docker daemon started, but this container cannot run nested containers.

Cause: outer container lacks privileges needed to unpack/run container layers.
Fix one of:
  1. run this dev environment privileged
  2. mount host Docker socket into this dev environment
  3. use .devcontainer/dind or .devcontainer/socket configs from this repo
EOF
    exit 78
  fi
  exit 1
fi
rm -f "$log"

tmp=$(mktemp -d)
echo ok > "$tmp/input.txt"

docker run --rm --network none -v "$tmp:/data" alpine:3.20 sh -c 'cat /data/input.txt > /data/output.txt'

test "$(cat "$tmp/output.txt")" = ok
rm -rf "$tmp"

echo "stables: nested Docker smoke passed"
