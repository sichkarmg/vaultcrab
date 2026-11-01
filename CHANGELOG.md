# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- Release archives for Linux arm64 (static), in addition to Linux amd64, Windows amd64 and macOS
  amd64/arm64. Releases ship `.tar.gz`/`.zip` archives with `README.md`, `LICENSE` and
  `THIRD_PARTY_NOTICES.md`, and a `checksums.txt` (SHA-256).

### Changed
- `vaultcrab --version` prints the version without the leading `v` (e.g. `vaultcrab 0.1.0`).

## [0.1.0] - 2026-10-30

### Added
- First public release: host list from Bitwarden/Vaultwarden (`bw serve`), built-in SSH client and
  terminal emulator, tabs and split panes, VNC/SMB/FTP/SFTP/RDP through external clients, filters,
  item editing, scripts, widgets, local shell, settings inside the program.
- On the first start, offer to copy the config directory of the project's former name.

[Unreleased]: https://github.com/sichkarmg/vaultcrab/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/sichkarmg/vaultcrab/releases/tag/v0.1.0
