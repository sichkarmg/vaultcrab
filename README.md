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

> **Project status: early (v0.1.0).** Config keys and behaviour may still change. The
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
  by dragging borders. Dropped sessions are marked `✗`; `Enter` reconnects and keeps the scrollback
  (memory only).
- **VNC, SMB, FTP, SFTP, RDP** through external clients, auto-detected per OS. VNC signs in without
  a password prompt: Vaultcrab authenticates to the server itself and lets the client in through a
  one-shot local proxy (optionally through an SSH jump host).
- **Filters and search** with a small rule language (`tag:linux folder:Lab/** scheme:ssh`), icons
  with ASCII fallbacks.
- **Add, edit, copy and delete items** from the TUI; only the host fields change, everything else
  in the item (notes, hidden fields, TOTP, password history) is kept.
- **Scripts, widgets, local shell.** Per-item launch scripts with placeholders, a widget column
  with the output of your commands, a local shell (`$SHELL`, or PowerShell via ConPTY on Windows).
- **Settings inside the program** (`,`), saved to `config.yaml`; a hand-edited config is re-read
  automatically.

## Supported systems

| OS | Binary |
|---|---|
| Linux x86-64 (static, no dependencies) | `vaultcrab-linux-amd64` |
| Windows 10/11 x64 | `vaultcrab-windows-amd64.exe` |
| macOS Intel / Apple Silicon | `vaultcrab-darwin-amd64` / `vaultcrab-darwin-arm64` |

**The macOS and Windows builds are not signed.** Gatekeeper and SmartScreen may warn on first
start (see the macOS instructions below for removing the quarantine flag).

## Installation

You need two programs: `vaultcrab` and the **Bitwarden CLI (`bw`)**. `bw` is installed separately
and is **not** shipped with Vaultcrab. Download Vaultcrab from
[Releases](https://github.com/sichkarmg/vaultcrab/releases) and check it against `SHA256SUMS`:

```sh
sha256sum -c SHA256SUMS --ignore-missing      # Windows: Get-FileHash <file>
```

### Linux

```sh
mkdir -p ~/.local/bin
install -m 755 vaultcrab-linux-amd64 ~/.local/bin/vaultcrab
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
Copy-Item .\vaultcrab-windows-amd64.exe "$dir\vaultcrab.exe"
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
install -m 755 vaultcrab-darwin-arm64 ~/.local/bin/vaultcrab      # amd64 for Intel Macs
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
make build        # dist/vaultcrab for this OS
make build-all    # dist/vaultcrab-{linux-amd64,windows-amd64.exe,darwin-amd64,darwin-arm64}
make release      # build-all + dist/SHA256SUMS (needs a clean tree; version = git describe)
make test vet     # go test ./... and go vet ./...
```

`third_party/` contains small patched forks of three dependencies (Bubble Tea, Ultraviolet,
x/vt), wired in with `replace` in `go.mod`; the patches are listed in `third_party/*/PATCHES.md`.

## Vault item format

A host is a Login item in one of these forms:

| Form | `username` | URI | Custom fields |
|---|---|---|---|
| flag field | `user@host` or `user@host:port` (IPv6: `user@[::1]:22`) | — | `ssh` = `true` |
| URI | `user` | `ssh://host:22`, `vnc://host:5900`, `smb://host/share`, `rdp://host`, `ftp://host/dir`, `sftp://host` | — |

The password or private key goes into the `password` field. Optional fields: `port`, `tags`, any
others (`env`, …) for filters; `script` (the script `Enter` runs); `jump` (VNC through an SSH
host). Hidden fields and notes never become metadata.

## Configuration

The config file is optional:

| OS | Path |
|---|---|
| Linux | `~/.config/vaultcrab/config.yaml` |
| Windows | `%AppData%\vaultcrab\config.yaml` |
| macOS | `~/Library/Application Support/vaultcrab/config.yaml` |

A fully commented example: [`docs/config.example.yaml`](docs/config.example.yaml). Most settings
can also be changed in the program (`,` or `⚙`). Command line: `-config FILE`, `-ascii`, `-version`.
`VAULTCRAB_DEBUG_LOG=/path/file` logs mouse events and window sizes (key presses are never logged).

Keys: `Enter` connect, `/` search, `x` scripts, `t` local shell, `a`/`e`/`C`/`D` add/edit/copy/delete,
`p` copy password, `?` help. Session commands use the prefix `Ctrl+B` (configurable): `|` and `-`
split, arrows move focus, `z` zoom, `[` scrollback, `x` close pane.

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
- Host keys are checked against Vaultcrab's own `known_hosts`; a changed key is refused.
  `ssh.strict_host_keys: true` refuses unknown hosts too.
- Copied text goes to the clipboard (that is the point of copying); the system clipboard is cleared
  30 s after copying a password.

Please report vulnerabilities privately (see [CONTRIBUTING.md](CONTRIBUTING.md)).

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
