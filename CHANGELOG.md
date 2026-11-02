# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- The multi-line paste dialog has an **Edit** button (`e`): the text opens in an editor with line
  numbers; `Ctrl+S` pastes the edited text, `Esc` returns to the preview with the changes.
- Event log (Settings → Behavior → Event log, `log.enabled`, `log.file`): vault states, unlock
  results, connections, external programs, scripts, config reloads and errors in `vaultcrab.log`
  (rotated at 1 MiB; never passwords, keys typed or terminal output). While it is on, a `≡` button
  in the tab bar and prefix `g` open a viewer.

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
