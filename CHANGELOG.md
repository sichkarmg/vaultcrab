# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] - 2026-11-09

### Added
- Port knocking before a connection: the custom fields `knock` (`7000,8000/udp,9000`) and
  `knock_delay` (`200ms`) of an item; for a jump host also `knock:` / `knock_delay:` of a jump
  template (Settings → Jump hosts → Knock). The knocks appear in the connection debug.
- Every dialog has a `✕` in its top border: a click closes it (like Esc until it is gone).
- Mouse buttons in each row of the Tunnels list: `[open] [edit] [del] [x]` on a running tunnel,
  `[start] [edit] [del]` on a stopped one (`del` asks with a second click).
- The Knock field in the item form (SSH hosts): the port knocking sequence, saved in the field
  `knock` of the item.
- `scripts/screenshots.sh` renders the README screenshots from scenes with a demo vault; new
  screenshots of the tunnels, the jump picker and the connection debug.

### Changed
- A search shows the hosts it finds in folded folders: the folders open for the search (and can
  be folded during it); clearing it brings the folds of the list back.
- The event log stays with the host list like the settings: the other tabs can be used while it is
  open, and it is read again on the return.
- `D` twice (and `[del]`) in the Tunnels list also stops the removed tunnel.

## [0.6.0] - 2026-11-09

### Added
- Tunnels (port forwarding, like `ssh -L`) through a jump host: `Ctrl+T` in step 2 of the jump picker
  (the target's port and an optional local port), `T` on an SSH host of the list (a port of that host
  or of one it reaches), and SOCKS5 proxies (`type: socks`, like `ssh -D`). The Tunnels list (prefix
  `T`, the `⇄N` button of the tab bar, Settings → Tunnels) shows the state, `local → jump → target`,
  the time and the connections; start, stop, restart, copy the URL, open it in the browser (a
  warning about self-signed certificates for https), add, edit, save, remove.
- Saved tunnels (`tunnels:`: name, jump template or SSH host, target, `local_port: auto` or a number,
  `open`, `type`, `bind`) and `tunnel:` (`browser`, `attempts`), applied without a restart.
- Tunnels listen on `127.0.0.1` only unless `bind` says otherwise (with a warning); a busy local port
  is replaced by a free one, and the form offers it.
- A lost connection to the jump host is noticed by keepalives (also after a sleep): the tunnels
  connect again with a backoff (1 s … 1 min, `tunnel.attempts` times), the status bar says so; a
  failed tunnel tries again on `r` or a new connection to its port. Quitting closes the tunnels.

### Changed
- The shells and the tunnels through the same jump host (same user and key) share one SSH connection
  to it; it is closed when the last of them ends.

### Fixed
- After a lost connection, the mouse wheel no longer types `64;62;28M…` into the shell of the next
  connection: the modes an application of the old connection turned on (mouse tracking and its
  encodings, bracketed paste, application keys, the alternate screen) are reset when it ends.
- Windows: editing or copying an item right after it was created, or connecting with the password
  of a new copy at once, no longer fails with "Not found" (bw serve does not see a new item for a
  moment there); such answers are retried for 5 s for items Vaultcrab has just created.

## [0.5.0] - 2026-11-09

### Added
- Tab bar (Settings → Appearance): `✕` on the active tab closes it, connected or not
  (`ui.tab_close_button`, on); a click on the state icon or number closes a tab, the active one after a
  confirmation (`ui.tab_status_close`, off); the tabs of one host are grouped into one entry that opens
  and folds on a click (`ui.group_tabs`, on).
- Connection debug (Settings → Behavior, `ssh.debug`, on by default): each attempt is traced like
  `ssh -vvv` (DNS, TCP, versions, host key check, authentication methods, jump channel, PTY; no
  secrets); a failed attempt shows the steps in its pane, `y` or `[Copy debug]` copies them.
- Show the password in the item form (`Ctrl+R` or `[Show]` next to the field): what is typed, and the
  current password of the item read from the vault (a private key by its first line); `[Hide]` masks
  it again and forgets it.
