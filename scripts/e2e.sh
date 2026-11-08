#!/usr/bin/env bash
# End-to-end check on the test VMs (Linux Mint 20 and Windows 11):
#   1. builds vaultcrab and the integration test binaries (build tag
#      "integration") for linux/amd64 and windows/amd64;
#   2. copies them to the VMs over ssh (~/vaultcrab-e2e, replaced every run);
#   3. on each VM: `vaultcrab --version`, a short TUI smoke start, then the
#      integration tests against `bw serve` of the TEST vault account;
#   4. prints a summary; the exit code is non-zero if anything failed.
#
#   scripts/e2e.sh            # both VMs
#   scripts/e2e.sh linux      # or: windows
#
# Variables: see scripts/lib/vm.sh for VAULTCRAB_VM_*; in addition
#   VAULTCRAB_E2E_BW_PASSWORD_CMD  command printing the master password of the
#                                  TEST vault account (required for the
#                                  integration tests; without it they are skipped)
#   VAULTCRAB_E2E_LINUX_ENV        KEY=VALUE pairs (space-separated) for bw on the
#   VAULTCRAB_E2E_WIN_ENV          VMs, e.g. BITWARDENCLI_APPDATA_DIR=...,
#                                  NODE_EXTRA_CA_CERTS=..., VAULTCRAB_IT_BW_PATH=...
#   VAULTCRAB_E2E_JUMP             optional: a jump host chain for the tests of the
#   VAULTCRAB_E2E_JUMP_TARGET      connections through a jump host (user@host[:port]
#   VAULTCRAB_E2E_JUMP_KEY         each) and a local TEST private key accepted by
#                                  both; the key is copied to the VMs
# bw must be logged in to the test account on the VMs (scripts/seed-vault.sh
# fills it). The password goes to the VMs over ssh stdin, never through argv.
set -uo pipefail
cd "$(dirname "$0")/.."
. scripts/lib/vm.sh

targets=${1:-all}
case $targets in all) targets="linux windows" ;; linux | windows) ;; *)
	echo "usage: $0 [linux|windows]" >&2; exit 2 ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
