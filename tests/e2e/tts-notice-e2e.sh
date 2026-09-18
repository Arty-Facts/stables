#!/usr/bin/env bash
# End-to-end test of the announcement queue: the path a message takes from an agent
# or a shell to the moment the client can speak it.
#
# It drives the same sequence the TUI drives — ask for messages *without taking
# them*, then take them at the moment they can be spoken — because the interesting
# behaviour is stateful and the failure mode is silent: a message read while nobody
# is listening is gone. The TUI's audio cannot be asserted from a script, so this
# covers everything up to the speaker, and `stables tts-mcp-ping` covers the
# producer's end.
#
# It creates no Docker networks and no containers, unless TTS_E2E_START=1 asks it to
# start the stack, in which case it stops only a stack it started itself. Messages
# that were already queued when the test ran are put back.
#
#   TTS_URL=http://127.0.0.1:17493   where the server is
#   TTS_E2E_START=1                  start the stack if nothing is answering
#   TTS_E2E_SKIP_IF_DOWN=1           exit 0 instead of 1 when nothing is answering
#   STABLES_BIN=/path/to/stables      the CLI to ping with
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
TTS_URL="${TTS_URL:-http://127.0.0.1:17493}"
TTS_DIR="${STABLES_TTS_DIR:-$HOME/.stables/tts}"
BIN="${STABLES_BIN:-$ROOT/stables}"
SOURCE="e2e-notice"
TEXT="the deploy finished"
STARTED_STACK=0
FAILURES=0
PREEXISTING=""

pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1"; FAILURES=$((FAILURES + 1)); }
note() { printf '  ..    %s\n' "$1"; }

# ── messages that were already waiting are not ours to destroy ────────────────
restore_preexisting() {
  [ -z "$PREEXISTING" ] && return 0
  local count
  count=$(printf '%s' "$PREEXISTING" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
  note "putting back $count message(s) that were queued before this test"
  printf '%s' "$PREEXISTING" | python3 -c '
import json, sys, urllib.request
for item in json.load(sys.stdin):
    body = json.dumps({
        "text": item.get("text", ""),
        "kind": item.get("kind", "say"),
        "source": item.get("source", ""),
    }).encode()
    urllib.request.urlopen(urllib.request.Request(
        sys.argv[1] + "/v1/notify", data=body,
        headers={"content-type": "application/json"}), timeout=10).read()
' "$TTS_URL"
}

cleanup() {
  local code=$?
  if [ "$STARTED_STACK" -eq 1 ]; then
    note "stopping the stack this test started"
    (cd "$TTS_DIR" && docker compose down >/dev/null 2>&1) || true
  fi
  exit "$code"
}
trap cleanup EXIT INT TERM

health() { curl -fsS -m 5 "$TTS_URL/health" >/dev/null 2>&1; }

# ── the server ────────────────────────────────────────────────────────────────
if ! health && [ "${TTS_E2E_START:-0}" = "1" ]; then
  if [ ! -d "$TTS_DIR" ]; then
    printf 'no stack installed at %s — run: stables install tts\n' "$TTS_DIR" >&2
    exit 1
  fi
  # Only start what is not already running, so teardown never stops the user's own
  # stack.
  if [ -z "$(cd "$TTS_DIR" && docker compose ps -q 2>/dev/null)" ]; then
    STARTED_STACK=1
    (cd "$TTS_DIR" && docker compose up -d >/dev/null 2>&1)
  fi
  for _ in $(seq 1 60); do health && break; sleep 2; done
fi

if ! health; then
  msg="nothing answering at $TTS_URL"
  if [ "${TTS_E2E_SKIP_IF_DOWN:-0}" = "1" ]; then
    printf '%s — skipping\n' "$msg"
    exit 0
  fi
  printf '%s\n  start it with: stables install tts   (or TTS_E2E_START=1)\n' "$msg" >&2
  exit 1
fi
pass "the server answers at $TTS_URL"

# ── the producer's end ────────────────────────────────────────────────────────
printf '\nproducing a message\n'
if [ ! -x "$BIN" ]; then
  fail "stables binary not found at $BIN (build it, or set STABLES_BIN)"
else
  # Remember what was waiting, so restoring it later is possible.
  PREEXISTING=$(curl -fsS "$TTS_URL/v1/notifications?consume=false" \
    | python3 -c 'import json,sys; print(json.dumps(json.load(sys.stdin).get("items", [])))')

  if out=$("$BIN" tts-mcp-ping "$TEXT" --kind ping --source "$SOURCE" --url "$TTS_URL/mcp" 2>&1); then
    pass "tts-mcp-ping queued it: $out"
  else
    fail "tts-mcp-ping failed: $out"
  fi
fi

# ── the client's sequence ─────────────────────────────────────────────────────
printf '\nthe client polls without consuming\n'
peek() { curl -fsS "$TTS_URL/v1/notifications?consume=false"; }
mine() { python3 -c '
import json, sys
items = json.load(sys.stdin).get("items", [])
print(sum(1 for i in items if i.get("source") == sys.argv[1]))
' "$SOURCE"; }

first=$(peek | mine)
if [ "$first" -ge 1 ]; then
  pass "a peek sees the message"
else
  fail "a peek did not see the message"
fi
second=$(peek | mine)
if [ "$second" -eq "$first" ]; then
  pass "peeking twice leaves it queued, so a poll cannot lose it"
else
  fail "the second peek saw $second of $first: peeking consumed something"
fi

printf '\ntaking it when it can be spoken\n'
taken=$(curl -fsS "$TTS_URL/v1/notifications" \
  | python3 -c '
import json, sys
items = json.load(sys.stdin).get("items", [])
print(sum(1 for i in items if i.get("source") == sys.argv[1]))
' "$SOURCE")
if [ "$taken" -ge 1 ]; then
  pass "taking returns the message"
else
  fail "taking returned no message of ours"
fi
left=$(peek | mine)
if [ "$left" -eq 0 ]; then
  pass "a second take finds nothing: reads are take-once"
else
  fail "the message is still queued after being taken"
fi

printf '\nthe queue is bounded\n'
capacity=$(curl -fsS "$TTS_URL/v1/notifications?consume=false" \
  | python3 -c 'import json,sys; print(json.load(sys.stdin).get("capacity", 0))')
if [ "$capacity" -gt 0 ]; then
  pass "the server reports a capacity of $capacity messages"
else
  fail "the server did not report a capacity"
fi

restore_preexisting
PREEXISTING=""

printf '\n'
if [ "$FAILURES" -gt 0 ]; then
  printf '%d check(s) failed\n' "$FAILURES" >&2
  exit 1
fi
printf 'announcement queue: all checks passed\n'
