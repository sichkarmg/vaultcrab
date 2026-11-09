<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/vaultcrab-logo-dark.svg">
    <img alt="Vaultcrab" src="assets/vaultcrab-logo-light.svg" width="440">
  </picture>
</p>

# Vaultcrab

**Cross-platform terminal session manager for SSH, VNC, SMB and custom protocols.
Credentials stay in your vault (Bitwarden/Vaultwarden, gopass).**

**English** · [Русский](README.ru.md)

> **Project status: early (v0.3.0).** Config keys and behaviour may still change. The
> Bitwarden/Vaultwarden provider is implemented; gopass is planned (the extension point is
> described in [`docs/extending.md`](docs/extending.md)).

Vaultcrab is a single static binary for Linux, Windows and macOS: a host list read from your
password vault, a built-in SSH client and terminal emulator, tabs and tmux-like split panes,
mouse support, a local shell, your own scripts and widgets. Passwords and keys are fetched from
the vault only at the moment of connecting and never written to disk.

![Host list: type filter tabs, folder tree, item card, widget column; sessions through a jump host in the tab bar (two of one host grouped), ⇄ the running tunnels](docs/screenshot-hosts.png)

![Tunnels: Proxmox web through a jump host, a stopped saved tunnel, a failed one, a SOCKS proxy](docs/screenshot-tunnels.png)

<details>
<summary>More screenshots</summary>

![Through a jump host, step 2: the targets used before, the credentials](docs/screenshot-jump.png)

![A failed connection with its debug steps (ssh.debug)](docs/screenshot-debug.png)

![A tab split into three panes](docs/screenshot-panes.png)

![Settings → Appearance](docs/screenshot-settings.png)

![Settings → External programs: VNC client, password mode "proxy"](docs/screenshot-programs.png)

</details>

## What it does

- **Hosts from your vault.** Bitwarden/Vaultwarden through a local `bw serve` (started
  automatically on `127.0.0.1` and a random port): folders (nested `A/B/C`), organizations and
  collections. Metadata is cached for an offline mode (no secrets in the cache).
- **Secrets only when connecting.** The password or private key is read on every (re)connect and
  wiped from memory right after the handshake. It is not cached, not written to disk and not passed
  through argv or environment variables.
- **Private key in the password field** (OpenSSH, PKCS#1, SEC1, PKCS#8; also "flattened" into one
  line or with CRLF): parsed in memory, no temporary files, no ssh-agent.
- **Built-in SSH** (`golang.org/x/crypto/ssh`): password, keyboard-interactive, keys; own
  `known_hosts` with trust on first use; keepalives; legacy algorithms as a last resort for old
  devices (e.g. MikroTik RouterOS 6).
- **Through a jump host** (`j`): pick a jump host or template, type the IP of the target — no vault
  item per machine behind a bastion; host keys of both hops checked, a history of the targets used.
- **Tunnels** (`Ctrl+T` in the jump picker, `T` on a host, prefix `T`): forward a local port through a
  jump host (Proxmox web on `https://localhost:port`) or run a SOCKS proxy; saved tunnels, reconnection,
  `127.0.0.1` only by default.
- **Tabs and panes.** Each session is a terminal emulator (16/256/true colour, Unicode, `htop`,
  `mc`, `vim`, mouse in remote apps). Split panes horizontally and vertically, resize with keys or
  by dragging borders. Dropped sessions are marked `✗` (or, with `ui.tab_status: color`, by the
  colour of the tab number: green connected, yellow connecting, red broken); `Enter` reconnects and
  keeps the scrollback (memory only). With many tabs the `0:Hosts` tab always stays visible.
- **VNC, SMB, FTP, SFTP, RDP** through external clients, auto-detected per OS. VNC signs in without
  a password prompt: Vaultcrab authenticates to the server itself and lets the client in through a
  one-shot local proxy (optionally through an SSH jump host).
