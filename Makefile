GO        ?= go
PKG       := ./cmd/vaultcrab
# Computed once (:=) so the version cannot change halfway through the build.
ifeq ($(origin VERSION),undefined)
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
endif
LDFLAGS   := -s -w -X main.version=$(VERSION)
export CGO_ENABLED=0

PLATFORMS := linux/amd64 windows/amd64 darwin/amd64 darwin/arm64

.PHONY: build build-all release test vet clean

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

# Release artifacts: dist/vaultcrab-<os>-<arch> + dist/SHA256SUMS (not committed;
# uploaded to GitHub Releases).
release:
	@case "$(VERSION)" in *dirty*) echo "working tree is dirty: commit first"; exit 1;; esac
	$(MAKE) build-all VERSION=$(VERSION)
	cd dist && sha256sum vaultcrab-*-* > SHA256SUMS && cat SHA256SUMS

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

clean:
	rm -rf dist
