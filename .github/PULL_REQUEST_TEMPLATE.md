## What and why

<!-- Short description of the change and the reason for it. Link issues: "Fixes #123". -->

## Decisions

<!-- Choices made along the way and alternatives considered (if any). -->

## Checklist

- [ ] Conventional commit title (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`)
- [ ] Tests added or updated; `gofmt`, `go vet ./...`, `go test ./...` pass
- [ ] `scripts/e2e.sh` run (only for connections, vault or platform code): result:
- [ ] `README.md` / `README.ru.md`, `docs/config.example.yaml`, `CHANGELOG.md` (Unreleased) updated if needed
- [ ] No timers or redraws while idle (idle wake-up test stays green)
- [ ] No real credentials, host names, e-mail addresses or personal data
- [ ] New dependencies: permissive license, `THIRD_PARTY_NOTICES.md` updated

## Manual verification on macOS

<!-- What can only be checked by hand on macOS: interactive TUI, mouse, external client windows.
     Write "none" if nothing. -->
