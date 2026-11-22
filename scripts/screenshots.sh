#!/usr/bin/env bash
# Renders the README screenshots (docs/screenshot-*.svg and .png) and the
# theme gallery (docs/themes/*.png) from the scenes of
# internal/tui/screenshots_test.go: a demo vault with test data only. Needs
# rsvg-convert (librsvg) for the PNG files.
#
#   scripts/screenshots.sh
set -euo pipefail
cd "$(dirname "$0")/.."
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
version=$(sed -n 's/^var version = "\(.*\)"$/\1/p' cmd/vaultcrab/main.go)
VAULTCRAB_SCREENSHOT_VERSION=$version VAULTCRAB_SCREENSHOTS=$tmp \
	go test -tags screenshots -run TestScreenshots -count=1 ./internal/tui >/dev/null
go build -o "$tmp/ansi2svg" ./tools/ansi2svg
declare -A titles=(
	[hosts]="Vaultcrab — hosts"
	[jump]="Vaultcrab — through a jump host"
	[tunnels]="Vaultcrab — tunnels"
	[debug]="Vaultcrab — connection debug"
	[panes]="Vaultcrab — split panes"
	[settings]="Vaultcrab — settings"
	[programs]="Vaultcrab — external programs"
)
for name in "${!titles[@]}"; do
	"$tmp/ansi2svg" -w 150 -h 40 -title "${titles[$name]}" <"$tmp/$name.ans" >"docs/screenshot-$name.svg"
	rsvg-convert -z 1.5 "docs/screenshot-$name.svg" -o "docs/screenshot-$name.png"
	echo "docs/screenshot-$name.png"
done
mkdir -p docs/themes
for f in "$tmp"/theme-*.ans; do
	theme=$(basename "$f" .ans)
	theme=${theme#theme-}
	light=()
	case $theme in *-light | *-latte) light=(-light) ;; esac
	"$tmp/ansi2svg" "${light[@]}" -w 150 -h 40 -title "Vaultcrab — theme $theme" <"$f" >"$tmp/$theme.svg"
	rsvg-convert "$tmp/$theme.svg" -o "docs/themes/$theme.png"
	echo "docs/themes/$theme.png"
done
