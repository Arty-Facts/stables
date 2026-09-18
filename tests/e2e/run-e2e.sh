#!/usr/bin/env bash
# Stable OSS two-container E2E test.
#
# Spins up two fresh Docker containers (both on the host daemon):
#   - stables-e2e-client : acts as the "host" — runs `stables remote ...`
#   - stables-e2e-remote : the target server (sshd + docker CLI + host socket)
#
# Verifies:
#   1. remote install of stab (binary + state dirs on the remote)
#   2. remote install of Ollama + Open WebUI (compose stacks on shared daemon)
#   3. pulling a tiny model on the remote
#   4. calling that model over HTTP
#   5. remote container restart (reboot simulation) — install survives
#
# Requires: docker CLI + host daemon reachable from the caller (stab
# container with the host Docker socket, or any docker host).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="$ROOT/stables"
IMAGE="stables-e2e:latest"
NET="stables-e2e"
SUBNET="${E2E_SUBNET:-10.250.0.0/24}"
OLLAMA_SUBNET="${E2E_OLLAMA_SUBNET:-10.250.1.0/24}"
REMOTE="stables-e2e-remote"
CLIENT="stables-e2e-client"
NAME="e2e"
COMPOSE_NET="${NAME}-llm"   # the compose template declares an explicit network: <name>-llm
OLLAMA_PORT="${E2E_OLLAMA_PORT:-12434}"
WEBUI_PORT="${E2E_WEBUI_PORT:-13000}"
TUNNEL_PORT="${E2E_TUNNEL_PORT:-11435}"
WEBUI_NET="${NAME}-webui"
MODEL="${E2E_MODEL:-qwen2.5:0.5b}"   # small CHAT model (all-minilm is an embedding model — cannot serve /v1/chat/completions)
USER_NAME="tester"
ADDR="$USER_NAME@stables-e2e-remote"
WORK="$(mktemp -d)"
PUBKEY="$WORK/id_ed25519.pub"
KEEP="${KEEP:-0}"
PASS=0

cleanup() {
    rm -rf "$WORK"
    if [ "$KEEP" != "1" ] && [ -n "${RAN:-}" ]; then
        docker rm -f "$CLIENT" "$REMOTE" >/dev/null 2>&1 || true
        if [ "$KEEP" != "1" ]; then
            # Tear down the ollama compose stack + its network + data volume so a
            # re-run starts clean (they live on the SHARED daemon).
            docker rm -f "$NAME-ollama" "$NAME-open-webui" >/dev/null 2>&1 || true
            docker network rm "$COMPOSE_NET" "$WEBUI_NET" >/dev/null 2>&1 || true
            # Named volumes get a compose-project prefix (the directory name, "ollama");
            # remove both the prefixed and bare forms.
            docker volume rm "ollama_$NAME-open-webui-data" >/dev/null 2>&1 || true
            docker volume rm "$NAME-open-webui-data" >/dev/null 2>&1 || true
            docker volume rm "webui_$NAME-open-webui-data" >/dev/null 2>&1 || true
            docker network rm "$NET" >/dev/null 2>&1 || true
            # Verify the host route for our subnet is gone (leave no residue).
            if host_routes 2>/dev/null | grep -F "$SUBNET" >/dev/null; then
                printf 'WARN: host still has route for %s after teardown\n' "$SUBNET" >&2
            else
                printf 'teardown verified: no host route left for %s\n' "$SUBNET" >&2
            fi
        fi
    fi
}
trap cleanup EXIT

step() { printf '\n=== %s ===\n' "$*"; }
ok()   { PASS=$((PASS+1)); printf 'PASS: %s\n' "$*"; }
fail() { printf 'FAIL: %s\n' "$*"; exit 1; }

host_routes() {
    # Real host netns routes (this caller may be a container sharing the daemon).
    docker run --rm --net=host alpine:3.20 ip route 2>/dev/null \
      || { command -v ip >/dev/null && ip route; }
}

subnet_clear() {
    local cand="$1"
    python3 - "$cand" <<'PYEOF'
import ipaddress, subprocess, sys
cand = ipaddress.ip_network(sys.argv[1], strict=False)
lines = subprocess.run(
    ["docker", "run", "--rm", "--net=host", "alpine:3.20", "ip", "route"],
    capture_output=True, text=True).stdout.splitlines()
bad = []
for line in lines:
    parts = line.split()
    if len(parts) < 2 or "/" not in parts[1]:
        continue
    try:
        route = ipaddress.ip_network(parts[1], strict=False)
    except ValueError:
        continue
    if cand.overlaps(route):
        bad.append(line.strip())
if bad:
    print("OVERLAP with host routes:")
    for b in bad:
        print("  " + b)
    sys.exit(1)
print("host routes clear for", cand)
PYEOF
}

