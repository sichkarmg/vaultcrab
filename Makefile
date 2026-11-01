GO        ?= go
PKG       := ./cmd/vaultcrab
# Computed once (:=) so the version cannot change halfway through the build.
# git describe gives v1.2.3[-N-gHASH][-dirty]; the binary reports it without "v".
ifeq ($(origin VERSION),undefined)
VERSION   := $(patsubst v%,%,$(shell git describe --tags --always --dirty 2>/dev/null || echo dev))
endif
LDFLAGS   := -s -w -X main.version=$(VERSION)
export CGO_ENABLED=0

# Keep in sync with .goreleaser.yaml.
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 darwin/amd64 darwin/arm64

.PHONY: build build-all release release-check test vet fmt-check e2e clean

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o dist/vaultcrab $(PKG)

build-all:
	@mkdir -p dist && rm -f dist/vaultcrab-*-* dist/SHA256SUMS
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
		echo "-> $$os/$$arch"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' \
			-o dist/vaultcrab-$$os-$$arch$$ext $(PKG) || exit 1; \
	done

# Local artifacts: dist/vaultcrab-<os>-<arch> + dist/SHA256SUMS. Published
# releases are built by .github/workflows/release.yml (see RELEASING.md).
release:
	@case "$(VERSION)" in *dirty*) echo "working tree is dirty: commit first"; exit 1;; esac
	$(MAKE) build-all VERSION=$(VERSION)
	cd dist && sha256sum vaultcrab-*-* > SHA256SUMS && cat SHA256SUMS

# Checks before tagging a release (version in code, CHANGELOG, tests, builds).
release-check:
	scripts/release-check.sh

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...
	$(GO) vet -tags integration ./...

fmt-check:
	@out=$$(gofmt -l $$(git ls-files '*.go')); \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

# Integration tests on the test VMs (see scripts/e2e.sh for the variables).
e2e:
	scripts/e2e.sh

clean:
	rm -rf dist
