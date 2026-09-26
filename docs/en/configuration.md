# Configuration reference

tgsync is configured with two files in the **node folder**:

| File | Required | Purpose |
|---|---|---|
| `.env` | yes | Bot token, group, allow-list, limits, optional features |
| `profiles.yaml` | no | Named sets of Claude Code settings for sessions |

Both are read once at start. **Restart the node after any change.**

A commented template of every variable is in [`.env.example`](../../.env.example), and a profile example in [`profiles.example.yaml`](../../profiles.example.yaml).

- [Node folder](#node-folder)
- [Process environment variables](#process-environment-variables)
- [.env reference](#env-reference)
- [Durations](#durations)
- [sudo](#sudo)
- [Voice messages](#voice-messages)
- [profiles.yaml](#profilesyaml)
- [Examples](#examples)

## Node folder

The node folder holds `.env`, `profiles.yaml`, the database (`data/tgsync.db`) and, when sudo is enabled, the socket `askpass.sock`. tgsync changes into this folder at start, so relative paths such as `DB_PATH=./data/tgsync.db` resolve there.

tgsync picks the folder in this order (first match wins):

1. `TGSYNC_HOME`, if set (the folder must exist).
2. The current directory, if it contains `.env`.
3. `~/.config/tgsync`, if it contains `.env`.
4. The OS config folder + `tgsync`, if it contains `.env`: `%APPDATA%\tgsync` on Windows, `~/Library/Application Support/tgsync` on macOS.
5. Otherwise the current directory.

Defaults used by the install scripts:

| OS | Node folder | Binary | Log |
|---|---|---|---|
| Linux | `~/.config/tgsync` | `~/.local/bin/tgsync` | journald (`journalctl --user -u tgsync`) |
| macOS | `~/Library/Application Support/tgsync` | `~/.local/bin/tgsync` | `~/Library/Logs/tgsync.log` |
| Windows | `%APPDATA%\tgsync` | `%LOCALAPPDATA%\tgsync\bin\tgsync.exe` | `%APPDATA%\tgsync\tgsync.log` |
| Docker | `~/.config/tgsync` on the host, mounted as `/config` | inside the image | `docker compose logs` |

Tip: `tgsync check` prints the folder it uses on its first line.

## Process environment variables

These are read from the real process environment, **not** from `.env` (they are needed before `.env` is found):

| Variable | Meaning |
|---|---|
| `TGSYNC_HOME` | Force the node folder. Set by the Windows task and the Docker image. |
| `TGSYNC_LOG` | Append logs to this file instead of stderr. Set by the Windows task, which has no console. |
| `CLAUDE_CONFIG_DIR` | Where Claude Code keeps its settings and transcripts (default `~/.claude`). tgsync reads transcripts from here for `/history`. |

Internal variables you should not set: `TGSYNC_ASKPASS`, `TGSYNC_ASKPASS_SOCK`, `TGSYNC_SUDO_TOKEN`.

The `claude` process and every tool it runs inherit the node's environment, including any extra variables you put in `.env`. tgsync removes `TELEGRAM_BOT_TOKEN` and `SUDO_PASSWORD` from its environment before starting any session, so the agent never sees them.

## .env reference

Syntax: one `NAME=value` per line, `#` starts a comment. Leading and trailing spaces are trimmed. `tgsync check` reports all configuration errors at once.

### Required

| Variable | Format | Description |
|---|---|---|
| `TELEGRAM_BOT_TOKEN` | `123456:ABC-…` | Token of this node's bot from @BotFather. One bot per node: two processes polling one token get `409 Conflict`. |
| `ALLOWED_USER_IDS` | `123456789` or `123456789,987654321` | Telegram user ids that may control the node. Positive integers, comma separated. Everyone else is ignored without a reply. |
| `GROUP_CHAT_ID` | `-1001234567890` | Id of the forum supergroup. Must be negative. All nodes share one group. |
| `PROJECTS_ROOT` | absolute path | Folder with your projects. Every direct subfolder is a project in `/projects` and `/new`. Must be absolute and writable (`/newproject` creates folders here). |

### Node

| Variable | Default | Description |
|---|---|---|
| `BOT_LANGUAGE` | `en` | Interface language: `en` or `ru`. Covers messages, buttons, the command menu and `tgsync check`. After changing it, restart the node and run `tgsync profile` to update the bot's descriptions. |
| `NODE_NAME` | hostname | Name in the control topic title (`🖥 <name>`). Use a different name on each machine. |
| `DB_PATH` | `./data/tgsync.db` | SQLite database. Relative to the node folder. The folder is created with mode `0700`. The database uses WAL, so `-wal` and `-shm` files appear next to it. |
| `CLAUDE_CLI_PATH` | empty (search `PATH`) | Full path to the `claude` executable. Needed when the service's `PATH` does not include it. On Windows only the native `claude.exe` works; npm's `claude.cmd` shim is refused. |

### Limits and timers

| Variable | Default | Description |
|---|---|---|
| `MAX_PARALLEL_SESSIONS` | `3` | Agent turns that run at the same time on this node. More sessions can be open; extra turns wait in a queue. Positive integer. |
| `IDLE_TIMEOUT` | `2h` | Stop an idle `claude` process after this long to free memory. The next message resumes the same session. `0`: never stop. |
| `STALL_WARN` | `20m` | Warn in the topic when a running turn shows no activity for this long (does not count time spent waiting for your answer). `0`: off. |
| `REMIND_EVERY` | `2h` | Remind about an unanswered permission request or question. `0`: off. |
| `MAX_TURN_DURATION` | `0` | Interrupt a turn that runs longer than this. `0`: no limit. |

### Sessions

| Variable | Default | Description |
|---|---|---|
| `AUTO_SEND_GLOBS` | empty | Comma-separated globs. Files changed in a turn that match are sent to the topic when the turn ends. `**/` at the start means "in any folder" (`**/*.md` matches `README.md` and `docs/a.md`); other patterns match the path relative to the project root (`reports/*.pdf`). Documents the agent mentions in its answer are sent anyway. At most 5 files are sent per turn; the rest are reachable through buttons. |
| `DEFAULT_PROFILE` | `full` | Profile used when neither `/new … --profile` nor `projects.<name>.profile` in `profiles.yaml` sets one. Must exist; `tgsync check` verifies it. |
| `SHOW_HOOK_OUTPUT` | `true` | Show hook messages addressed to the user (`systemMessage`) and hook failures in the topic. Accepts `true/false`, `1/0`, `yes/no`. |

### sudo

| Variable | Default | Description |
|---|---|---|
| `SUDO_MODE` | `off` | `off`, `env` or `telegram`. See [sudo](#sudo). Forced to `off` on Windows. |
| `SUDO_PASSWORD` | empty | Password for `SUDO_MODE=env`. Required in that mode. Not trimmed. |

### Voice messages

| Variable | Default | Description |
|---|---|---|
| `STT_URL` | empty (voice off) | Base URL of a self-hosted OpenAI-compatible speech-to-text server, e.g. `http://127.0.0.1:8000`. Must be `http` or `https`. tgsync calls `POST {STT_URL}/v1/audio/transcriptions`. |
| `STT_MODEL` | `Systran/faster-whisper-small` | Model name sent to the server. |
| `STT_TIMEOUT` | `60s` | Limit for downloading plus transcribing one message. `0` removes this limit, but the HTTP request still stops after 2 minutes. |
| `STT_MAX_SECONDS` | `300` | Longest accepted voice message, in seconds. Longer ones are refused with a reply. |

## Durations

`IDLE_TIMEOUT`, `STALL_WARN`, `REMIND_EVERY`, `MAX_TURN_DURATION` and `STT_TIMEOUT` use Go duration syntax: a number with a unit `s`, `m` or `h`, possibly combined: `45s`, `30m`, `2h`, `1h30m`. A bare `0` disables the timer. A number without a unit (other than `0`) or a negative value is an error.

## sudo

The agent's shell has no terminal, so `sudo` cannot ask for a password on its own. tgsync provides an askpass bridge (Linux and macOS only):

1. The agent wants to run a command containing `sudo`. A `🔐 sudo` request with ✅/❌ appears in the topic. There is no "Always" button for sudo.
2. After ✅, each real `sudo` call in the command (found by a shell parser) gets a one-time token valid for 5 minutes and until the end of the turn.
3. `sudo` runs `tgsync` as `SUDO_ASKPASS`, which asks the node over the unix socket `askpass.sock` (mode `0600`). The node answers only for a valid token and only to a process started by a real setuid `sudo`.

| Mode | Where the password comes from | Trade-off |
|---|---|---|
| `off` | nowhere: sudo commands are rejected | Safest. Recommended unless you really need it. |
| `env` | `SUDO_PASSWORD` in `.env` | Password stored in plain text on disk (keep `.env` at `0600`). The agent runs as your user and could technically read it; tgsync blocks this by rules, not by OS permissions. |
| `telegram` | you type it in the topic after ✅; the bot deletes your message at once | Password travels through Telegram servers (Bot API is not end-to-end encrypted) and is visible to other group members until deleted. Needs the bot's "Delete messages" right; `tgsync check` fails without it. Asked for every command; up to 3 attempts per call. A wrong password counts as a failed login (`pam_faillock` may lock the account). |

Safer than any mode: a `NOPASSWD` rule in `/etc/sudoers.d/` for only the commands you need, for example:

```
user ALL=(root) NOPASSWD: /usr/bin/systemctl restart myapp
```

Never put `SUDO_PASSWORD` into a systemd `Environment=` line or a compose `environment:` block: from there it is visible in `/proc/<pid>/environ`. Keep it in `.env`, which tgsync loads and then removes from its environment.

The approval mode (🟢 everything / 🟡 everything except sudo / 🔴 ask for every command) is not an `.env` setting: it is chosen in Telegram (`/approve`) and stored in the database. The default is 🔴 ask.

## Voice messages

1. Start the bundled self-hosted server (CPU image of [speaches](https://github.com/speaches-ai/speaches)):
   ```bash
   docker compose -f deploy/stt/compose.yaml up -d
   ```
   It listens on `127.0.0.1:8000` only.
2. Download the model once (about 480 MB, kept in a Docker volume):
   ```bash
   curl -X POST http://127.0.0.1:8000/v1/models/Systran/faster-whisper-small
   ```
3. Set in `.env`:
   ```
   STT_URL=http://127.0.0.1:8000
   ```
4. Restart the node.

The `small` model on a CPU transcribes a minute of audio in 10–20 seconds. On a machine with a GPU, run the same server with the `latest-cuda` image and a larger model, then change `STT_URL` and `STT_MODEL`. Audio never goes to a cloud service; if the STT server runs on another machine, put it behind a VPN or TLS.

Voice messages and audio files (up to Telegram's 20 MB bot download limit) are transcribed and sent to the agent as text.

## profiles.yaml

`profiles.yaml` lives next to `.env`. Without it, only the built-in `full` profile exists.

```yaml
profiles:
  <name>:
    setting_sources: [user, project, local]   # optional
    env:                                       # optional
      NAME: "value"
    settings:                                  # optional
      <any Claude Code settings.json keys>
projects:
  <project folder name>:
    profile: <name>
```

| Key | Type | Meaning |
|---|---|---|
| `profiles.<name>.setting_sources` | list of `user`, `project`, `local` | Which Claude Code settings files are loaded: `user` = `~/.claude/settings.json` (and user plugins), `project` = `.claude/settings.json`, `local` = `.claude/settings.local.json`. Omitted or empty = all three. |
| `profiles.<name>.env` | map of strings | Extra environment variables for the session's `claude` process. Quote values such as `"off"` or `"1"` so YAML keeps them as strings. |
| `profiles.<name>.settings` | map | Settings layered on top, passed as `claude --settings '<json>'`. Any key of Claude Code's `settings.json` works, e.g. `enabledPlugins`, `permissions`, `model`. |
| `projects.<folder>.profile` | string | Default profile for that project (folder name under `PROJECTS_ROOT`). |

Choice order for a new session:

1. `/new <project> <task> --profile <name>`
2. `projects.<project>.profile`
3. `DEFAULT_PROFILE` from `.env` (default `full`)

`full` always exists and means "everything from `~/.claude`, like a terminal session". You may define your own `full` to override it. An unknown profile name is an error; `/profiles` in Telegram lists what is loaded.

## Examples

Minimal `.env`:

```
TELEGRAM_BOT_TOKEN=123456:ABC-replace-with-your-token
ALLOWED_USER_IDS=123456789
GROUP_CHAT_ID=-1001234567890
PROJECTS_ROOT=/home/user/projects
```

A second node in the same group (another machine):

```
TELEGRAM_BOT_TOKEN=654321:XYZ-token-of-the-second-bot
ALLOWED_USER_IDS=123456789
GROUP_CHAT_ID=-1001234567890
PROJECTS_ROOT=/home/user/code
NODE_NAME=desktop
```

A busy build server with voice input and strict limits:

```
MAX_PARALLEL_SESSIONS=6
IDLE_TIMEOUT=30m
STALL_WARN=10m
MAX_TURN_DURATION=3h
AUTO_SEND_GLOBS=**/*.md,**/*.pdf,reports/*.html
STT_URL=http://127.0.0.1:8000
```

`profiles.yaml` that disables one plugin in Telegram sessions and keeps a sandbox project on project settings only:

```yaml
profiles:
  quiet:
    settings:
      enabledPlugins:
        "example-plugin@example-marketplace": false
  project-only:
    setting_sources: [project, local]
projects:
  sandbox:
    profile: project-only
```
