# Contributing

Thanks for your interest in Vaultcrab!

- **Issues first** for larger changes: describe the problem or the feature before writing code.
- **Small, focused pull requests.** One topic per PR, with a short description of what and why.
- **Checks must pass:** `gofmt`, `go vet ./...`, `go test ./...`, `make build-all`.
- **Tests** for new behaviour, especially anything that handles secrets.
- **Keep the rules** from [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): no secrets in logs, cache,
  config, argv or environment; the UI talks to the logic only through `internal/core`.
- **No real data:** never include real passwords, keys, host names, IP addresses or e-mail
  addresses in code, tests, screenshots or logs. Use `example.com` and `192.0.2.0/24`.
- New Go files start with `// SPDX-License-Identifier: MIT`.

## License of contributions

Vaultcrab is licensed under the [MIT License](LICENSE). By submitting a contribution you agree
that it is licensed under the same MIT License. The name and logos are not part of that license
(see [TRADEMARKS.md](TRADEMARKS.md)).

## Security issues

Please do not open public issues for vulnerabilities. Use GitHub's private vulnerability
reporting ("Report a vulnerability" on the Security tab of the repository).