- Connections through a jump host (`j` in the host list, prefix `j` anywhere): a two-step picker,
  first the jump host (a template or any SSH host; used ones first, fuzzy search), then the target
  (an IP or `[user@]host[:port]` typed, a target used before, or a host of the list). Built-in SSH
  over a channel of the jump host, the agent is never forwarded; tabs show `↪ jump → target` and
  reconnect through both hops.
- Jump templates (`jump_templates:`, Settings → Jump hosts, `J` on a host): the jump host, user, port,
  keys (a file with the passphrase asked once and kept in memory, the ssh-agent, or a vault item) and
  the host key check (`ask` by default, `accept-new`, `strict`); a changed host key of a known address
  is always refused, on both hops.
- Credentials of addresses typed through a jump host (`Ctrl+K` in step 2): a password typed once
  per jump host (kept in memory until exit), the ssh-agent, a key file, or a vault item (an SSH host,
  or a Login item marked with the field `credential` = `true`); the choice is remembered per jump host.
  `key: password` in a template does the same.
- A local history of the jump connections (names and addresses only, frecency order, limit 200,
  `jump.history` turns it off, `vaultcrab history clear` deletes it).
- Rename a tab (prefix `R`); the name is kept with the saved tabs.
- Errors of connections through a jump host name the stage: the jump host unreachable, the target
  unreachable from it, the authentication on either, a host key.

### Changed
- The jump picker stays with the host list like the settings and the item form: the other tabs can
  be switched to while it is open (tab 0 shows `0:Hosts ↪`), and it is there again with the step and
  the typed address on the return; prefix `j` on a session tab opens it on the host list.
- `j` in the host list opens the jump picker instead of moving down (arrows move; `ui.keys.jump`
  picks another key).

## [0.4.0] - 2026-11-08

### Added
- Auto-lock (Settings → Security, `unlock:` in the config, applied without a restart): lock the
  vaults after 15 min / 1 h / 8 h (default) / 1 day / 3 days / 5 days without input, or never; time
  asleep counts (the first key after it opens the unlock dialog). Longer than 8 h and never show a
  warning (the unlocked `bw serve` answers any program of the user).
- Lock with the screen (`unlock.lock_on_screen_lock`, off by default): Linux (D-Bus screensaver,
  logind), Windows (session lock), macOS (`com.apple.screenIsLocked`, needs cgo). A sleep by itself
  does not lock.

- Quick unlock with a PIN (Settings → Security → PIN unlock): the master password is kept in the
  macOS Keychain / Windows Credential Manager / Linux Secret Service, encrypted with an argon2id key of
  the PIN; 5 wrong PINs (`pin_attempts`) delete it; the master password is required again after a
  restart and every 14 days; a logout or a changed master password deletes it; change the PIN with
  `p`. The README describes why a PIN is weaker than the master password.
- Touch ID on macOS (Settings → Security → Touch ID, `unlock.touch_id`): asked first, then the PIN,
  then the master password; changed fingerprints delete its data; the README explains why this
  variant (an unsigned app) is weaker than a biometric Keychain item of a signed app. Ignored on Linux
  and Windows. Enter on the empty input (or Ctrl+T) asks for Touch ID again in the unlock dialog; a
  dialog that opened by itself while nobody was at the Mac (auto-lock during a sleep) waits for Enter
  before showing the prompt, and a prompt nobody touched says it timed out.
- `vaultcrab forget` deletes the PIN, the Touch ID data and every stored master password.
- No core dumps on Linux and macOS (and no ptrace by other processes of the user on Linux).

### Changed
- The macOS release binaries are built natively on a macOS runner with cgo (for the screen lock
  detection); Linux and Windows stay static without cgo. The macOS CI job uploads native test
  binaries.
- Separate states and messages: *Offline: server unreachable, local copy* (passwords still work),
  *bw serve stopped, restarting…*, *bw serve unreachable*, *Locked* (with the reason when bw or a
  restart locked it). A connection attempt during a restart waits for it.
