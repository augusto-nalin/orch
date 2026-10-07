BIN := plugin/bin/orchctl
PREFIX ?= $(HOME)/.local/bin

.PHONY: build test install
build:
	go build -o $(BIN) ./cmd/orchctl

test:
	go vet ./...
	go test ./...

# orchctl: the broker CLI that skills and hooks call. orch: the user's start
# command (iTerm2 screen with the plugin loaded).
install: build
	mkdir -p $(PREFIX)
	ln -sf $(CURDIR)/$(BIN) $(PREFIX)/orchctl
	ln -sf $(CURDIR)/plugin/scripts/orch-screen.sh $(PREFIX)/orch
