# Development notes

## Layout

- `cmd/vaultcrab` — the program; `cmd/termproto`, `tools/` — developer tools
  (`tools/ansi2svg` renders the README screenshots, `tools/sshprobe` debugs SSH handshakes).
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

Linux, Windows and macOS, all with `CGO_ENABLED=0` (the Linux binary is static):

```sh
make build-all    # dist/vaultcrab-<os>-<arch>
make release      # + dist/SHA256SUMS
go test ./...
```

## Integration tests

`internal/vault/integration_test.go` runs against a real `bw serve` of a **test** account when
`VAULTCRAB_IT_BW=1` is set. `scripts/seed-vault.sh` fills a test Vaultwarden account with
`vaultcrab-test-*` items (`--wipe` removes only those).

## Extension points

See [extending.md](extending.md).
