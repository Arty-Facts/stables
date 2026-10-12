GO ?= go
GOTOOLCHAIN ?= local
BIN ?= stab
# Static by default: this binary is baked into `stables` and scp'd to remote
# hosts by `stables remote install`, where the target's glibc is unknown. The
# SQLite driver is modernc.org/sqlite (pure Go), so cgo buys nothing.
CGO_ENABLED ?= 0
export CGO_ENABLED

# The top-level build injects these (base64) so the deployable binary carries
# its models.json and secrets.env without the repo ever referencing them.
MODELS_B64 ?=
SECRETS_B64 ?=

export GOTOOLCHAIN := $(GOTOOLCHAIN)

.PHONY: build test vet clean install

build:
	$(GO) build -trimpath -buildvcs=false -ldflags "-X github.com/stables/stab/internal/embedded.modelsJSONB64=$(MODELS_B64) -X github.com/stables/stab/internal/embedded.secretsB64=$(SECRETS_B64)" -o $(BIN) ./cmd/stab

install: build
	./$(BIN) install

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -f $(BIN)