- **Filters and search** with a small rule language (`tag:linux folder:Lab/** scheme:ssh`), icons
  with ASCII fallbacks.
- **Add, edit, copy and delete items** from the TUI; only the host fields change, everything else
  in the item (notes, hidden fields, TOTP, password history) is kept. In the password field
  `Ctrl+R` or `[Show]` shows what is typed and the current password of the item (a key by its first
  line); `[Hide]` masks it again.
- **Scripts, widgets, local shell.** Per-item launch scripts with placeholders, a widget column
  with the output of your commands, a local shell (`$SHELL`, or PowerShell via ConPTY on Windows).
- **Safe multi-line paste.** Pasted text of several lines is shown first and pasted only after
  confirmation; **Edit** (`e`) opens it in an editor (`Ctrl+S` pastes the edited text).
- **Settings inside the program** (`,`), saved to `config.yaml`; a hand-edited config is re-read
  automatically. Every dialog has a `✕` in its top border to close it with the mouse.

## Supported systems

| OS | Release archive |
|---|---|
| Linux x86-64 / ARM64 (static, no dependencies) | `vaultcrab_<version>_linux_amd64.tar.gz` / `…_linux_arm64.tar.gz` |
| Windows 10/11 x64 | `vaultcrab_<version>_windows_amd64.zip` |
| macOS Intel / Apple Silicon | `vaultcrab_<version>_darwin_amd64.tar.gz` / `…_darwin_arm64.tar.gz` |

Each archive contains the `vaultcrab` binary (`vaultcrab.exe` on Windows), `README.md`, `LICENSE`
and `THIRD_PARTY_NOTICES.md`.

**The macOS and Windows builds are not signed.** Gatekeeper and SmartScreen may warn on first
start (see the macOS instructions below for removing the quarantine flag).

## Installation

