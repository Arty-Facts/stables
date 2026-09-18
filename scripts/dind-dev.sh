#!/usr/bin/env bash
set -euo pipefail

SOCK=${STABLES_DOCKER_SOCK:-/tmp/stables-docker.sock}
ROOT=${STABLES_DOCKER_ROOT:-/tmp/stables-docker-root}
LOG=${STABLES_DOCKER_LOG:-/tmp/stables-dockerd.log}
PID=${STABLES_DOCKER_PID:-/tmp/stables-docker.pid}

case "${1:-start}" in
  start)
    if docker -H "unix://$SOCK" info >/dev/null 2>&1; then
      echo "stables: nested Docker already running at $SOCK"
      echo "export DOCKER_HOST=unix://$SOCK"
      exit 0
    fi
    sudo pkill -F "$PID" 2>/dev/null || true
    sudo rm -rf "$ROOT" "$LOG" "$PID" "$SOCK"
    sudo dockerd \
      --host="unix://$SOCK" \
      --pidfile="$PID" \
      --data-root="$ROOT" \
      --storage-driver=vfs \
      --iptables=false \
      --bridge=none \
      --ip-forward=false \
      --ip-masq=false \
      > "$LOG" 2>&1 &
    for _ in $(seq 1 40); do
      if sudo test -S "$SOCK" && sudo docker -H "unix://$SOCK" info >/dev/null 2>&1; then
        sudo chmod 666 "$SOCK" || true
        echo "stables: nested Docker ready at $SOCK"
        echo "export DOCKER_HOST=unix://$SOCK"
        exit 0
      fi
      sleep 1
    done
    echo "stables: nested Docker failed" >&2
    sudo tail -120 "$LOG" >&2 || true
    exit 1
    ;;
  stop)
    sudo pkill -F "$PID" 2>/dev/null || true
    sudo rm -f "$SOCK" "$PID"
    echo "stables: nested Docker stopped"
    ;;
  logs)
    sudo tail -200 "$LOG"
    ;;
  *)
    echo "usage: scripts/dind-dev.sh [start|stop|logs]" >&2
    exit 2
    ;;
esac
