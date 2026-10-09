DEV := plugin/bin/orchctl-dev
REL := plugin/bin/orchctl
# Signing identity and notary profile for `make release`; override on the command line.
SIGN_ID ?= $(shell security find-identity -p codesigning -v 2>/dev/null | sed -n 's/.*"\(Developer ID Application:[^"]*\)".*/\1/p' | head -1)
NOTARY_PROFILE ?= orch-notary

.PHONY: build test install release

# Dev build; the committed release binary hands over to it (see devExec).
build:
	go build -o $(DEV) ./cmd/orchctl
	@[ -e $(REL) ] || cp $(DEV) $(REL)

test:
	go vet ./...
	go test ./...

# Same as `orch setup`: build, link orch + orchctl into ~/.local/bin, check deps.
install:
	bash plugin/scripts/orch-setup.sh

# Signed, notarized universal binary at $(REL), committed with the plugin version bump.
# Needs a "Developer ID Application" certificate in the keychain and, once:
#   xcrun notarytool store-credentials orch-notary
release: test
	@[ -n "$(VERSION)" ] || { echo "usage: make release VERSION=x.y.z"; exit 1; }
	@[ -n "$(SIGN_ID)" ] || { echo "no Developer ID Application certificate in the keychain (or set SIGN_ID=…)"; exit 1; }
	$(eval T := $(shell mktemp -d))
	for a in arm64 amd64; do \
	  CGO_ENABLED=0 GOOS=darwin GOARCH=$$a go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(T)/orchctl-$$a ./cmd/orchctl || exit 1; \
	done
	lipo -create -output $(REL) $(T)/orchctl-arm64 $(T)/orchctl-amd64
	codesign --force --options runtime --timestamp -s "$(SIGN_ID)" $(REL)
	ditto -c -k $(REL) $(T)/orchctl.zip
	xcrun notarytool submit $(T)/orchctl.zip --keychain-profile "$(NOTARY_PROFILE)" --wait
	codesign --verify --strict $(REL)
	jq --arg v "$(VERSION)" '.version = $$v' plugin/.claude-plugin/plugin.json > $(T)/plugin.json && mv $(T)/plugin.json plugin/.claude-plugin/plugin.json
	rm -rf $(T)
	@echo "release $(VERSION) ready: commit $(REL) and plugin/.claude-plugin/plugin.json"