overlaps_any_network() {
    local cand="$1" net ip
    for net in $(docker network ls --format '{{.Name}}'); do
        case "$net" in bridge|host|none|"$NET") continue;; esac
        ip=$(docker network inspect "$net" --format '{{(index .IPAM.Config 0).Subnet}}' 2>/dev/null || true)
        [ -n "$ip" ] || continue
        if python3 -c '
import ipaddress, sys
a = ipaddress.ip_network(sys.argv[1], strict=False)
b = ipaddress.ip_network(sys.argv[2], strict=False)
sys.exit(0 if a.overlaps(b) else 1)
' "$cand" "$ip"; then
            return 0
        fi
    done
    return 1
}

command -v docker >/dev/null || { echo "need docker CLI"; exit 1; }
[ -x "$BIN" ] || { echo "build first: make -C $ROOT build"; exit 1; }

# --- 0. preconditions ------------------------------------------------------
step "preconditions"
[ -S /var/run/docker.sock ] || fail "host docker socket not visible"
docker info >/dev/null 2>&1 || fail "docker daemon unreachable"
for p in "$OLLAMA_PORT" "$WEBUI_PORT"; do
    if docker ps --format '{{.Names}} {{.Ports}}' | grep -F ":$p->" >/dev/null; then
        fail "host port $p already published; set E2E_OLLAMA_PORT/E2E_WEBUI_PORT to free ports"
    fi
done
RAN=1
docker rm -f "$REMOTE" "$CLIENT" >/dev/null 2>&1 || true
# Clean resources from an interrupted run before checking host routes. The
# Ollama Compose network is separate from the E2E SSH network.
docker rm -f "$NAME-ollama" "$NAME-open-webui" >/dev/null 2>&1 || true
docker network rm ollama_default >/dev/null 2>&1 || true

# Subnet discipline: never let docker auto-pick a test subnet — auto subnets
# (172.20+/172.30/172.31…) have historically collided with host VPN/LAN/remote
# routing and poisoned the host routing table. Use an explicit isolated subnet
# and reject it if it overlaps host routes or any existing docker network.
test_subnet_ok=1
subnet_clear "$SUBNET" || test_subnet_ok=0
subnet_clear "$OLLAMA_SUBNET" || test_subnet_ok=0
if overlaps_any_network "$SUBNET"; then test_subnet_ok=0; fi
if overlaps_any_network "$OLLAMA_SUBNET"; then test_subnet_ok=0; fi
[ "$test_subnet_ok" = 1 ] || fail "subnet $SUBNET not clear; pick another E2E_SUBNET (isolated /24)"
existing_subnet="$(docker network inspect "$NET" --format '{{(index .IPAM.Config 0).Subnet}}' 2>/dev/null || true)"
if [ -n "$existing_subnet" ] && [ "$existing_subnet" != "$SUBNET" ]; then
  docker network rm "$NET" >/dev/null 2>&1 \
    || fail "existing E2E network $NET uses $existing_subnet and cannot be removed"
  existing_subnet=""
fi
if [ -z "$existing_subnet" ]; then
  docker network create --subnet "$SUBNET" "$NET" >/dev/null \
    || fail "cannot create E2E network with subnet $SUBNET; choose E2E_SUBNET"
  ok "network $NET created on explicit subnet $SUBNET"
fi

# --- 1. build test image + keys --------------------------------------------
step "build e2e image"
docker build -q -t "$IMAGE" -f "$ROOT/tests/e2e/Dockerfile.ssh" "$ROOT/tests/e2e" >/dev/null \
  || fail "image build failed"
ok "image built"

ssh-keygen -q -t ed25519 -N '' -f "$WORK/id_ed25519" >/dev/null 2>&1 \
  || fail "ssh-keygen failed"
ok "ssh key generated"

# --- 2. bring up remote container -------------------------------------------
step "start remote container"
docker run -d --name "$REMOTE" --network "$NET" \
  -e E2E_MODE=server -e "E2E_PUBKEY=$(cat "$PUBKEY")" \
  -v /var/run/docker.sock:/var/run/docker.sock \
  "$IMAGE" >/dev/null || fail "remote container start"
ok "remote container running"

# --- 3. bring up client container (the "host") ------------------------------
step "start client container"
docker run -d --name "$CLIENT" --network "$NET" \
  -e E2E_MODE=client \
  "$IMAGE" >/dev/null || fail "client container start"
