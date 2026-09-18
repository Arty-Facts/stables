# stab runtime image: a tmux-first, code-server-based container that runs
# the Pi harness and exposes the web-based VS Code UI.
FROM codercom/code-server

USER root
RUN apt-get update \
  && apt-get install -y --no-install-recommends \
    bash curl ca-certificates git openssh-client ripgrep fd-find gosu tmux gettext-base sudo \
  && (apt-get install -y --no-install-recommends docker-cli docker-compose \
    || apt-get install -y --no-install-recommends docker.io docker-compose) \
  && ln -sf /usr/bin/fdfind /usr/local/bin/fd \
  && rm -rf /var/lib/apt/lists/* \
  && echo "coder ALL=(ALL) NOPASSWD:ALL" > /etc/sudoers.d/coder \
  && chmod 0440 /etc/sudoers.d/coder

# Install Node.js 24.x LTS (required by the pi installer).
RUN curl -fsSL https://deb.nodesource.com/setup_24.x | bash - \
  && apt-get install -y --no-install-recommends nodejs \
  && rm -rf /var/lib/apt/lists/* \
  && npm install -g npm@latest

# Install the pi coding agent globally.
RUN curl -fsSL https://pi.dev/install.sh | sh

# Pi requires a resolved API key value for Ollama-compatible local providers.
ENV OLLAMA=ollama

# Pi's agent dir must exist and be owned by coder in the image: the controller
# bind-mounts skills/extensions/settings.json beneath it, and Docker would
# otherwise create the parent as root — leaving Pi (which runs as coder) unable
# to create its settings.json.lock lockdir and auth.json.
RUN mkdir -p /home/coder/.pi/agent \
  && chown -R coder:coder /home/coder/.pi

COPY entrypoint.sh /usr/local/bin/entrypoint.sh
# Windows checkouts may copy this file with CRLF endings.
RUN sed -i 's/\r$//' /usr/local/bin/entrypoint.sh \
  && chmod +x /usr/local/bin/entrypoint.sh

RUN mkdir -p /workspace /stab-state /var/log/stab
WORKDIR /workspace

# Stay as root — entrypoint will drop to coder after aligning uid/gid.
ENTRYPOINT ["dumb-init", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["--bind-addr", "0.0.0.0:8080", "/workspace"]
