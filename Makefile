DEV := plugin/bin/orchctl-dev
DIST := dist
REPO := augusto-nalin/orch
# Signing identity and notary profile for `make release`; override on the command line.
SIGN_ID ?= $(shell security find-identity -p codesigning -v 2>/dev/null | sed -n 's/.*"\(Developer ID Application:[^"]*\)".*/\1/p' | head -1)
NOTARY_PROFILE ?= orch-notary

.PHONY: build test install release publish

# Dev build; the plugin/bin/orchctl shim hands over to it.
build:
	go build -o $(DEV) ./cmd/orchctl

test:
	go vet ./...
	go test ./...

# Same as `orch setup`: build, link orch + orchctl into ~/.local/bin, check deps.
install:
	bash plugin/scripts/orch-setup.sh

# Signed, notarized universal binary in $(DIST)/orchctl (the shim downloads it raw)
# and $(DIST)/orchctl.zip (for manual downloads; notarytool takes only a zip). Its
# sha256 goes to plugin/bin/orchctl.sha256, which the shim checks after downloading. Commit both files changed, push, then `make publish`.
# Needs a "Developer ID Application" certificate in the keychain and, once:
#   xcrun notarytool store-credentials orch-notary
release: test
	@[ -n "$(VERSION)" ] || { echo "usage: make release VERSION=x.y.z"; exit 1; }
	@[ -n "$(SIGN_ID)" ] || { echo "no Developer ID Application certificate in the keychain (or set SIGN_ID=…)"; exit 1; }
	$(eval T := $(shell mktemp -d))
	for a in arm64 amd64; do \
	  CGO_ENABLED=0 GOOS=darwin GOARCH=$$a go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(T)/orchctl-$$a ./cmd/orchctl || exit 1; \
	done
	lipo -create -output $(T)/orchctl $(T)/orchctl-arm64 $(T)/orchctl-amd64
	codesign --force --options runtime --timestamp -s "$(SIGN_ID)" $(T)/orchctl
	rm -rf $(DIST) && mkdir -p $(DIST)
	ditto -c -k $(T)/orchctl $(DIST)/orchctl.zip
	xcrun notarytool submit $(DIST)/orchctl.zip --keychain-profile "$(NOTARY_PROFILE)" --wait
	codesign --verify --strict $(T)/orchctl
	cp $(T)/orchctl $(DIST)/orchctl
	shasum -a 256 $(T)/orchctl | cut -d' ' -f1 > plugin/bin/orchctl.sha256
	echo $(VERSION) > $(DIST)/latest-version.txt
	jq --arg v "$(VERSION)" '.version = $$v' plugin/.claude-plugin/plugin.json > $(T)/plugin.json && mv $(T)/plugin.json plugin/.claude-plugin/plugin.json
	rm -rf $(T)
	@echo "release $(VERSION) ready: commit plugin/bin/orchctl.sha256 and plugin/.claude-plugin/plugin.json, push, then: make publish"

# GitHub release v<plugin version> with the binary, the zip and latest-version.txt
# (the update check reads it from the latest release). Asset download counts =
# installs and days orch was started.
publish:
	$(eval V := $(shell jq -r .version plugin/.claude-plugin/plugin.json))
	@[ "$$(cat $(DIST)/latest-version.txt 2>/dev/null)" = "$(V)" ] || { echo "$(DIST) is not release $(V) — run: make release VERSION=$(V)"; exit 1; }
	gh release create v$(V) -R $(REPO) --target $$(git rev-parse HEAD) --title "orch $(V)" --notes "" $(DIST)/orchctl $(DIST)/orchctl.zip $(DIST)/latest-version.txt
