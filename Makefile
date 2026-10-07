BIN := plugin/bin/orch
# The skills call `orch` on PATH.
PREFIX ?= $(HOME)/.local/bin

.PHONY: build test install
build:
	go build -o $(BIN) ./cmd/orch

test:
	go vet ./...
	go test ./...

# Symlinks $(PREFIX)/orch to the build output.
install: build
	mkdir -p $(PREFIX)
	ln -sf $(CURDIR)/$(BIN) $(PREFIX)/orch
