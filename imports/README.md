# Importing hosts from other programs

Vaultcrab reads its host list from your password vault, so moving from another SSH or remote
desktop client means moving its sessions **into the vault**. The usual route:

1. export the sessions of the old program, convert them to the
   [Bitwarden JSON format](https://bitwarden.com/help/condition-bitwarden-import/);
2. import that file with the Bitwarden CLI;
3. delete the file: it contains passwords in plain text.

Vaultcrab then shows the imported items as hosts. The item format it expects is described in the
main [README](../README.md#vault-item-format): a Login item with a URI such as
`ssh://host:22` or `rdp://host` and the user name in `username`.

## Minimal JSON

```json
{
  "encrypted": false,
  "folders": [{"id": "6c4c1f7e-0000-4000-8000-000000000001", "name": "Imported/Lab"}],
  "items": [
    {
      "type": 1,
      "name": "web1",
      "folderId": "6c4c1f7e-0000-4000-8000-000000000001",
      "notes": "imported from another program",
      "login": {
        "username": "admin",
        "password": "change-me",
        "uris": [{"uri": "ssh://192.0.2.10:22"}]
      }
    }
  ]
}
```

Nested folders are written with `/` in the folder name (`Imported/Lab`). Supported URI schemes:
`ssh`, `vnc`, `smb`, `rdp`, `ftp`, `sftp`; items with other schemes (telnet, serial) are imported
into the vault but are not shown as hosts.

## Import and export with the Bitwarden CLI

```sh
bw login                                      # once; bw config server https://… for Vaultwarden
export BW_SESSION="$(bw unlock --raw)"
bw import bitwardenjson sessions.json         # import
shred -u sessions.json                        # Windows: delete the file and empty the recycle bin

bw export --format json --output vault.json   # export (backup); also plain text: keep it safe
```

To import into an organization add `--organizationid <id>` (`bw list organizations`).

## Converters

| Program | Converter | Notes |
|---|---|---|
| SecureCRT | [`securecrt/securecrt_to_bitwarden.py`](securecrt/securecrt_to_bitwarden.py) | sessions, folders, user names, saved passwords |

### SecureCRT

The script reads the session files (`Config/Sessions/**/*.ini`) of SecureCRT, decrypts the saved
passwords (`Password` and `Password V2`, also with a Config Passphrase) and writes a Bitwarden JSON
file: one Login item per session with `ssh://`/`rdp://` URI, port and user name, the SecureCRT
folders as Bitwarden folders under `SecureCRT/`, and the session path, protocol and identity file in
the notes. Requires Python 3 and `pycryptodome` (`pip install pycryptodome`).

```sh
# the Sessions folder is found automatically on Linux, macOS and Windows; -d sets it
python securecrt_to_bitwarden.py -d ~/.vandyke/SecureCRT/Config/Sessions --ask-passphrase -o crt.json
python securecrt_to_bitwarden.py --dry-run          # only list what would be exported

bw login
export BW_SESSION="$(bw unlock --raw)"
bw import bitwardenjson crt.json
shred -u crt.json
```

Options: `--root-folder NAME` (default `SecureCRT`, `''` for none), `--skip-no-password`,
`-p PASSPHRASE` (unsafe: stays in the shell history; prefer `--ask-passphrase`). The output file
is created with mode 0600.

Contributions of converters for other programs are welcome (see [CONTRIBUTING.md](../CONTRIBUTING.md)):
test data only, no real hosts or passwords.
