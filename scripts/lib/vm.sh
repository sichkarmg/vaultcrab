# Helpers for the test VMs, sourced by scripts/e2e.sh and scripts/release-verify.sh.
#
# Variables (all optional):
#   VAULTCRAB_VM_LINUX     ssh destination of the Linux VM   (default: mint20)
#   VAULTCRAB_VM_WIN       ssh destination of the Windows VM (default: win)
#   VAULTCRAB_VM_SSH_OPTS  extra ssh/scp options, e.g. "-i ~/.ssh/test_vms"
#
# The Windows VM runs OpenSSH Server; the work directory is ~/vaultcrab-e2e on
# both VMs and is replaced on every run. Only test accounts and test data.

VM_LINUX=${VAULTCRAB_VM_LINUX:-mint20}
VM_WIN=${VAULTCRAB_VM_WIN:-win}
# shellcheck disable=SC2206 # word splitting of the options is intended
VM_OPTS=(-o BatchMode=yes -o ConnectTimeout=10 -o LogLevel=ERROR ${VAULTCRAB_VM_SSH_OPTS:-})
VM_DIR=vaultcrab-e2e

vm_ssh() { local host=$1; shift; ssh "${VM_OPTS[@]}" "$host" "$@"; }

# vm_upload HOST LOCALDIR: replaces ~/vaultcrab-e2e on HOST with LOCALDIR.
vm_upload() {
	local host=$1 dir=$2
	if [ "$host" = "$VM_WIN" ]; then
		vm_ssh "$host" "powershell -NoProfile -Command \"Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $VM_DIR\"" </dev/null
	else
		vm_ssh "$host" "rm -rf ~/$VM_DIR" </dev/null
	fi
	scp -q -r "${VM_OPTS[@]}" "$dir" "$host:$VM_DIR"
}

# vm_version HOST WANT: runs `vaultcrab --version` from the uploaded dir and
# compares it with "vaultcrab WANT" (WANT empty: any version).
vm_version() {
	local host=$1 want=$2 got
	if [ "$host" = "$VM_WIN" ]; then
		got=$(vm_ssh "$host" "$VM_DIR\\vaultcrab.exe --version" </dev/null | tr -d '\r')
	else
		got=$(vm_ssh "$host" "~/$VM_DIR/vaultcrab --version" </dev/null)
	fi
	echo "  $got"
	[ -n "$got" ] || return 1
	[ -z "$want" ] || [ "$got" = "vaultcrab $want" ]
}

# vm_smoke HOST: starts the TUI in a remote terminal with an empty config for a
# few seconds and checks that it draws its status bar. No vault is unlocked.
# stdin stays open (sleep): on EOF the Windows console ends the program.
vm_smoke() {
	local host=$1 out
	if [ "$host" = "$VM_WIN" ]; then
		out=$(sleep 10 | timeout 8 ssh -tt "${VM_OPTS[@]}" "$host" \
			"$VM_DIR\\vaultcrab.exe -ascii -config $VM_DIR\\smoke\\config.yaml" 2>&1 || true)
		vm_ssh "$host" "powershell -NoProfile -Command \"Get-Process vaultcrab -ErrorAction SilentlyContinue | Where-Object Path -like '*\\$VM_DIR\\*' | Stop-Process -Force\"" </dev/null || true
	else
		out=$(sleep 10 | timeout 8 ssh -tt "${VM_OPTS[@]}" "$host" \
			"cd ~/$VM_DIR && mkdir -p smoke && TERM=xterm-256color XDG_CONFIG_HOME=\$PWD/smoke XDG_CACHE_HOME=\$PWD/smoke ./vaultcrab -ascii" 2>&1 || true)
		vm_ssh "$host" "pkill -f '^\\./vaultcrab -ascii' || true" </dev/null
	fi
	if printf '%s' "$out" | grep -aq 'Vaultcrab'; then
		echo "  TUI started and drew the status bar"
	else
		echo "  TUI output did not contain 'Vaultcrab'"
		return 1
	fi
}
