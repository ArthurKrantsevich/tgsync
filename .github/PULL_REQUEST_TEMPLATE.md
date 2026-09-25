## What and why

<!-- What does this change do, and why is it needed? Link the issue: "Fixes #123". -->

## How it was tested

<!-- Tests added or updated, and any manual check in a real Telegram group. -->

## Checklist

- [ ] `make test` passes (tests run with `-race`)
- [ ] `gofmt -l .` prints nothing and `go vet ./...` is clean for linux, darwin and windows
- [ ] New behaviour has tests; a bug fix has a test that fails without it
- [ ] Code that touches paths, processes or sockets keeps Linux, macOS and Windows working
- [ ] Docs updated in **both** `docs/en` and `docs/ru` (and README / CHANGELOG if user-visible)
- [ ] Commits follow `type(scope): subject` (see CONTRIBUTING.md)
- [ ] No tokens, real ids, passwords or personal paths in code, tests, docs or screenshots

## Security impact

<!-- Does this change what runs without a tap, what the agent can read, or what is sent to Telegram? If yes, explain. Otherwise write "None". -->
