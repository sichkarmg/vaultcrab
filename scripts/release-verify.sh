#!/usr/bin/env bash
# Verifies a (draft) GitHub release before it is published:
#   1. downloads its assets with gh (works for drafts);
#   2. checks checksums.txt (SHA-256) and that every expected archive is there
#      and contains the binary, README.md and LICENSE;
#   3. checks that the Linux binaries are statically linked;
#   4. on the test VMs (Linux Mint 20, Windows 11): `vaultcrab --version` must
#      print the release version, and a short TUI smoke start must succeed.
#
#   scripts/release-verify.sh v0.2.0
#
# Needs: gh authenticated for the repository (e.g. GH_TOKEN in the environment),
# sha256sum, tar, unzip, file; ssh access to the VMs (see scripts/lib/vm.sh).
# VAULTCRAB_REPO overrides the repository (default sichkarmg/vaultcrab).
# VAULTCRAB_RELEASE_DIR=DIR checks local archives + checksums.txt instead of
# downloading (e.g. from `goreleaser release --snapshot`).
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib/vm.sh

[ $# -eq 1 ] || { echo "usage: $0 vX.Y.Z" >&2; exit 2; }
tag=v${1#v}
ver=${tag#v}
repo=${VAULTCRAB_REPO:-sichkarmg/vaultcrab}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
fails=0
pass() { echo "ok    $*"; }
fail() { echo "FAIL  $*"; fails=$((fails + 1)); }

mkdir -p "$work/dl"
if [ -n "${VAULTCRAB_RELEASE_DIR:-}" ]; then
	cp "$VAULTCRAB_RELEASE_DIR"/checksums.txt "$VAULTCRAB_RELEASE_DIR"/vaultcrab_"$ver"_* "$work/dl/" || exit 1
else
	gh release view "$tag" --repo "$repo" --json isDraft,isPrerelease,tagName \
		--template '{{.tagName}}: draft={{.isDraft}} prerelease={{.isPrerelease}}{{"\n"}}' ||
		{ echo "release $tag not found in $repo" >&2; exit 1; }
	gh release download "$tag" --repo "$repo" --dir "$work/dl" || { echo "download failed" >&2; exit 1; }
fi

(cd "$work/dl" && sha256sum --quiet -c checksums.txt) && pass "checksums.txt" || fail "checksums.txt"
for f in "$work"/dl/*; do
	n=$(basename "$f")
	[ "$n" = checksums.txt ] || grep -q "  $n\$" "$work/dl/checksums.txt" || fail "$n is not in checksums.txt"
done

for target in linux_amd64 linux_arm64 darwin_amd64 darwin_arm64 windows_amd64; do
	bin=vaultcrab ext=tar.gz
	[ "$target" = windows_amd64 ] && bin=vaultcrab.exe ext=zip
	a=$work/dl/vaultcrab_${ver}_$target.$ext
	[ -f "$a" ] || { fail "missing $(basename "$a")"; continue; }
	d=$work/x/$target
	mkdir -p "$d"
	if [ $ext = zip ]; then unzip -q "$a" -d "$d"; else tar -xzf "$a" -C "$d"; fi
	missing=
	for f in "$bin" README.md LICENSE; do [ -f "$d/$f" ] || missing="$missing $f"; done
	[ -z "$missing" ] && pass "$(basename "$a")" || fail "$(basename "$a") lacks$missing"
	case $target in linux_*)
		file "$d/$bin" | grep -q 'statically linked' && pass "$target is static" || fail "$target is not static" ;;
	esac
done

declare -A result
for t in linux windows; do
	echo "### $t"
	if [ $t = linux ]; then host=$VM_LINUX src=$work/x/linux_amd64/vaultcrab; else host=$VM_WIN src=$work/x/windows_amd64/vaultcrab.exe; fi
	[ -f "$src" ] || { result[$t]="FAIL (no binary)"; continue; }
	stage=$work/vm-$t
	mkdir -p "$stage/smoke" && cp "$src" "$stage/"
	vm_upload "$host" "$stage" || { result[$t]="FAIL (upload to $host)"; continue; }
	vm_version "$host" "$ver" || { result[$t]="FAIL (--version)"; continue; }
	vm_smoke "$host" || { result[$t]="FAIL (smoke)"; continue; }
	result[$t]=PASS
done

echo
echo "=== release-verify summary ($tag)"
for t in linux windows; do
	printf '%-8s %s\n' "$t" "${result[$t]}"
	[ "${result[$t]}" = PASS ] || fails=$((fails + 1))
done
[ $fails -eq 0 ] && echo "all checks passed" || echo "$fails check(s) failed"
[ $fails -eq 0 ]
