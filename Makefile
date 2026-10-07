BIN := plugin/bin/orchctl

.PHONY: build test install
build:
	go build -o $(BIN) ./cmd/orchctl

test:
	go vet ./...
	go test ./...

# Same as `orch setup`: build, link orch + orchctl into ~/.local/bin, check deps.
install:
	bash plugin/scripts/orch-setup.sh
