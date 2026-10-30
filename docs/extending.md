# Extending Vaultcrab

Vaultcrab has two extension points: **vault providers** (where hosts and
credentials come from) and **protocols** (how a host is opened).

## Vault providers

A provider implements `core.Source` (`internal/core/core.go`):

| Method     | Purpose |
|------------|---------|
| `Status`   | Is the store reachable, logged in, locked? |
| `Unlock`   | Unlock with the master password (if the store has one). |
| `Sync`     | Refresh from the server (no-op for local stores). |
| `Snapshot` | Host metadata only: names, addresses, folders, tags. **No secrets.** |
| `Secret`   | The credential of one item, fetched on each connection attempt. |

The service obtains the provider through `core.Options.Connect`
(`cmd/vaultcrab/main.go`). Rules every provider must follow:

- secrets are never written to the cache, the config, logs or scrollback;
- secrets are not passed to other programs through argv or the environment
  (the only exception is a `{password}` placeholder the user writes into a
  command line themselves);
- `Snapshot` must be cheap to cache: Vaultcrab keeps it on disk for the
  offline mode.

Implemented: **Bitwarden / Vaultwarden** (`internal/vault`, talks to
`bw serve` on 127.0.0.1). Planned: **gopass**.

## Protocols

1. Add a `model.Scheme` constant and list it in `model.Schemes`
   (`internal/model/model.go`). The scheme is detected from the item's URI
   (`<scheme>://host:port`) or its custom fields.
2. Decide how it opens:
   - **in a tab** (built-in client, like SSH, or a proxied external client,
     like VNC): handle it in `Service.InTab` / `OpenSession`
     (`internal/core/sessions.go`) and add a dialer in `internal/connect`;
   - **as an external program** (like SMB, FTP, SFTP, RDP): add well-known
     clients per OS to `connect.DefaultFallbacks` and presets to
     `internal/launchers`. Users can always point a scheme at any program in
     `config.yaml` (`external:`), with `{host}`, `{port}`, `{user}`,
     `{url}`, `{path}`, `{name}` and, explicitly, `{password}` placeholders.
3. Add an icon (and its ASCII variant) in `internal/config/icons.go`.

Custom actions that are not a protocol (ping, port checks, your own tools)
are better done as **scripts** (`scripts:` in `config.yaml`, see
`docs/config.example.yaml`).
