# Development notes

## Layout

- `cmd/vaultcrab` — the program; `cmd/termproto`, `tools/` — developer tools
  (`tools/ansi2svg` renders the README screenshots: `scripts/screenshots.sh` makes the scenes of
  `internal/tui/screenshots_test.go` with a demo vault and renders them; `tools/sshprobe` debugs SSH handshakes).
- `internal/core` — application logic; `internal/tui` — the Bubble Tea interface.
- `internal/vault` — the Bitwarden/Vaultwarden provider (`bw serve` client).
- `internal/connect`, `internal/session`, `internal/term` — SSH, VNC, external clients, terminal.
- `third_party/` — patched forks of Bubble Tea, Ultraviolet and x/vt (see `PATCHES.md` in each).

## Rules

1. Logic is separate from the interface: packages in `internal/` (except `internal/tui`) do not
   import Bubble Tea or lipgloss; `internal/tui` talks to the rest only through `internal/core`
   (checked by `internal/archtest`).
2. Keep secrets in memory as briefly as possible: never log them, never put them into the cache,
   the config or scrollback dumps, never pass them through argv or the environment. The one
   exception is a `{password}` placeholder the user writes into a command line themselves; the UI
   warns about it.
3. Use test credentials only. Never commit real passwords, keys or host names; use `example.com`
   and documentation addresses (`192.0.2.0/24`, `198.51.100.0/24`).

## Build targets

Linux and Windows with `CGO_ENABLED=0` (the Linux binary is static). macOS with cgo when built on a
Mac (the screen lock detection of `internal/screenlock` uses Foundation); cross-built macOS binaries
have no cgo and work without that detection. The release job runs on macOS for that reason, and the
macOS CI job uploads native test binaries (artifact `vaultcrab-macos`).

```sh
make fmt-check vet test   # gofmt, go vet (also with -tags integration), go test ./...
make build-all            # dist/vaultcrab-<os>-<arch>
make release-check        # before a release tag, see RELEASING.md
```

CI (`.github/workflows/ci.yml`, on pull requests and pushes to `main`) runs gofmt, vet, tests
and `make build` on Linux, macOS and Windows, plus gitleaks (`.gitleaks.toml`) and govulncheck;
its summary status is `ci`.

## Integration tests

Integration tests have the build tag `integration` and are not part of `go test ./...`.
`internal/vault/integration_test.go` runs against a real `bw serve` of a **test** account: bw on
`PATH` (or `VAULTCRAB_IT_BW_PATH`), logged in, `BW_SESSION` set:

```sh
go test -tags integration -run '^TestIntegration' ./internal/vault
```

`scripts/seed-vault.sh` fills a test Vaultwarden account with `vaultcrab-test-*` items (`--wipe`
removes only those).

`scripts/e2e.sh` (`make e2e`) builds vaultcrab and the integration test binaries for Linux and
Windows, copies them to the test VMs over ssh, runs `vaultcrab --version`, a short TUI smoke start
and the integration tests there, and prints a summary. The VMs and the test account are configured
with environment variables (see the header of the script and `scripts/lib/vm.sh`).
`scripts/release-verify.sh` checks a draft release the same way (see [RELEASING.md](../RELEASING.md)).

## Extension points

See [extending.md](extending.md).
