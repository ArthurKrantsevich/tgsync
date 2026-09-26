**English** · [Русский](README.ru.md)

# tgsync

Run Claude Code agent sessions on your own machines and drive them from Telegram. Give the agent a task from your phone, watch it work, answer its questions, approve commands, review the diff and commit — while the work itself happens on your computer, with your plugins, skills and hooks.

Each machine running tgsync is a **node** with its own bot. All nodes share one Telegram forum group:

- every node has a control topic `🖥 <node name>` with a pinned status card;
- every agent session gets its own topic.

> The bot's interface is currently in Russian. The documentation is available in English and Russian.

## Why

Claude Code works best on a real machine with your repositories, toolchains and credentials. Long tasks, though, do not need you at the keyboard. tgsync keeps the agent where your code lives and moves only the conversation to Telegram, so you can start, steer and review work from anywhere, and every risky action still waits for your tap.

## Features

**Sessions**
- New projects and sessions from Telegram, one topic per session; resume after a crash or restart.
- Live status of each turn: elapsed time, step count, current action, latest remark.
- Message queue with a 👀 reaction and a «send now» button; limits on parallel sessions and one turn per project.
- Stop a turn, switch Claude Code mode (`default`, `acceptEdits`, `plan`), close a session.

**Permissions**
- Permission requests as buttons: allow once, deny, or «always» with deliberately narrow rules.
- Node-wide approve modes: ask for everything, approve all but sudo, or approve all.
- The agent's `AskUserQuestion` prompts as buttons, including multiple choice and free-text answers.
- `sudo` through Telegram: per-command approval with the password from `.env` or typed in the chat and deleted right away.

**Turn results**
- Turn summary with changed files and `+/-` line stats.
- Whole-turn diff as a file, commit via the agent, confirmed rollback to the state before the turn.
- Files both ways: the agent sends documents it wrote; you send files and screenshots for it to read.
- `/ls` file browser and `/file` to fetch any project file.

**Insight**
- Subagents panel: who is running, on what, who started them; stop an agent or fetch its full result.
- Context fill with a one-tap compact button; per-session and per-node token usage; subscription limit alerts.
- `/history`: continue any Claude Code session from the terminal or desktop app, forking it if it is still active.

**More**
- Voice messages via a self-hosted speech-to-text server; the agent restates the task and waits for your «yes».
- Plugin profiles per project or per session.
- Tidy group: pinned node card, auto-cleaned control topic, confirmed cleanup of old topics, per-node topic colours.
- Linux (systemd), macOS (launchd) and Windows (Task Scheduler); Docker optional.

## How it works

```mermaid
flowchart LR
    you["You<br/>(Telegram app)"] <--> tg["Telegram<br/>forum group"]
    tg <--> nodeA["tgsync node A<br/>(bot A)"]
    tg <--> nodeB["tgsync node B<br/>(bot B)"]
    nodeA <--> claudeA["claude CLI<br/>(Agent SDK)"]
    claudeA <--> projA["projects in<br/>PROJECTS_ROOT"]
    nodeA --- dbA[("SQLite")]
    nodeA -. voice, optional .-> stt["self-hosted STT"]
```

A node is a single Go binary. It long-polls the Telegram Bot API for its bot, starts one `claude` process per active session through a Go client for the Claude Agent SDK protocol, answers the agent's permission callbacks with Telegram buttons, and keeps its state (sessions, rules, usage) in a local SQLite database. Nodes do not talk to each other; they only share the group.

## Requirements

- Linux, macOS or Windows 10/11.
- [Claude Code](https://docs.claude.com/en/docs/claude-code) installed and logged in (`claude` on `PATH`). On Windows the native `claude.exe` and Git for Windows are required.
- Go (the version in `go.mod`) to build from source.
- A Telegram group with Topics enabled and one bot per node.

## Quick start

1. Install and log in to Claude Code on the machine.
2. Create a bot with [@BotFather](https://t.me/BotFather) and copy its token.
3. Create a Telegram group, enable **Topics**, add the bot as an admin with the "Manage topics" right (also "Delete messages" and "Pin messages" for the full experience).
4. Clone the repository and fill in `.env`: bot token, your user ID, the group ID (starts with `-100`, e.g. `-1001234567890`), `PROJECTS_ROOT`.
   ```bash
   cp .env.example .env
   ```
5. Check the setup: `make check`.
6. Install the service: `make install` (moves `.env` into the node folder, `~/.config/tgsync` on Linux). On Windows use `scripts/install.ps1`.
7. Open the `🖥 <NODE_NAME>` topic in the group, send `/menu`, then `/new myproject your task`.

Full instructions, including macOS, Windows, Docker and the voice server: [docs/en/setup.md](docs/en/setup.md).

## Releases

Pushing a `v*` tag builds archives with GoReleaser for Linux, macOS and Windows (amd64 and arm64) and attaches them to a draft GitHub release, which is published by hand. No container images are published; the `Dockerfile` builds one locally. Building from source is always an option.

## Documentation

| | English | Русский |
|---|---|---|
| Setup and maintenance | [setup](docs/en/setup.md) | [установка](docs/ru/setup.md) |
| Configuration | [configuration](docs/en/configuration.md) | [настройка](docs/ru/configuration.md) |
| User guide | [usage](docs/en/usage.md) | [как пользоваться](docs/ru/usage.md) |
| Security | [security](docs/en/security.md) | [безопасность](docs/ru/security.md) |
| Design spec | [spec](docs/en/spec.md) | [спецификация](docs/ru/spec.md) |
| Troubleshooting | [troubleshooting](docs/en/troubleshooting.md) | [решение проблем](docs/ru/troubleshooting.md) |

Also: [CHANGELOG](CHANGELOG.md) · [CONTRIBUTING](CONTRIBUTING.md) · [SECURITY](SECURITY.md)

## Security

tgsync lets whoever controls the bot run an agent as your OS user. Only IDs in `ALLOWED_USER_IDS` can control a node, and everyone in the group sees the agent's output, so keep the group private. By default every risky action waits for a tap; the «all» approve modes remove that safeguard. Messages pass through Telegram's servers and are not end-to-end encrypted. Apart from Telegram and Claude Code itself, tgsync calls no third-party services; voice recognition runs on your own server. Read [docs/en/security.md](docs/en/security.md) before enabling auto-approve or sudo, and report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE) © 2026 Arthur Krantsevich
