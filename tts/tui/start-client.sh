#!/usr/bin/env bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Server URL is read from ~/.config/ratts/config.toml (server_url key).
# To override without editing the file, set RATTS_SERVER before running:
#   RATTS_SERVER=http://myserver:5000 ./start-client.sh
if [ -n "$RATTS_SERVER" ]; then
    CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/ratts"
    mkdir -p "$CONFIG_DIR"
    CONFIG_FILE="$CONFIG_DIR/config.toml"
    if [ ! -f "$CONFIG_FILE" ]; then
        printf 'server_url = "%s"\n' "$RATTS_SERVER" > "$CONFIG_FILE"
        echo "Created $CONFIG_FILE with server_url = $RATTS_SERVER"
    else
        # Update existing server_url line, or append if absent
        if grep -q '^server_url' "$CONFIG_FILE"; then
            sed -i "s|^server_url.*|server_url = \"$RATTS_SERVER\"|" "$CONFIG_FILE"
        else
            printf '\nserver_url = "%s"\n' "$RATTS_SERVER" >> "$CONFIG_FILE"
        fi
        echo "Updated server_url in $CONFIG_FILE to $RATTS_SERVER"
    fi
fi

cd "$SCRIPT_DIR"
cargo build --release -p ratts-cli 2>&1

exec ./target/release/ratts-cli