- No periodic checks any more: the vault is synced every `bw.sync_interval`, and while the server or
  `bw serve` is unreachable it is checked again after 15 s, 30 s, 1 min, … up to 5 min. The event log
  records why (the crash line of bw, the failed server check).

### Fixed
- After a sleep of a few hours with the VPN still down, the vault no longer asks for the master
  password. bw serve exits when it cannot refresh its expired access token (the first sync after
  the sleep); Vaultcrab now checks that the server is reachable before a sync and skips it while it
  is not, and restarts a crashed `bw serve` with the session key of the old one, so the vault stays
  unlocked. `s` no longer restarts a running `bw serve` (which locked the vault).

## [0.3.0] - 2026-11-02

### Added
- The multi-line paste dialog has an **Edit** button (`e`): the text opens in an editor with line
  numbers; `Ctrl+S` pastes the edited text, `Esc` returns to the preview with the changes.
- Settings → Tab status (`ui.tab_status: color`): instead of the state icon, the tab number is
  coloured (green connected, yellow connecting, red broken, blue new output).
- Each pane header of a split tab has a `✕` button that closes just that pane (a live session asks
  first); `Ctrl+D` closes a broken or finished pane. The status bar and the help mention both.
- Prefix `c` (`clone_tab`) opens a new tab with a new connection to the host of the focused pane.
- Splitting a pane offers the host of the current pane first and selected, so `Enter` clones it
  into the new pane.
- Event log (Settings → Behavior → Event log, `log.enabled`, `log.file`): vault states, unlock
  results, connections, external programs, scripts, config reloads and errors in `vaultcrab.log`
  (rotated at 1 MiB; never passwords, keys typed or terminal output). While it is on, a `≡` button
  in the tab bar and prefix `g` open a viewer.
- Several vaults: further Bitwarden/Vaultwarden vaults (`vaults:` in the config, Settings → Vaults:
  add with `bw config server` + `bw login`, log in, unlock, sync, remove), each with its own bw data
  directory, `bw serve` and cache. Their items appear under a folder named after the vault; the
  host card shows the vault, account and server; the status bar shows each vault's state; the
  unlock dialog names the vault (also with a single vault: account and server).
- `imports/README.md`: how to import sessions of other programs into the vault (Bitwarden JSON,
  `bw import` / `bw export`), with a converter for SecureCRT.
  coloured (green connected, yellow connecting, red broken, blue new output).

### Fixed
- With many tabs the `0:Hosts` tab no longer scrolls out of the tab bar.

## [0.2.0] - 2026-11-02

### Added
- Release archives for Linux arm64 (static), in addition to Linux amd64, Windows amd64 and macOS
  amd64/arm64. Releases ship `.tar.gz`/`.zip` archives with `README.md`, `LICENSE` and
  `THIRD_PARTY_NOTICES.md`, and a `checksums.txt` (SHA-256).

### Changed
- `vaultcrab --version` prints the version without the leading `v` (e.g. `vaultcrab 0.1.0`).
- Dependencies: `github.com/jezek/xgb` (X11 clipboard) 1.1.1 → 1.3.1.

### Fixed
- A config file saved in place by an editor is reloaded only after the write has finished; reading
  it while it was truncated briefly applied an empty configuration.
- Windows: saving settings no longer fails with "Access is denied" while another Vaultcrab (or the
  automatic reload) is reading `config.yaml`; the file is replaced with POSIX rename semantics.

## [0.1.0] - 2026-10-30

### Added
- First public release: host list from Bitwarden/Vaultwarden (`bw serve`), built-in SSH client and
  terminal emulator, tabs and split panes, VNC/SMB/FTP/SFTP/RDP through external clients, filters,
  item editing, scripts, widgets, local shell, settings inside the program.
- On the first start, offer to copy the config directory of the project's former name.

[Unreleased]: https://github.com/sichkarmg/vaultcrab/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.6.0...v0.7.0
[0.6.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.5.0...v0.6.0
[0.5.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.3.0...v0.4.0
[0.3.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sichkarmg/vaultcrab/releases/tag/v0.1.0
