# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/sichkarmg/vaultcrab/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/sichkarmg/vaultcrab/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/sichkarmg/vaultcrab/releases/tag/v0.1.0