cexec() { docker exec -i --env HOME=/home/tester "$CLIENT" bash -lc "$*"; }
cexec "command -v ssh && command -v scp" >/dev/null || fail "client missing ssh/scp"
# Push the binary and key through the Docker API (stdin) — bind-mount sources
# must exist on the daemon's host filesystem, which they do not from a
# container-shared daemon.
cexec "mkdir -p ~/bin && cat > ~/bin/stables && chmod +x ~/bin/stables && test -x ~/bin/stables" \
  < "$BIN" || fail "push stables binary to client"
cexec "mkdir -p ~/.ssh && cat > ~/.ssh/id_ed25519 && chmod 700 ~/.ssh && chmod 600 ~/.ssh/id_ed25519" \
  < "$WORK/id_ed25519" || fail "install client ssh key"
ok "client container ready (binary + ssh key installed)"

# wait for remote sshd (from the client — exercises real ssh path)
for i in $(seq 1 30); do
    if cexec "ssh -o BatchMode=yes $ADDR true" >/dev/null 2>&1; then break; fi
    sleep 2
done
cexec "ssh -o BatchMode=yes $ADDR true" >/dev/null || fail "remote ssh not ready"
ok "client -> remote ssh works"

# --- 4. remote install stab ---------------------------------------------
step "stables remote install stab"
cexec "~/bin/stables remote install stab $ADDR" || fail "remote install stab"
# NOTE: the remote script is the SOLE ssh command, multi-line single-quoted.
# OpenSSH sends argv with newlines unquoted, so the remote shell executes it
# line-by-line — every line must be a complete command with its own guard.
# Do NOT wrap in `bash -lc '...'` (bash gets a bare `-c` → error + broken guard).
cexec "ssh -o BatchMode=yes $ADDR '
  test -x ~/stables || exit 1
  test -x ~/.local/bin/stables || exit 1
  test -x ~/.local/bin/stab || exit 1
  test -d ~/.stables || exit 1
  test -d ~/.pi/agent/skills || exit 1
  ~/.local/bin/stab --version || exit 1
  echo stab-files-ok
'" || fail "remote stab files missing"
ok "remote stab installed + files verified"

# --- 5. remote install ollama (pulls images, boots compose) ------------------
step "stables remote install ollama (may pull several GB)"
cexec "~/bin/stables remote install ollama $ADDR --name $NAME --ollama-port $OLLAMA_PORT --network-subnet $OLLAMA_SUBNET" \
  || fail "remote install ollama"
ok "remote ollama installed"

step "compose containers visible on shared daemon"
docker ps --filter "name=$NAME-ollama" --format 'table {{.Names}}\t{{.Status}}'
ok "compose stack present"

# --- 6. pull tiny model on the remote ----------------------------------------
step "stables remote ollama pull $MODEL"
cexec "~/bin/stables remote ollama pull $ADDR $MODEL" || fail "remote ollama pull"
ok "model pulled"
cexec "ssh -o BatchMode=yes $ADDR docker exec $NAME-ollama ollama list" | grep -F "$MODEL" \
  || fail "model not listed on remote"
ok "model listed on remote"

# --- 7. call the model over HTTP ----------------------------------------------
step "call model over HTTP (client container on the stack network)"
# The call runs inside an alpine client attached to the stack's own compose
# network, addressing the ollama container by compose DNS name. This is
# topology-independent: no host-published-port routing, no NAT assumptions —
# exactly what an in-stack consumer (e.g. Open WebUI) does.
chat_http() {  # $1 = url
    local url="$1"
    docker run --rm --network "$COMPOSE_NET" \
      -e CALL_URL="$url" -e CALL_MODEL="$MODEL" alpine:3.20 sh -c '
        body="{\"model\":\"$CALL_MODEL\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with exactly: STB-E2E-OK\"}],\"stream\":false}"
        wget -qO- -T 240 --header="Content-Type: application/json" --post-data="$body" "$CALL_URL"'
}
RESP=""
URL="http://ollama:11434/v1/chat/completions"
# First call right after `pull` can hit a cold model load (Ollama returns an
# error until the model is resident). Retry with backoff — mirrors real usage.
for attempt in 1 2 3 4 5 6 7 8 9 10; do
    RESP="$(chat_http "$URL" 2>/tmp/e2e-chat.err || true)"
    case "$RESP" in *STB-E2E-OK*) break;; esac
    printf '  model not ready yet (attempt %d/10), retrying... [%s]\n' "$attempt" "$(tail -c 300 /tmp/e2e-chat.err 2>/dev/null | tr '\n' ' ')"
    sleep 15