You need two programs: `vaultcrab` and the **Bitwarden CLI (`bw`)**. `bw` is installed separately
and is **not** shipped with Vaultcrab. Download the archive for your system and `checksums.txt`
from [Releases](https://github.com/sichkarmg/vaultcrab/releases), check and unpack it:

```sh
sha256sum -c checksums.txt --ignore-missing   # macOS: shasum -a 256 -c …; Windows: Get-FileHash <file>
tar -xzf vaultcrab_<version>_linux_amd64.tar.gz   # Windows: Expand-Archive <file>.zip
```

### Linux

```sh
mkdir -p ~/.local/bin
install -m 755 vaultcrab ~/.local/bin/vaultcrab
# Bitwarden CLI: the official zip, or `sudo snap install bw`, or `npm i -g @bitwarden/cli`
curl -L -o /tmp/bw.zip 'https://bitwarden.com/download/?app=cli&platform=linux'
unzip -o /tmp/bw.zip -d ~/.local/bin && chmod +x ~/.local/bin/bw

bw config server https://vault.example.com    # your Vaultwarden; not needed for bitwarden.com
bw login you@example.com
vaultcrab
```

### Windows (PowerShell)

```powershell
$dir = "$env:LOCALAPPDATA\vaultcrab\bin"
New-Item -ItemType Directory -Force $dir | Out-Null
Copy-Item .\vaultcrab.exe "$dir\vaultcrab.exe"
[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path','User') + ";$dir", 'User')
winget install Bitwarden.CLI

# in a new terminal window
bw config server https://vault.example.com
bw login you@example.com
vaultcrab
```

Windows Terminal is recommended (Unicode icons); in the classic console Vaultcrab switches to ASCII
icons automatically.

### macOS

```sh
mkdir -p ~/.local/bin
install -m 755 vaultcrab ~/.local/bin/vaultcrab   # from the darwin_arm64 (Apple Silicon) or darwin_amd64 archive
xattr -d com.apple.quarantine ~/.local/bin/vaultcrab 2>/dev/null   # the binary is not signed
brew install bitwarden-cli

bw config server https://vault.example.com
bw login you@example.com
vaultcrab
```

**Self-hosted Vaultwarden with a private CA:** `bw` (Node.js) ignores the system certificate store;
set `bw.env: [NODE_EXTRA_CA_CERTS=/path/to/ca.pem]` in the config (passed to `bw` only).

## Building from source

Go 1.26 or newer. No cgo, except on macOS: built on a Mac, `make` uses cgo for the screen lock
detection (Settings → Security); a macOS binary cross-built elsewhere works without it.

```sh
make build          # dist/vaultcrab for this OS
make build-all      # dist/vaultcrab-{linux-amd64,linux-arm64,windows-amd64.exe,darwin-amd64,darwin-arm64}
make test vet       # go test ./... and go vet ./...
make fmt-check      # gofmt
make release-check  # checks before a release tag (see RELEASING.md)
```

Releases are built by GitHub Actions with GoReleaser ([RELEASING.md](RELEASING.md)); changes are
listed in [CHANGELOG.md](CHANGELOG.md). Integration tests against a real `bw serve` use the build tag
`integration` ([docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)).

`third_party/` contains small patched forks of three dependencies (Bubble Tea, Ultraviolet,
x/vt), wired in with `replace` in `go.mod`; the patches are listed in `third_party/*/PATCHES.md`.

## Vault item format

A host is a Login item in one of these forms:

| Form | `username` | URI | Custom fields |
|---|---|---|---|
| flag field | `user@host` or `user@host:port` (IPv6: `user@[::1]:22`) | — | `ssh` = `true` |
| URI | `user` | `ssh://host:22`, `vnc://host:5900`, `smb://host/share`, `rdp://host`, `ftp://host/dir`, `sftp://host` | — |

**Coming from another program?** Sessions can be imported into the vault with `bw import`; see
[`imports/README.md`](imports/README.md) for the JSON format and a converter for SecureCRT.

The password or private key goes into the `password` field. Optional fields: `port`, `tags`, any
others (`env`, …) for filters; `script` (the script `Enter` runs); `jump` (VNC through an SSH
host); `knock` and `knock_delay` (port knocking before connecting, see below). Hidden fields and notes never become metadata. A Login item without an address but with a
field `credential` = `true` is not a host: it is a credential offered for addresses typed through a
jump host (see [Jump hosts](#jump-hosts)).

### Port knocking

A host whose SSH port opens only after a knock (knockd, a MikroTik or iptables rule): put the
sequence into a custom field **`knock`** of its item (or the **Knock** field of the item form, `e`/`a`) — ports separated by commas or spaces, each
`port`, `port/tcp` or `port/udp` (TCP by default), e.g. `7000,8000/udp,9000` — and optionally
**`knock_delay`** (the pause after each knock, `200ms` by default, up to `10s`). Before every connection
(and reconnection) Vaultcrab sends a TCP connection attempt or a UDP packet to each port in turn from
this computer, then connects; the steps appear in the connection debug. The same applies to a host
used as a **jump host** (also for its tunnels); a jump template can set `knock:` / `knock_delay:`
itself (Settings → Jump hosts → Knock) for a jump host typed as an address. A target behind a jump
host is not knocked (the knocks would have to come from the jump host). The sequence is not a secret
of the item: it is a plain field, like the port.

## Several vaults

Besides the vault of the `bw:` section you can add further Bitwarden/Vaultwarden vaults (other
accounts or servers) in **Settings → Vaults** (`a`): Vaultcrab writes them to `vaults:` in the
config, runs `bw config server` and `bw login` for each with its own bw data directory, and starts a
`bw serve` per vault. Their items appear in the host list under a folder named after the vault, the
host card shows which vault, account and server an item belongs to, the status bar shows the state
of each vault, and the unlock dialog names the vault it unlocks. Items can be edited in their vault
(not moved between vaults). See `vaults:` in [`docs/config.example.yaml`](docs/config.example.yaml).

## Jump hosts

For machines reachable only through a jump host (bastion), e.g. dozens of workstations with one SSH
key whose IP addresses keep changing, there is no need for a vault item per machine:

- **`j`** in the host list (prefix `j` anywhere) opens a two-step picker. **Step 1**: the jump host:
  a template or any SSH host of the list (the ones used most and most recently first, the rest
  searched by typing, fuzzy). **Step 2**: the target: type an IP or `[user@]host[:port]`, or pick a
  target used before through this jump host (most used and most recent first) or a host of the list
  (it then connects with its own user and credential). `Enter` connects, `Esc` goes back; mouse: a
  click selects, a click on the selected row chooses it.
  The picker belongs to the host list (tab 0): while it is open the other tabs can be switched to
  (prefix + digit, `n`/`p`/`l`, `Ctrl+PgDn`/`PgUp`, a click on a tab); tab 0 shows `0:Hosts ↪`
  (ASCII: `~>`), and the picker is there again with the step and the typed address on the return
  (prefix `j` from any tab returns to it too).
- **Credentials of typed addresses** (`Ctrl+K` in step 2), for thousands of machines with one
  password or key: the default (the template's key, else the jump host's credential), a **password
  typed once** (kept in memory until Vaultcrab exits, for every address through this jump host; asked
  again if a target refuses it), the ssh-agent, a key file, or a **vault item**: any SSH host, or a
  Login item marked with a custom field `credential` = `true` (its password field holds the password
  or the private key, its username is the default user). The choice is remembered per jump host (in
  the history: the reference, never the secret) and offered again for the next address.
- **Templates** (`jump_templates:` in the config, or Settings → Jump hosts, applied without a
  restart; `J` on a host of the list makes one of it): the jump host (a vault item or
  `[user@]host[:port]`), the user and port of the targets, the key of the targets (`~/.ssh/id_ed25519`,
  `agent`, `password` or `vault:<item>`), the key of the jump host (`jump_key`, by default the credential of its
  vault item, else `key`), and the host key check. See [`docs/config.example.yaml`](docs/config.example.yaml).
- The connection is the built-in SSH client: it logs in to the jump host, opens a TCP channel from it
  to the target and runs the second SSH session over that channel (like `ssh -J`). **The agent is
  never forwarded.** The session is an ordinary tab (`↪ jump → target`): split, clone, rename
  (prefix `R`), reconnect (both hops again), saved with the open tabs.
- **Keys**: a key file with a passphrase is asked for once per run of Vaultcrab and kept in memory
  only (never written to disk); `agent` uses `SSH_AUTH_SOCK` (Windows: the OpenSSH agent); a vault item
  is fetched for each connection and wiped after the handshake, like any credential.
- **Host keys of both hops** are checked against Vaultcrab's `known_hosts`. `host_key_check: ask`
  (default) shows a new key with its fingerprint: accept and remember, accept once (useful for
  targets whose address will belong to another machine tomorrow), or reject. `accept-new` remembers
  new keys silently (Settings warns about it); `strict` refuses unknown hosts. **A key that changed
  for a known address is always refused** with a warning naming the line of `known_hosts` to remove;
  it is never replaced silently.
- **Errors say which stage failed**: the jump host unreachable, the target unreachable from the jump
  host, the authentication on the jump host or on the target, a host key.
- **History** (`jump.history`, on by default): which jump host, target and user were used, when and how
  often — names and addresses only — in `jump-history.json` of the cache directory (not in the vault),
  at most `jump.history_limit` entries (200). Settings → Jump hosts turns it off (`H`) or clears it
  (`c` twice); so does `vaultcrab history clear`.

The key `j` replaces "move down" in the host list (arrows still move); `ui.keys.jump` picks another
key. Chains of several jump hosts and VNC/SMB through such a template are not supported yet (a VNC item
can name a jump host in its `jump` field).

## Tunnels

Port forwarding through a jump host (like `ssh -L`), for web interfaces and other services that are
reachable only from behind it — e.g. Proxmox at `https://192.168.1.10:8006` opened as
`https://localhost:18006` on your computer:

- **From the jump picker**: `j`, the jump host, the target address, then **`Ctrl+T`** instead of
  `Enter`: the tunnel form opens with the target's host; type its port (`8006`) and `Ctrl+S`. The local
  port is any free one unless you type one.
- **Directly through an SSH host**: **`T`** on an SSH host of the list opens the form with that host as
  the jump host and `localhost:` as the target (a port of the host itself, or `other-host:port` it
  reaches).
- **The Tunnels list** (prefix **`T`** anywhere, the **`⇄N`** button of the tab bar while tunnels run,
  Settings → Tunnels): the saved tunnels and the running ones with their state (active, connecting,
  retrying, failed, stopped), `127.0.0.1:18006 → jump → target:port`, the time and the connections
  (open/total). `Enter` starts a saved tunnel or opens a running one in the browser, `s` start/stop,
  `r` restart, `y` copy the URL, `o` open in the browser, `a` add, `e` edit, `S` save a running tunnel
  under a name, `D` twice removes a saved one (and stops it). With the mouse, each row has its buttons:
  `[open] [edit] [del] [x]` (stop) on a running tunnel, `[start] [edit] [del]` on a stopped one (`del`
  asks with a second click). The list stays with the host list: other tabs can be used while it is open.
- **Saved tunnels** (`tunnels:` in the config, or the form with a **Name**; applied without a
  restart): the jump host (a jump template or an SSH host of the vault), the target `host:port`, the
  local port (`auto` or a number), the URL to open (`open:`, `{port}` and `{host}`; default
  `http://{host}:{port}`), the type and the bind address. A saved tunnel runs once: starting it again
  shows the running one.
- **SOCKS proxy** (`type: socks`, like `ssh -D`): a SOCKS5 proxy on the local port; every connection
  goes to the address it asks for, resolved by the jump host. Set `localhost:port` as the SOCKS5 proxy
  of the browser.
- **Only this computer**: tunnels listen on `127.0.0.1`. Another address (`bind:`, e.g. `0.0.0.0`)
  must be set explicitly, and the form and the list warn that other computers can then use the tunnel.
- **A busy port**: the form offers a free one (`Ctrl+S` again takes it); a saved tunnel whose port is
  busy when it starts takes the next free one and says so.
- **One connection per jump host**: the tunnels and the shells through the same jump host (same user
  and key) share one SSH connection to it; the last one to end closes it. Authentication, keys,
  passphrases and host key checks are those of the jump connections; no secret goes into arguments,
  the environment or logs.
- **Reconnection**: when the connection to the jump host is lost (keepalives notice it, e.g. after a
  sleep), the tunnels connect again after 1 s, 2 s, 4 s … up to 1 min, at most `tunnel.attempts` times
  (8); the status bar tells about the loss and the reconnection. Then the tunnel is *failed* until `r`
  or a new connection to its port, which tries again at once. A refused authentication or host key
  fails at once. Nothing runs on a timer while tunnels are idle.
- **Open in the browser**: `tunnel.browser` (a command, `{url}` replaced) or the system default (`open`,
  `xdg-open`, `rundll32 url.dll`). For `https` the list warns that the page probably has a self-signed
  certificate (Proxmox, routers): check it in the browser before accepting it.
- Quitting Vaultcrab closes all tunnels. Reverse forwarding (`-R`), chains of several jump hosts and
  VNC/SMB through a tunnel are not supported yet.

## Offline, sleep and a crashed bw serve

- **Server unreachable** (no network, VPN down): the status bar says *Offline: server unreachable,
  local copy*. The host list and the passwords come from bw's local encrypted copy, no password is
  asked. Before each sync Vaultcrab checks that the server accepts a connection (through the proxy
  of the bw environment, if any) and does not ask bw to sync while it does not; the check is
  repeated after 15 s, 30 s, 1 min, … up to every 5 min (`s` checks now), and the vault is synced as
  soon as the server is back.
- **bw serve exited** (status *bw serve stopped, restarting…*): it is started again at once (with a
  growing delay if it keeps exiting). A connection attempt meanwhile waits for it. The vault stays
  unlocked: the new `bw serve` gets the session key of the old one (see the security notes).
- **Locked**: only then is the master password asked. The unlock dialog says why when Vaultcrab did
  not lock it itself (bw locked it, or a restarted bw serve could not reopen it).
- `s` never restarts a running `bw serve` (that would lock it). The event log records the reason of
  every state change (the error line of a crashed `bw serve`, the failed server check).

Why: bw (checked with 2026.10) exits when it has to refresh its access token while the server is
unreachable — typically the first sync after a sleep longer than the token lifetime (2 h on
Vaultwarden), before the VPN is back. A new `bw serve` starts locked, which is why earlier versions
asked for the master password after such a sleep. bw serve itself has no vault timeout timer.

## Auto-lock

Settings → **Security** (`unlock:` in the config, applied at once):

| Setting | Default | |
|---|---|---|
| Lock after no input for (`lock_timeout`) | 8 hours | 15 min, 1 hour, 8 hours, 1 day, 3 days, 5 days, never |
| Lock with the screen (`lock_on_screen_lock`) | off | also lock when the screen is locked |

- The timeout counts from the last key or mouse input in Vaultcrab, in wall-clock time: time asleep
  counts, so after a long sleep the first key opens the unlock dialog (and does nothing else). A
  sleep by itself does not lock. Locking asks bw to lock (`bw lock`), which also drops the session
  key; open sessions stay connected.
- **Longer than 8 hours or never** is a weaker setting, and the settings page says so: while the
  vault is unlocked, `bw serve` answers on `127.0.0.1` without authentication, so any program of
  your user account can read every password for that long.
- Screen lock detection: Linux through D-Bus (the screensaver's `ActiveChanged` — GNOME, Cinnamon,
  MATE, KDE… — and the logind `Lock` of the session), Windows through the session lock notification,
  macOS through `com.apple.screenIsLocked` (a closed lid counts when it locks the screen). The macOS
  detection needs a build with cgo: the release binaries have it, a cross-built binary does not
  (the settings page says so). Without a graphical session (e.g. over ssh) locks are not seen.

### Quick unlock: PIN

Settings → Security → **PIN unlock**: enter the PIN twice, then the master password of an unlocked
vault (bw checks it). The master password is kept encrypted in the secure store of the system — the
macOS Keychain, the Windows Credential Manager (DPAPI) or the Linux Secret Service (GNOME Keyring,
KWallet, KeePassXC); the config only gets `unlock.pin: true`. The unlock dialog then asks for the PIN
(`Tab`: the master password).

- At least 6 digits (`pin_alphanumeric: true` allows letters); a repeated digit or a plain sequence
  is refused. The input is hidden.
- Encryption: argon2id derives a key from the PIN (random salt, 256 MiB, 3 passes, about a second);
  it encrypts (AES-256-GCM) an X25519 private key, and the master passwords are sealed to its public
  key. So the stored copy is renewed after every unlock with the master password without the PIN.
- Wrong PINs are counted in the secure store; after `pin_attempts` (default 5) in a row the stored
  passwords and the PIN are deleted and the master password is needed.
- The master password is asked again after a restart of the computer (`master_after_reboot`) and
  every `master_every_days` days (default 14). A logout of bw, another account, or a password bw no
  longer accepts (a changed master password) deletes the stored copy.
- `p` on the Security page changes the PIN; `F` twice or **`vaultcrab forget`** deletes the PIN and
  every stored master password (and the Touch ID data).
- Secrets never go into argv, the environment, the log or the config; on Linux and macOS Vaultcrab
  disables core dumps (on Linux the process is also closed to ptrace by other processes of the user).
- Without a secure store (Linux without a Secret Service or with a locked keyring, Windows over SSH,
  a macOS build without cgo) the PIN is not available, and the settings page says why.

**Threat model, strongest first:**

1. **Master password** — nothing is stored; as strong as the password and Bitwarden's KDF.
2. **Touch ID** — a fingerprint in front of the stored password; see the next section for why it is
   weaker than with a signed app. Changed fingerprints invalidate it.
3. **PIN** — weaker than the master password. The attempt limit only applies inside Vaultcrab:
   someone who copies the secure store (a process of your account while the keyring is open, or a
   backup) can try PINs offline without it; each try costs about a second of argon2id, and 6 digits
   are a million candidates. Use a PIN only on your own computer, and prefer a long one.

### Quick unlock: Touch ID (macOS)

Settings → Security → **Touch ID** (`unlock.touch_id`): the master password of an unlocked vault
(checked by bw) and one fingerprint turn it on. The unlock dialog then shows the Touch ID prompt at
once; if Touch ID is not available, canceled, not recognized or **the enrolled fingerprints changed**
(the stored data is then deleted), it falls back to the PIN and then to the master password. The
reboot and N-days rules of the PIN apply too; `vaultcrab forget` deletes the Touch ID data.
**Enter** on the empty input (or **Ctrl+T**) asks for Touch ID again from the same dialog. A dialog
that opens by itself after a lock (the auto-lock, the screen lock, bw locking the vault) says the
vault is locked and waits for Enter before it asks for Touch ID, so a prompt never pops up
unasked; `u` (unlock) asks at once.

**Weaker model than a signed app.** Vaultcrab is not signed with an Apple Developer ID, so its
Keychain item cannot carry a biometric access control of the Data Protection Keychain. Vaultcrab
asks for Touch ID itself and then reads the item from the login keychain (accessible only while the
Mac is unlocked, never synced). A program that reads the item directly skips Touch ID — macOS asks
the user to allow that for any binary other than the one that created it (also for a new Vaultcrab
version: allow it once). On Linux and Windows the setting is ignored; macOS binaries built without cgo
(cross-compiled) have no Touch ID — the release binaries have it.

## Configuration

The config file is optional:

| OS | Path |
|---|---|
| Linux | `~/.config/vaultcrab/config.yaml` |
| Windows | `%AppData%\vaultcrab\config.yaml` |
| macOS | `~/Library/Application Support/vaultcrab/config.yaml` |

A fully commented example: [`docs/config.example.yaml`](docs/config.example.yaml). Most settings
can also be changed in the program (`,` or `⚙`). Command line: `-config FILE`, `-ascii`, `-version`.
**Event log:** Settings → Behavior → Event log (`log.enabled`) writes vault states, connections,
external programs, config reloads and errors to `vaultcrab.log` in the cache directory (`log.file`;
rotated at 1 MiB). Passwords, typed keys and terminal output are never logged. While it is on, a `≡`
button in the tab bar (or prefix `g`) shows the log. `VAULTCRAB_DEBUG_LOG=/path/file` additionally
logs mouse events and window sizes for debugging.

**Connection debug** (Settings → Behavior, `ssh.debug`, on by default): every connection attempt is
traced like `ssh -vvv` — name resolution, TCP, the server version, the host key and its check, the
authentication methods offered (keys by type and fingerprint, never a password), the channel through
a jump host, the PTY. When the attempt fails, the steps are shown in the pane; `y` or `[Copy debug]` at
the right of the status bar copies them.

Keys: `Enter` connect, `/` search, `x` scripts, `t` local shell, `j` through a jump host, `T` a tunnel,
`a`/`e`/`C`/`D` add/edit/copy/delete, `p` copy password, `?` help. Session commands use the prefix
`Ctrl+B` (configurable): `|` and `-` split (the host of the current pane is offered first, so `Enter`
clones it), `c` clone the session into a new tab, `R` rename the tab, `j` jump host picker, `T` tunnels, arrows
move focus, `z` zoom, `[` scrollback, `x` close pane, `&` close the tab with all its panes. In a split
tab each pane header has a `✕` button that closes just that pane; `Ctrl+D`
closes a broken or finished pane (`Enter` reconnects it).

**Tab bar** (Settings → Appearance): `✕` on the active tab closes it, connected or not
(`ui.tab_close_button`, on); a click on the state icon or number of a tab closes it, the active tab
after a confirmation (`ui.tab_status_close`, off); the tabs of one host are grouped into one entry
`N:host ×3 ▸` — a click opens the group (the name once, then each tab by its state and number:
`▾ host ●4 ●5 ✗6`; a click on a number switches to that tab), a click on the
header folds it again (`ui.group_tabs`, on). Tab numbers and keys still address every tab.

## External clients (VNC, SMB, SFTP, …)

Clients are configured per scheme and OS under `external:` or in Settings → External programs,
with auto-detection of well-known clients (TigerVNC, TightVNC, RealVNC, Remmina, FileZilla, WinSCP,
FreeRDP, mstsc, the system file manager, macOS `open`), argument presets, checks and a test start.

VNC password modes (`external.vnc.password`):

| Mode | How the client gets the password |
|---|---|
| `proxy` (default) | it does not: Vaultcrab signs in and the client connects to a local one-shot proxy |
| `stdin` | one line on stdin |
| `file` | a vncpasswd file `{passfile}` (0600, removed after 10 s or when the client exits) |
| `url` | `{url}` = `vnc://user:pass@host:port` (macOS `open`) |
| `argv` | `{password}` on the command line |

## Security notes

- Secrets are requested from the vault only when connecting, kept in byte slices and wiped right
  after the handshake. Only metadata is cached; tests check that.
- **`{password}` placeholder.** You can put `{password}` into an external program's or script's
  command line (e.g. `smb://{user}:{password}@{host}/{path}`). It is masked in the UI, but **other
  users of the computer can see it in the process list** while the program runs; the settings page
  warns about this. Prefer `proxy`, `stdin` or `file` modes where possible.
- `bw serve` listens on `127.0.0.1` only, but it has no authentication of its own: while the vault is
  unlocked, any local process of the same machine can query it (a property of the Bitwarden CLI).
  Vaultcrab stops it on exit, on signals and, through a helper process / Job Object, even when it is
  killed.
- **Session key of bw serve.** While a vault is unlocked, Vaultcrab keeps the session key that bw
  returned on unlock (not the master password) in memory, to restart a crashed `bw serve` unlocked.
  The restarted process gets it as `BW_SESSION` in its environment, which processes of the same
  user can read (they can query the unlocked `bw serve` anyway). The key is dropped when the vault
  is locked (bw then also deletes the copy of the vault key it encrypts with it) or the connection
  is closed; after Vaultcrab exits it opens nothing.
- Host keys are checked against Vaultcrab's own `known_hosts`; a changed key is refused.
  `ssh.strict_host_keys: true` refuses unknown hosts too.
- Copied text goes to the clipboard (that is the point of copying); the system clipboard is cleared
  30 s after copying a password.

Please report vulnerabilities privately (see [SECURITY.md](SECURITY.md)).

## Limitations

- Passphrase-protected private keys are not supported.
- An organization item cannot be moved back to the personal vault (a Bitwarden restriction).
- SMB passwords are entered in the external client.
- Keys typed before a connection is established are not buffered.

## Extending

Vault providers and protocols have explicit extension points: see
[`docs/extending.md`](docs/extending.md) and [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md).

## License

Vaultcrab is open source under the [MIT License](LICENSE).

The **name "Vaultcrab" and the logos** (`assets/`) are **not** licensed under MIT. Forks and derived
products must use a different name and a different logo. See [TRADEMARKS.md](TRADEMARKS.md).
Third-party licenses: [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

Vaultcrab is not affiliated with or endorsed by Bitwarden, Vaultwarden, TigerVNC or other
mentioned projects.
