# Contributing

Thanks for your interest in Vaultcrab!

## How changes get in

- **External pull requests are reviewed and merged by the repository owner**. Automated
  maintenance in this repository does not merge external PRs.
- **Issues first** for larger changes: describe the problem or the feature before writing code.
  Use the issue templates (bug report, feature request).
- **Small, focused pull requests.** One topic per PR, with a short description of what and why.
  Branch names: `feat/<topic>`, `fix/<topic>`, `chore/<topic>`; commit messages follow
  [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `chore:`,
  `refactor:`, `test:`). PRs are squash-merged.
- **Checks must pass:** the `ci` workflow runs `gofmt`, `go vet`, `go test ./...` and `make build`
  on Linux, macOS and Windows, plus gitleaks and govulncheck. Locally:
  `make fmt-check vet test build-all`.
- **Tests** for new behaviour, especially anything that handles secrets. Integration tests against a
  real `bw serve` use the build tag `integration` (see [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)).
- **Docs:** update `README.md` and `README.ru.md` for user-facing changes,
  `docs/config.example.yaml` for config changes, and the `Unreleased` section of
  [CHANGELOG.md](CHANGELOG.md).
- **Keep the rules** from [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md): no secrets in logs, cache,
  config, argv or environment; the UI talks to the logic only through `internal/core`; no timers or
  redraws while idle.
- **No real data:** never include real passwords, keys, host names, IP addresses or e-mail
  addresses in code, tests, screenshots or logs. Use `example.com` and `192.0.2.0/24`.
- **Dependencies:** permissive licenses only (MIT, BSD, ISC, Apache-2.0; MPL-2.0 as separate
  files); no GPL/AGPL code in the binary. Update [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
- New Go files start with `// SPDX-License-Identifier: MIT`.

## License of contributions

Vaultcrab is licensed under the [MIT License](LICENSE). By submitting a contribution you agree
that it is licensed under the same MIT License. The name and logos are not part of that license
(see [TRADEMARKS.md](TRADEMARKS.md)).

## Security issues

Please do not open public issues for vulnerabilities. See [SECURITY.md](SECURITY.md).