done
case "$RESP" in
    *STB-E2E-OK*) ok "model responded (HTTP /v1/chat/completions)";;
    *) fail "model call failed after 10 attempts; raw=$(printf '%s' "$RESP" | head -c 400); err=$(tail -c 300 /tmp/e2e-chat.err 2>/dev/null)";;
esac
ok "model call verified"

# --- 8. reboot simulation -----------------------------------------------------
step "restart remote container (reboot simulation)"
docker restart "$REMOTE" >/dev/null || fail "remote restart"
for i in $(seq 1 30); do
    if cexec "ssh -o BatchMode=yes $ADDR true" >/dev/null 2>&1; then break; fi
    sleep 2
done
# (same ssh multi-line pattern as step 4 — see the note there)
cexec "ssh -o BatchMode=yes $ADDR '
  test -x ~/stables || exit 1
  test -x ~/.local/bin/stables || exit 1
  test -x ~/.local/bin/stab || exit 1
  test -d ~/.stables || exit 1
  ~/.local/bin/stab --version || exit 1
'" || fail "stab state lost after restart"
ok "install survives remote restart"
docker ps --filter "name=$NAME-ollama" --format '{{.Names}} {{.Status}}' | grep -q Up \
  && ok "ollama stack still up" || fail "ollama stack not up after restart"

# --- 9. compose stack removal must not leak networks/routes --------------------
step "remove ollama stack on remote; verify no leftover network/route"
cexec "~/bin/stables remote remove ollama $ADDR" >/dev/null 2>&1 || fail "remote remove ollama"
sleep 2
docker network inspect "$COMPOSE_NET" >/dev/null 2>&1 \
  && fail "compose network $COMPOSE_NET left behind after remove" \
  || ok "compose network $COMPOSE_NET removed"
if host_routes 2>/dev/null | grep -F "$OLLAMA_SUBNET" >/dev/null; then
  fail "host still has route for $OLLAMA_SUBNET after teardown"
fi
ok "no host route left for $OLLAMA_SUBNET"

# --- 10. remote install + fetch webui ------------------------------------------
step "stables remote install webui"
cexec "~/bin/stables remote install webui $ADDR --name $NAME --webui-port $WEBUI_PORT" \
  || fail "remote install webui"
docker ps --filter "name=$NAME-open-webui" --format '{{.Names}} {{.Status}}' | grep -q Up \
  || fail "webui container not running"
ok "webui stack present"

step "stables remote fetch webui"
cexec "~/bin/stables remote fetch webui $ADDR" || fail "remote fetch webui"
ok "webui fetch ok"

# --- 11. tunnel up / list / down ----------------------------------------------
step "stables tunnel remote (client -> remote)"
cexec "~/bin/stables tunnel remote $ADDR --port $TUNNEL_PORT" || fail "tunnel up"
cexec "test -s ~/.stables/tunnels/current && grep -q '\"mode\":\"remote\"' ~/.stables/tunnels/current" \
  || fail "tunnel state not written"
cexec "~/bin/stables tunnel list" | grep -q remote || fail "tunnel list shows remote"
ok "tunnel up + list"

step "stables tunnel down"
cexec "~/bin/stables tunnel down" || fail "tunnel down"
cexec "test ! -e ~/.stables/tunnels/current" || fail "tunnel state not cleared"
ok "tunnel down"

# --- 12. remote remove webui + stab -------------------------------------
step "remote remove webui"
cexec "~/bin/stables remote remove webui $ADDR" || fail "remote remove webui"
docker ps --filter "name=$NAME-open-webui" --format '{{.Names}}' | grep -q . \
  && fail "webui container left behind" || ok "webui removed"

step "remote remove stab"
cexec "~/bin/stables remote remove stab $ADDR" || fail "remote remove stab"
cexec "ssh -o BatchMode=yes $ADDR '
  test ! -x ~/.local/bin/stab || exit 1
  echo stab-removed-ok
'" || fail "stab binary not removed"
ok "stab removed"

# --- summary -----------------------------------------------------------------
printf '\n=========================================\n'
printf 'E2E RESULT: PASS (%s checks)\n' "$PASS"
if [ "$KEEP" = "1" ]; then
    printf 'Containers left running; teardown: docker rm -f %s %s\n' "$CLIENT" "$REMOTE"
else
    printf 'Teardown complete; no e2e containers/networks/volumes left.\n'
fi
