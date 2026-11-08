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

![Host list: type filter tabs, folder tree, item card, widget column](docs/screenshot-hosts.png)

![A tab split into three panes](docs/screenshot-panes.png)

<details>
<summary>More screenshots</summary>

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
  in the item (notes, hidden fields, TOTP, password history) is kept.
- **Scripts, widgets, local shell.** Per-item launch scripts with placeholders, a widget column
  with the output of your commands, a local shell (`$SHELL`, or PowerShell via ConPTY on Windows).
- **Safe multi-line paste.** Pasted text of several lines is shown first and pasted only after
  confirmation; **Edit** (`e`) opens it in an editor (`Ctrl+S` pastes the edited text).
- **Settings inside the program** (`,`), saved to `config.yaml`; a hand-edited config is re-read
  automatically.

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

Go 1.26 or newer, no cgo:

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
host). Hidden fields and notes never become metadata.

## Several vaults

Besides the vault of the `bw:` section you can add further Bitwarden/Vaultwarden vaults (other
accounts or servers) in **Settings → Vaults** (`a`): Vaultcrab writes them to `vaults:` in the
config, runs `bw config server` and `bw login` for each with its own bw data directory, and starts a
`bw serve` per vault. Their items appear in the host list under a folder named after the vault, the
host card shows which vault, account and server an item belongs to, the status bar shows the state
of each vault, and the unlock dialog names the vault it unlocks. Items can be edited in their vault
(not moved between vaults). See `vaults:` in [`docs/config.example.yaml`](docs/config.example.yaml).

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

Keys: `Enter` connect, `/` search, `x` scripts, `t` local shell, `a`/`e`/`C`/`D` add/edit/copy/delete,
`p` copy password, `?` help. Session commands use the prefix `Ctrl+B` (configurable): `|` and `-`
split (the host of the current pane is offered first, so `Enter` clones it), `c` clone the session
into a new tab, arrows move focus, `z` zoom, `[` scrollback, `x` close pane, `&` close the tab with
all its panes. In a split tab each pane header has a `✕` button that closes just that pane; `Ctrl+D`
closes a broken or finished pane (`Enter` reconnects it).

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
