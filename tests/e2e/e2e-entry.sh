#!/usr/bin/env bash
# Entry script for the E2E SSH container. As tester:
#  - if E2E_PUBKEY is set and mode=server: install it, start sshd, sleep forever
#  - if mode=client: write ssh client config (accept-new) and exec the command
set -euo pipefail

MODE="${E2E_MODE:-server}"

if [ "$MODE" = "client" ]; then
    mkdir -p ~/.ssh
    cat > ~/.ssh/config <<'EOF'
Host *
    StrictHostKeyChecking accept-new
    UserKnownHostsFile /dev/null
    LogLevel ERROR
EOF
    chmod 600 ~/.ssh/config
    # Commands are driven via `docker exec` from the orchestrator.
    exec sleep infinity
fi

# Server mode.
if [ -n "${E2E_PUBKEY:-}" ] && [ ! -f ~/.ssh/authorized_keys ]; then
    printf '%s\n' "$E2E_PUBKEY" >> ~/.ssh/authorized_keys
    chmod 600 ~/.ssh/authorized_keys
fi
sudo mkdir -p /run/sshd
sudo chmod 755 /run/sshd
sudo /usr/sbin/sshd
exec sleep infinity
