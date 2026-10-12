#!/usr/bin/env bash
## check_stab_harness.sh — prove a freshly built `stab` delivers a working
## harness (Pi) inside its container, and that a missing harness fails loudly.
##
## Inputs (environment):
##   STAB_BIN      path to the stab binary to test (required)
##   STAB_SCRATCH  scratch dir to use (default: mktemp -d under /tmp)
##   STAB_KEEP=1   keep container, image and scratch dir for inspection
##
## Outputs: one PASS/FAIL line per check on stdout, plus captured evidence;
##   exit status 0 when every check passes, 1 otherwise.
##
## Side effects: builds one Docker image from the embedded runtime template,
##   runs one container on the default bridge network, and removes both through
##   `stab kill` / `docker rmi` on exit (skipped with STAB_KEEP=1). The scratch
##   HOME has no host-side Pi install, which is the case the check is about: the
##   container must provide the harness by itself.
set -uo pipefail

STAB_BIN="${STAB_BIN:?set STAB_BIN to the stab binary to test}"
STAB_SCRATCH="${STAB_SCRATCH:-$(mktemp -d /tmp/stab-harness-check.XXXXXX)}"
HOME_DIR="$STAB_SCRATCH/home"
PROJ_DIR="$STAB_SCRATCH/project"
CONTAINER=""
IMAGE=""

fails=0
pass() { printf '  PASS  %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; fails=$((fails + 1)); }
# check <description> <command...>
check() {
    local desc="$1"; shift
    if out="$("$@" 2>&1)"; then
        pass "$desc"
    else
        fail "$desc"
        printf '        %s\n' "$out"
    fi
}

cleanup() {
    if [ -n "${STAB_KEEP:-}" ]; then
        printf '\nkept: container=%s image=%s scratch=%s\n' "$CONTAINER" "$IMAGE" "$STAB_SCRATCH"
        return
    fi
    if [ -d "$PROJ_DIR" ]; then (cd "$PROJ_DIR" && HOME="$HOME_DIR" "$STAB_BIN" kill >/dev/null 2>&1); fi
    [ -n "$IMAGE" ] && docker rmi -f "$IMAGE" >/dev/null 2>&1
    rm -rf "$STAB_SCRATCH"
}
trap cleanup EXIT INT TERM HUP

mkdir -p "$HOME_DIR/.stables" "$PROJ_DIR"
# gpu off keeps the check on the CPU template (the GPU template is a duplicate
# of the same install block, verified separately by diff/strings).
cat > "$HOME_DIR/.stables/config.json" <<'JSON'
{
  "version": 1,
  "state_dir": ".stables",
  "harness_command": "pi",
  "code_server_port": 8080,
  "profile": { "name": "default", "network": "bridge", "gpu": "off" }
}
JSON

printf 'stab harness check\n  binary: %s\n  scratch: %s\n' "$STAB_BIN" "$STAB_SCRATCH"

# The host-side Pi agent dir mounted into the container must NOT contain a Pi
# install, otherwise the check would pass for the wrong reason.
mkdir -p "$HOME_DIR/.pi/agent/extensions" "$HOME_DIR/.pi/agent/skills"
if [ -e "$HOME_DIR/.pi/agent/bin/pi" ]; then
    fail "scratch host agent dir must not contain bin/pi"
    exit 1
fi
pass "host agent dir has no Pi install"

printf '\nstarting deployment (stab . ; the attach step cannot work without a tty)\n'
(cd "$PROJ_DIR" && HOME="$HOME_DIR" "$STAB_BIN" . </dev/null >/dev/null 2>&1)
# The deployment is identified by the host path it mounts: the project dir is
# unique to this run, so no other container can match.
CONTAINER="$(docker ps --filter "label=stab.kind=local" --format '{{.ID}}' | while read -r id; do
    docker inspect -f '{{range .Mounts}}{{.Source}}{{"\n"}}{{end}}' "$id" | grep -qxF "$PROJ_DIR" && echo "$id"
done | head -1)"
if [ -z "$CONTAINER" ]; then
    fail "no running container mounted from $PROJ_DIR"
    exit 1
fi
IMAGE="$(docker inspect -f '{{.Config.Image}}' "$CONTAINER")"
pass "container running: $CONTAINER ($IMAGE)"

printf '\nchecks\n'
check "tmux main window exists" bash -c \
    "docker exec -u coder '$CONTAINER' tmux list-windows -t stab -F '#{window_name}' | grep -qx main"
check "harness resolves on PATH (non-login shell)" bash -c \
    "docker exec -u coder '$CONTAINER' pi --version"
check "harness resolves on PATH (login shell)" bash -c \
    "docker exec -u coder '$CONTAINER' bash -lc 'command -v pi'"
check "harness actually draws in the main window" bash -c \
    "docker exec -u coder '$CONTAINER' tmux capture-pane -p -t stab:main | grep -qv '^$'"
check "managed install writable by the container user" bash -c \
    "docker exec -u coder '$CONTAINER' sh -c 'touch /opt/pi-agent/install/.wtest && rm -f /opt/pi-agent/install/.wtest'"
check "no unreachable root-owned install left in the image" bash -c \
    "! docker run --rm --entrypoint sh '$IMAGE' -c 'test -e /root/.pi'"
check "login-shell PATH entry installed" bash -c \
    "docker run --rm --entrypoint sh '$IMAGE' -c 'grep -q /opt/pi-agent/bin /etc/profile.d/pi-agent.sh'"

printf '\nchecks: missing harness must fail loudly, not empty the layout\n'
python3 - "$HOME_DIR/.stables/config.json" <<'PY'
import json, sys
p = sys.argv[1]
cfg = json.load(open(p))
cfg["harness_command"] = "definitely-not-a-harness"
json.dump(cfg, open(p, "w"), indent=2)
PY
err="$(cd "$PROJ_DIR" && HOME="$HOME_DIR" "$STAB_BIN" . </dev/null 2>&1)"
rc=$?
if [ "$rc" -ne 0 ] && printf '%s' "$err" | grep -q "not available inside"; then
    pass "stab . refuses to attach and names the missing harness"
    printf '        %s\n' "$(printf '%s' "$err" | grep 'not available inside' | head -1)"
else
    fail "stab . should fail with an actionable error (rc=$rc)"
    printf '        %s\n' "$err"
fi

printf '\n'
if [ "$fails" -eq 0 ]; then
    printf 'RESULT: all checks passed\n'
    exit 0
fi
printf 'RESULT: %d check(s) failed\n' "$fails"
exit 1
