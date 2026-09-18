GO ?= go
BIN ?= stables
TARGET ?=
PYTHON ?= python3
PI_AGENT ?= $(HOME)/.pi/agent
# bash needed for `read -s` (no-echo password prompt) in build-secrets
SHELL := /bin/bash

.PHONY: build build-secrets build-minimal test clean install remote-install remote-clean-install ollama-install ollama-remote-install assets assets-common dind-start dind-stop docker-smoke test-docker e2e tts-tui test-tts check-tts-memory

# Default build: bake skills/extensions/packages (NO secrets).
build: assets
	$(GO) build -trimpath -buildvcs=false -tags embedassets -o $(BIN) ./cmd/stables

# Full build: embed models.json + secrets.env, sealed with an installer
# password (AES-256-GCM). Install requires the same password to unlock them;
# a wrong/missing password yields the base install without secrets.
build-secrets:
	@if [ -n "$$STABLES_PASSWORD" ]; then pw="$$STABLES_PASSWORD"; else \
		read -s -p "Installer password: " pw; echo; \
		[ -n "$$pw" ] || { echo "build-secrets requires a password" >&2; exit 1; }; \
	fi; \
	STABLES_PASSWORD="$$pw" $(MAKE) assets; \
	STABLES_PASSWORD="$$pw" $(GO) build -trimpath -buildvcs=false -tags embedassets,embedsecrets -o $(BIN) ./cmd/stables

# Minimal build: nothing baked (stubs). Same binary, empty ~/.pi/agent dirs.
build-minimal: assets-common
	$(GO) build -trimpath -buildvcs=false -o $(BIN) ./cmd/stables

# Shared: rebuild stab, copy the stable text assets + stab binary.
assets-common:
	$(MAKE) -C stab build
	mkdir -p internal/assets/files/stab
	cp stab/stab internal/assets/files/stab/stab
	rm -rf internal/assets/files/ollama internal/assets/files/webui
	cp -a assets/ollama assets/webui internal/assets/files/
	# The voice backend has no published image, so its source and the TUI binary
	# travel in the stables binary and are written out by `stables install tts`.
	command -v cargo >/dev/null || { echo "cargo is required to bake the voice TUI" >&2; exit 1; }
	# The client is baked into the binary, and cargo records the paths of every
	# crate it compiled: without remapping, the builder's home directory travels in
	# a public release.
	cd tts/tui && RUSTFLAGS="--remap-path-prefix=$(HOME)=/build $(RUSTFLAGS)" cargo build --release
	rm -rf internal/assets/files/tts
	mkdir -p internal/assets/files/tts
	cp -a tts/backend internal/assets/files/tts/backend
	# Bytecode and tests are build leftovers, not something to ship.
	find internal/assets/files/tts/backend -name __pycache__ -type d -exec rm -rf {} +
	rm -rf internal/assets/files/tts/backend/tests
	cp tts/tui/target/release/ratts-cli internal/assets/files/tts/ratts-cli
	cp -a assets/tts/. internal/assets/files/tts/
	# go:embed fails to compile if a pattern matches nothing, so the generated
	# directories must never be empty. Their .gitkeep files travel with the
	# copy above; stab's is created here because its directory is not replaced.
	touch internal/assets/files/stab/.gitkeep

# Full asset bake: shared assets + the build machine's Pi agent resources.
assets: assets-common
	# Clean first: embedgen writes only a fixed set of names, so anything left
	# by an earlier build (e.g. piagent/models.json, piagent/secrets.env) must
	# not survive to be embedded.
	rm -rf internal/assets/files/piagent internal/assets/files/home
	$(GO) run ./cmd/embedgen \
		-skills $(PI_AGENT)/skills \
		-extensions $(PI_AGENT)/extensions \
		-npm $(PI_AGENT)/npm \
		-git $(PI_AGENT)/git \
		-settings $(PI_AGENT)/settings.json \
		-models $(HOME)/.stables/models.json \
		-secrets $(HOME)/.stables/secrets.env \
		-out internal/assets/files/piagent

test: assets-common
	$(GO) test ./...
	$(MAKE) -C stab test

install: build
	./$(BIN) install stab

ollama-install: build
	./$(BIN) install ollama

remote-install: build
	@[ -n "$(TARGET)" ] || (echo "TARGET=user@host required" >&2; exit 2)
	./$(BIN) remote install stab $(TARGET)

remote-clean-install: build
	@[ -n "$(TARGET)" ] || (echo "TARGET=user@host required" >&2; exit 2)
	./$(BIN) remote clean-install stab $(TARGET)

ollama-remote-install: build
	@[ -n "$(TARGET)" ] || (echo "TARGET=user@host required" >&2; exit 2)
	./$(BIN) remote install ollama $(TARGET)

# The voice stack (voice/) is deliberately not embedded in the stables binary:
# it is source that `stables install tts` builds on the target machine. These
# targets build and test it here.
tts-tui:
	cd tts/tui && cargo build --release

# Backend tests need the runtime deps plus requirements-dev.txt installed.
test-tts:
	cd tts/tui && cargo test
	cd voice && $(PYTHON) -m pytest backend/tests -q

# Long-running check that the backend does not grow: starts the real server,
# serves a run of requests and samples its RSS. Needs tts/models populated
# (kokoro-v1.0.onnx, voices-v1.0.bin, piper/).
check-tts-memory:
	$(PYTHON) agent_tools/check_tts_memory.py

dind-start:
	./scripts/dind-dev.sh start

dind-stop:
	./scripts/dind-dev.sh stop

docker-smoke:
	DOCKER_HOST=$${DOCKER_HOST:-unix:///tmp/stables-docker.sock} ./scripts/docker-smoke.sh

test-docker: docker-smoke

e2e:
	bash tests/e2e/run-e2e.sh

clean:
	rm -f $(BIN) internal/assets/files/stab/stab
	$(MAKE) -C stab clean
