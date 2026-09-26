# Contributing

Thanks for your interest in tgsync. Bug reports, fixes and focused improvements are welcome.

## Before you start

- For anything larger than a small fix, open an issue first and describe the problem and the proposed change.
- Security issues go through private reporting, not public issues: see [SECURITY.md](SECURITY.md).

## Build

You need Go (the version in `go.mod`) and git. Running a node also needs Claude Code; see [docs/en/setup.md](docs/en/setup.md).

```bash
make build            # or: go build -o bin/ ./cmd/tgsync
./bin/tgsync version
```

Cross-compile for other platforms with `GOOS`/`GOARCH` (the build uses no cgo):

```bash
GOOS=windows GOARCH=amd64 go build -o bin/tgsync.exe ./cmd/tgsync
GOOS=darwin  GOARCH=arm64 go build -o bin/tgsync-darwin ./cmd/tgsync
```

## Test

```bash
make test             # go test -race ./...
```

- Tests must pass with `-race`.
- New behaviour needs tests; a bug fix needs a test that fails without it.
- Tests use the fake Telegram API and fake agent in `internal/telegram` and `internal/agent`; they must not need network access, a bot token or a real `claude`.
- `make it` runs integration checks against a real, logged-in `claude` CLI. It is optional and not part of CI.

## Formatting and vet

CI runs these on every push and pull request; run them locally first:

```bash
gofmt -l .                                        # must print nothing
go vet ./...
for os in linux darwin windows; do GOOS=$os go vet ./...; done
sh -n scripts/install.sh && sh -n scripts/uninstall.sh
```

CI also runs the tests on Linux, macOS and Windows. Code that touches paths, processes or sockets should keep all three working: use the helpers in `internal/files` for path containment and put OS-specific code in `_unix.go`, `_linux.go`, `_darwin.go` or `_windows.go`/`_other.go` files.

## Commits

Use `type(scope): subject`, in English, imperative mood, no trailing period:

```
fix(permissions): keep «Всегда» as narrow as what was approved
feat(session): «send now» button for queued messages
docs: macOS and Windows setup
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `ci`. The scope is usually the package (`session`, `router`, `permissions`, `files`, `group`, `sudo`, …). Keep one logical change per commit, and explain *why* in the body when it is not obvious.

## Pull requests

- Keep a pull request focused on one change; describe what it changes and how you tested it.
- Update the documentation in **both** `docs/en` and `docs/ru` when behaviour, commands, buttons or settings change, and add an entry to both `CHANGELOG.md` and `CHANGELOG.ru.md`.
- User-facing strings live in `internal/i18n`: add every new string there in both English and Russian and use `i18n.T` / `i18n.N` in code. `go test ./internal/i18n/` checks that both languages are filled in and use the same format verbs. Code comments and commit messages are in English.
- Anything that widens what the agent may do without a tap (permission rules, approve modes, protected paths, sudo) needs a clear rationale and tests for the refused cases.
- Never commit real bot tokens, user or chat IDs, passwords or personal paths. Use placeholders such as `-1001234567890`, `123456789` and `/home/user`.
- tgsync must not call third-party AI or ML cloud services; model inference (for example speech recognition) runs on self-hosted endpoints only.

By contributing you agree that your contributions are licensed under the [MIT License](LICENSE).
