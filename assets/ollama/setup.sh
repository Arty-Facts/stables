#!/usr/bin/env sh
set -eu

DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$DIR"

PORT="${OLLAMA_HOST_PORT:-11434}"
NAME="${STACK_NAME:-stables}"

if ! docker info >/dev/null 2>&1; then
  echo "stables: Docker is not running" >&2
  exit 1
fi

if docker compose version >/dev/null 2>&1; then
  COMPOSE="docker compose"
elif command -v docker-compose >/dev/null 2>&1; then
  COMPOSE="docker-compose"
else
  echo "stables: Docker Compose is required" >&2
  exit 1
fi

# The published port is bound on the Docker daemon's host, which is not always
# "localhost" from the caller's netns (shared-daemon / remote setups). Use an
# in-container probe (via the daemon socket) as the authoritative readiness
# signal: it resolves to the container's own 127.0.0.1, so it works in every
# topology with no routing assumptions.
echo "stables: starting Ollama..."
$COMPOSE up -d ollama

echo "stables: waiting for Ollama..."
i=0
until $COMPOSE exec -T ollama ollama list >/dev/null 2>&1; do
  i=$((i+1))
  if [ "$i" -ge 90 ]; then
    echo "stables: Ollama did not become ready" >&2
    exit 1
  fi
  sleep 2
done
echo "stables: Ollama is ready."

echo "stables: Ollama API on daemon host port $PORT"
