#!/usr/bin/env bash
# Checks the tree before a release tag is created (`make release-check`):
# the version in cmd/vaultcrab/main.go, a dated CHANGELOG.md section for it,
# no existing tag, a clean tree, gofmt, go vet, go test and all builds.
#
#   scripts/release-check.sh            # version from cmd/vaultcrab/main.go
#   scripts/release-check.sh 0.2.0      # also require this version
set -euo pipefail
cd "$(dirname "$0")/.."

fail() { echo "release-check: $*" >&2; exit 1; }
ok() { echo "ok   $*"; }

code=$(sed -n 's/^var version = "\(.*\)"$/\1/p' cmd/vaultcrab/main.go)
[ -n "$code" ] || fail "no 'var version = \"...\"' in cmd/vaultcrab/main.go"
want=${1:-$code}
want=${want#v}
[ "$code" = "$want" ] || fail "cmd/vaultcrab/main.go has $code, expected $want"
[[ $want =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]] || fail "$want is not SemVer"
ok "version $want in code"

grep -Eq "^## \[$want\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" CHANGELOG.md ||
	fail "CHANGELOG.md has no '## [$want] - YYYY-MM-DD' section"
ok "CHANGELOG.md section [$want]"

if git rev-parse -q --verify "refs/tags/v$want" >/dev/null; then
	fail "tag v$want already exists"
fi
ok "tag v$want is free"

[ -z "$(git status --porcelain --untracked-files=no)" ] || fail "working tree has uncommitted changes"
ok "clean tree"

make --no-print-directory fmt-check && ok gofmt
go vet ./... && go vet -tags integration ./... && ok "go vet"
go test -count=1 ./... >/dev/null && ok "go test"
make --no-print-directory build-all VERSION="$want" >/dev/null && ok "build-all"
got=$(go run ./cmd/vaultcrab -version 2>/dev/null || true)
[ "$got" = "vaultcrab $want" ] || fail "go run -version prints '$got'"
ok "-version prints '$got'"
if command -v goreleaser >/dev/null; then
	goreleaser check >/dev/null 2>&1 && ok "goreleaser check" || fail "goreleaser check"
else
	echo "skip goreleaser check (goreleaser not installed; the release workflow runs it)"
fi
echo "release-check: v$want is ready to tag"
