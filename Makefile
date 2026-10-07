BIN := plugin/bin/orch

.PHONY: build test vet install
build:
	go build -o $(BIN) ./cmd/orch

test:
	go vet ./...
	go test ./...

# Symlinks ~/bin/orch to the build output.
install: build
	mkdir -p $(HOME)/bin
	ln -sf $(CURDIR)/$(BIN) $(HOME)/bin/orch
