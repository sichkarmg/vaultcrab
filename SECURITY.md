# Security policy

## Supported versions

Vaultcrab is in early development (0.x). Security fixes go into the latest release only.

## Reporting a vulnerability

Please **do not open a public issue** for a vulnerability. Report it privately through GitHub:
**Security → Report a vulnerability** on the repository page
(<https://github.com/sichkarmg/vaultcrab/security/advisories/new>).

Include the affected version (`vaultcrab --version`), the operating system, and the steps to
reproduce. Do not include real passwords, keys or vault contents: use test data.

You will get a reply when the report has been reviewed. Fixed vulnerabilities are disclosed in a
GitHub security advisory and in [CHANGELOG.md](CHANGELOG.md) after a release with the fix is out.

## Scope

In scope: Vaultcrab itself, for example secrets reaching disk, logs, the cache, argv or the
environment; host key checking; the local VNC proxy; the handling of `bw serve`.

Out of scope: vulnerabilities of the Bitwarden CLI, Vaultwarden, external clients (VNC, RDP, SMB,
…) or the operating system; issues that need an already compromised local account. See also the
security notes in the [README](README.md#security-notes).
