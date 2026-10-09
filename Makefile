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

# Release binaries in $(DIST), one per platform, named orchctl-<os>-<arch>[.exe]:
# darwin-universal (signed, notarized; also as a .zip for manual downloads, since
# notarytool takes only a zip), linux-amd64/arm64, windows-amd64/arm64.exe. The shim
# downloads the raw one for its platform and checks it against
# plugin/bin/orchctl.sha256 ("<sha256>  <asset>" lines). Commit both files changed,
# push, then `make publish`.
# Needs a "Developer ID Application" certificate in the keychain and, once:
#   xcrun notarytool store-credentials orch-notary
release: test
	@[ -n "$(VERSION)" ] || { echo "usage: make release VERSION=x.y.z"; exit 1; }
	@[ -n "$(SIGN_ID)" ] || { echo "no Developer ID Application certificate in the keychain (or set SIGN_ID=…)"; exit 1; }
	$(eval T := $(shell mktemp -d))
	$(eval LDFLAGS := -s -w -X main.version=$(VERSION))
	rm -rf $(DIST) && mkdir -p $(DIST)
	for a in arm64 amd64; do \
	  CGO_ENABLED=0 GOOS=darwin GOARCH=$$a go build -trimpath -ldflags "$(LDFLAGS)" -o $(T)/orchctl-darwin-$$a ./cmd/orchctl || exit 1; \
	done
	for p in linux/amd64 linux/arm64 windows/amd64 windows/arm64; do \
	  o=$${p%/*}; a=$${p#*/}; x=$$([ $$o = windows ] && echo .exe); \
	  CGO_ENABLED=0 GOOS=$$o GOARCH=$$a go build -trimpath -ldflags "$(LDFLAGS)" -o $(DIST)/orchctl-$$o-$$a$$x ./cmd/orchctl || exit 1; \
	done
	lipo -create -output $(T)/orchctl-darwin-universal $(T)/orchctl-darwin-arm64 $(T)/orchctl-darwin-amd64
	codesign --force --options runtime --timestamp -s "$(SIGN_ID)" $(T)/orchctl-darwin-universal
	ditto -c -k $(T)/orchctl-darwin-universal $(DIST)/orchctl-darwin-universal.zip
	xcrun notarytool submit $(DIST)/orchctl-darwin-universal.zip --keychain-profile "$(NOTARY_PROFILE)" --wait
	codesign --verify --strict $(T)/orchctl-darwin-universal
	cp $(T)/orchctl-darwin-universal $(DIST)/
	cd $(DIST) && shasum -a 256 $$(ls orchctl-* | grep -v '\.zip$$') > ../plugin/bin/orchctl.sha256
	echo $(VERSION) > $(DIST)/latest-version.txt
	jq --arg v "$(VERSION)" '.version = $$v' plugin/.claude-plugin/plugin.json > $(T)/plugin.json && mv $(T)/plugin.json plugin/.claude-plugin/plugin.json
	rm -rf $(T)
	@echo "release $(VERSION) ready: commit plugin/bin/orchctl.sha256 and plugin/.claude-plugin/plugin.json, push, then: make publish"

# GitHub release v<plugin version> with the binaries, the zip and latest-version.txt
# (the update check reads it from the latest release). Asset download counts =
# installs and days orch was started.
publish:
	$(eval V := $(shell jq -r .version plugin/.claude-plugin/plugin.json))
	@[ "$$(cat $(DIST)/latest-version.txt 2>/dev/null)" = "$(V)" ] || { echo "$(DIST) is not release $(V) — run: make release VERSION=$(V)"; exit 1; }
	gh release create v$(V) -R $(REPO) --target $$(git rev-parse HEAD) --title "orch $(V)" --notes "" $(DIST)/orchctl-* $(DIST)/latest-version.txt
