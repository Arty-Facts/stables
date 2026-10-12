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

# Install Node.js 24.x LTS (required by the pi installer). The setup script is
# downloaded first and run from a file: `curl ... | bash` exits 0 when the
# download fails (bash reads an empty script), leaving a broken image behind a
# green build.
RUN curl -fsSL https://deb.nodesource.com/setup_24.x -o /tmp/nodesource.sh \
  && bash /tmp/nodesource.sh \
  && rm -f /tmp/nodesource.sh \
  && apt-get install -y --no-install-recommends nodejs \
  && rm -rf /var/lib/apt/lists/* \
  && npm install -g npm@latest

# Pi's managed install lives outside $HOME on purpose: the controller
# bind-mounts the host's ~/.pi/agent over /home/coder/.pi/agent at runtime, so
# anything baked there is hidden and the harness would be missing in every
# container. PI_CODING_AGENT_DIR selects the managed install root for the
# installer only — it is deliberately NOT set as a runtime ENV, so Pi keeps its
# config (models.json, auth.json, skills, extensions) in the mounted
# /home/coder/.pi/agent. Downloaded first and run from a file: `curl ... | sh`
# exits 0 on a failed download, producing an image with no Pi at all.
ENV PI_INSTALL_ROOT=/opt/pi-agent
RUN curl -fsSL https://pi.dev/install.sh -o /tmp/pi-install.sh \
  && PI_CODING_AGENT_DIR="$PI_INSTALL_ROOT" sh /tmp/pi-install.sh \
  && rm -f /tmp/pi-install.sh \
  && chown -R coder:coder "$PI_INSTALL_ROOT"

# Pi must be found by the non-login shells tmux windows use (ENV) and by login
# shells, which reset PATH from /etc/profile — hence the profile.d entry.
ENV PATH="${PI_INSTALL_ROOT}/bin:${PATH}"
RUN printf 'export PATH="%s/bin:$PATH"\n' "$PI_INSTALL_ROOT" > /etc/profile.d/pi-agent.sh \
  && chmod 0644 /etc/profile.d/pi-agent.sh

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
