#!/usr/bin/env bash
set -euo pipefail

TARGET_UID="${PUID:-1000}"
TARGET_GID="${PGID:-1000}"

# Adjust the coder user/group to match the host UID/GID so files created in the
# bind-mounted workspace are owned by the host user.
CUR_GID="$(id -g coder)"
CUR_UID="$(id -u coder)"

if [ "$CUR_GID" != "$TARGET_GID" ]; then
    groupmod -o -g "$TARGET_GID" coder 2>/dev/null || true
fi
if [ "$CUR_UID" != "$TARGET_UID" ]; then
    usermod -o -u "$TARGET_UID" coder 2>/dev/null || true
fi

# Re-own the coder home for the aligned uid. usermod re-targets the account
# but leaves the image's original uid on /home/coder itself (mode 700), which
# traps every tool that writes under $HOME (Pi, npm, git) in EACCES when the
# host uid differs from the image uid — for example remote servers whose user
# is not uid 1000. Only the top level is touched here: the bind-mounted trees
# beneath .pi/agent are host data and are handled (non-recursively) below.
chown coder:coder /home/coder 2>/dev/null || true
# Container-local dot dirs (npm cache, code-server state) are ours to re-own;
# they are never bind-mounted. Skipped when absent; failures are not fatal.
for d in /home/coder/.npm /home/coder/.cache; do
    [ -d "$d" ] && chown -R coder:coder "$d" 2>/dev/null || true
done
# Top-level dotfiles (.bashrc, .profile, ...).
chown coder:coder /home/coder/.[!.]* 2>/dev/null || true

PROJECT_DIR="${STAB_PROJECT_DIR:-/workspace}"
PROJECT_NAME="${STAB_PROJECT_NAME:-project}"

# Pi's agent dir must be writable by coder. Docker creates the mount-target
# parent as root when it is absent from the image, and Pi (running as coder)
# needs to create settings.json.lock (its lock dir) and auth.json there.
# Non-recursive chown: the host-owned skills/extensions mounted beneath this
# dir must never be re-owned, and a read-only project mount must not abort the
# script (set -e) before this runs.
mkdir -p /home/coder/.pi/agent
chown coder:coder /home/coder/.pi /home/coder/.pi/agent 2>/dev/null || true

# Pre-create Pi's credential store so model availability refresh can always
# open (and never has to create) auth.json.
AUTH_JSON="/home/coder/.pi/agent/auth.json"
if [ ! -f "$AUTH_JSON" ]; then
    echo '{}' > "$AUTH_JSON"
    chown coder:coder "$AUTH_JSON"
    chmod 600 "$AUTH_JSON"
fi

# Remaining code-server state dirs and the project's .stables dir must be
# writable by coder. The sessions mkdir is best-effort so read-only mounts
# cannot skip the chown that follows.
mkdir -p "$PROJECT_DIR/.stables/sessions" 2>/dev/null || true
chown -R coder:coder \
    /home/coder/.config \
    /home/coder/.local \
    /stab-state \
    "$PROJECT_DIR/.stables" 2>/dev/null || true

# If the host Docker socket is mounted, make coder a member of the socket's
# numeric group so Docker CLI and Compose can start sibling containers.
if [ -S /var/run/docker.sock ]; then
    DOCKER_GID="$(stat -c '%g' /var/run/docker.sock)"
    if ! getent group "$DOCKER_GID" >/dev/null 2>&1; then
        groupadd -g "$DOCKER_GID" dockerhost 2>/dev/null || true
    fi
    DOCKER_GROUP="$(getent group "$DOCKER_GID" | cut -d: -f1 || true)"
    [ -n "$DOCKER_GROUP" ] && usermod -aG "$DOCKER_GROUP" coder 2>/dev/null || true
fi

# Set the code-server window title to the real project name so multiple
# projects are easy to tell apart.
SETTINGS_DIR="/home/coder/.local/share/code-server/User"
mkdir -p "$SETTINGS_DIR"
cat > "$SETTINGS_DIR/settings.json" <<EOF
{
  "window.title": "${PROJECT_NAME} \u00b7 \${activeEditorShort}"
}
EOF
chown -R coder:coder /home/coder/.local/share/code-server 2>/dev/null || true

# Install the project's models.json into Pi's config dir, expanding $VAR /
# ${VAR} secret references from the environment (secrets are injected by the
# controller; they are never stored in models.json). If a previous models.json
# exists (Pi may have added providers via login), merge instead of overwrite:
# the project (external) providers win on conflict, but Pi-added providers are
# preserved so a login survives `stab update` without re-authenticating.
#
# Fail safe: a secret that was not injected expands to the empty string, and Pi
# rejects the WHOLE models.json when any provider has "apiKey": "" (minLength 1)
# — killing every model, not just the keyless one. So a provider whose apiKey is
# empty after expansion is dropped, never merged over a working entry. This is
# the normal case for an extensions-only build (`make build`) with no baked
# secrets, or a host with an empty ~/.stables/secrets.env.
PROJECT_MODELS="$PROJECT_DIR/.stables/models.json"
USER_MODELS="/home/coder/.pi/agent/models.json"
if [ -f "$PROJECT_MODELS" ]; then
    mkdir -p "$(dirname "$USER_MODELS")"
    envsubst < "$PROJECT_MODELS" > /tmp/stab-models-resolved.json
    if command -v node >/dev/null 2>&1; then
        node -e '
const fs = require("fs");
const resolvedPath = process.argv[1];
const curPath = process.argv[2];
const resolved = JSON.parse(fs.readFileSync(resolvedPath, "utf8"));
const cur = fs.existsSync(curPath) ? JSON.parse(fs.readFileSync(curPath, "utf8")) : {};

// apiKey present but empty (or whitespace) => its secret was not injected.
function keyless(p) {
  if (!p || p.apiKey === undefined) return false;
  return typeof p.apiKey === "string" && p.apiKey.trim() === "";
}

const baseProviders = resolved.providers || {};
const curProviders = (cur && cur.providers) || {};
const merged = {};
const dropped = [];
for (const [name, p] of Object.entries(baseProviders)) {
  if (keyless(p)) { dropped.push(name); continue; }
  merged[name] = p;
}
// Existing providers survive unless a usable (keyed) project provider replaces them.
for (const [name, p] of Object.entries(curProviders)) {
  if (merged[name]) continue;
  merged[name] = p;
}

const out = Object.assign({}, cur, resolved);
out.providers = merged;
fs.writeFileSync(curPath, JSON.stringify(out, null, 2) + "\n");
if (dropped.length > 0) {
  console.error("[entrypoint] WARNING: skipped providers with missing secrets (apiKey empty): " + dropped.join(", "));
  console.error("[entrypoint] add the keys to ~/.stables/secrets.env on the host, or use /login");
}
if (Object.keys(merged).length === 0) {
  console.error("[entrypoint] WARNING: no usable model providers — agent has no models");
}
' /tmp/stab-models-resolved.json "$USER_MODELS"
    else
        # No node: keep the old overwrite behavior (envsubst output only).
        mv /tmp/stab-models-resolved.json "$USER_MODELS"
    fi
    chown coder:coder "$USER_MODELS" 2>/dev/null || true
    echo "[entrypoint] installed models.json -> $USER_MODELS"
fi

exec gosu coder code-server "$@"