export CGO_ENABLED=0
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)
version=${version#v}
pkgs=$(grep -rl --include='*_test.go' '^//go:build integration' internal cmd 2>/dev/null | xargs -r -n1 dirname | sort -u)

build() { # build GOOS DIR EXT
	local os=$1 dir=$2 ext=$3 p
	mkdir -p "$dir/smoke"
	GOOS=$os GOARCH=amd64 go build -trimpath -ldflags "-s -w -X main.version=$version" \
		-o "$dir/vaultcrab$ext" ./cmd/vaultcrab || return 1
	for p in $pkgs; do
		GOOS=$os GOARCH=amd64 go test -c -tags integration -o "$dir/$(basename "$p").test$ext" "./$p" || return 1
	done
}

password() { [ -n "${VAULTCRAB_E2E_BW_PASSWORD_CMD:-}" ] && bash -c "$VAULTCRAB_E2E_BW_PASSWORD_CMD"; }

# Remote runners: read the password from stdin, unlock and sync bw (BW_SESSION
# only in the runner's environment), run the integration tests, lock bw again.
cat >"$stage/run.sh" <<'RUN'
#!/usr/bin/env bash
cd "$(dirname "$0")" || exit 1
export PATH="$HOME/.local/bin:$PATH"
[ -f env ] && { set -a; . ./env; set +a; }
IFS= read -r pw || pw=
[ -n "$pw" ] || { echo "no password: integration tests skipped"; exit 3; }
BW_SESSION=$(BW_PASSWORD="$pw" "${VAULTCRAB_IT_BW_PATH:-bw}" unlock --passwordenv BW_PASSWORD --raw 2>/dev/null)
unset pw
[ -n "$BW_SESSION" ] || { echo "bw unlock failed"; exit 1; }
export BW_SESSION
"${VAULTCRAB_IT_BW_PATH:-bw}" sync >/dev/null 2>&1 || echo "bw sync failed: testing the local copy"
rc=0
for t in ./*.test; do
	echo "== $t"
	"$t" -test.run '^TestIntegration' -test.v -test.count=1 -test.timeout 10m || rc=1
done
"${VAULTCRAB_IT_BW_PATH:-bw}" lock >/dev/null 2>&1
exit $rc
RUN
cat >"$stage/run.ps1" <<'RUN'
Set-Location $PSScriptRoot
if (Test-Path env) {
	foreach ($l in Get-Content env) { $k, $v = $l -split '=', 2; if ($k) { Set-Item "env:$k" $v } }
}
$bw = if ($env:VAULTCRAB_IT_BW_PATH) { $env:VAULTCRAB_IT_BW_PATH } else { 'bw' }
$pw = [Console]::In.ReadLine()
if (-not $pw) { 'no password: integration tests skipped'; exit 3 }
$env:BW_PASSWORD = $pw
$env:BW_SESSION = (& $bw unlock --passwordenv BW_PASSWORD --raw 2>$null)
Remove-Item env:BW_PASSWORD; $pw = $null
if (-not $env:BW_SESSION) { 'bw unlock failed'; exit 1 }
& $bw sync *> $null
if ($LASTEXITCODE -ne 0) { 'bw sync failed: testing the local copy' }
$rc = 0
foreach ($t in Get-ChildItem *.test.exe) {
	"== $($t.Name)"
	& $t.FullName '-test.run' '^TestIntegration' '-test.v' '-test.count=1' '-test.timeout' '10m'
	if ($LASTEXITCODE -ne 0) { $rc = 1 }
}
& $bw lock *> $null
exit $rc
RUN

declare -A result
for t in $targets; do
	echo "### $t"
	if [ "$t" = linux ]; then
		host=$VM_LINUX dir=$stage/linux ext= envs=${VAULTCRAB_E2E_LINUX_ENV:-}
		run="bash ~/$VM_DIR/run.sh"
	else
		host=$VM_WIN dir=$stage/windows ext=.exe envs=${VAULTCRAB_E2E_WIN_ENV:-}
		run="powershell -NoProfile -ExecutionPolicy Bypass -File $VM_DIR\\run.ps1"
	fi
	os=$t
	[ "$t" = linux ] || os=windows
	if ! build "$os" "$dir" "$ext"; then result[$t]="FAIL (build)"; continue; fi
	if [ "$t" = linux ]; then cp "$stage/run.sh" "$dir/"; else cp "$stage/run.ps1" "$dir/"; fi
	# shellcheck disable=SC2086 # one KEY=VALUE per word
	[ -z "$envs" ] || printf '%s\n' $envs >"$dir/env"
	if [ -n "${VAULTCRAB_E2E_JUMP_KEY:-}" ]; then
		cp "$VAULTCRAB_E2E_JUMP_KEY" "$dir/jumpkey" && chmod 600 "$dir/jumpkey"
		printf '%s\n' "VAULTCRAB_IT_JUMP=${VAULTCRAB_E2E_JUMP:-}" "VAULTCRAB_IT_JUMP_TARGET=${VAULTCRAB_E2E_JUMP_TARGET:-}" \
			"VAULTCRAB_IT_JUMP_KEY=jumpkey" >>"$dir/env"
	fi
	if ! vm_upload "$host" "$dir"; then result[$t]="FAIL (upload to $host)"; continue; fi
	echo "-- version"
	vm_version "$host" "$version" || { result[$t]="FAIL (--version)"; continue; }
	echo "-- smoke"
	vm_smoke "$host" || { result[$t]="FAIL (smoke)"; continue; }
	echo "-- integration tests"
	password | vm_ssh "$host" "$run" | tr -d '\r' | tee "$stage/$t.log"
	case ${PIPESTATUS[1]} in
	0) result[$t]="PASS ($(grep -c '^--- PASS' "$stage/$t.log") integration tests)" ;;
	3) result[$t]="PASS (version, smoke); integration tests SKIPPED: no password" ;;
	*) result[$t]="FAIL (integration tests: $(grep '^--- FAIL' "$stage/$t.log" | tr '\n' ' '))" ;;
	esac
done

echo
echo "=== e2e summary (vaultcrab $version)"
rc=0
for t in $targets; do
	printf '%-8s %s\n' "$t" "${result[$t]}"
	case ${result[$t]} in PASS*SKIPPED*) [ "$rc" != 0 ] || rc=3 ;; PASS*) ;; *) rc=1 ;; esac
done
exit $rc
