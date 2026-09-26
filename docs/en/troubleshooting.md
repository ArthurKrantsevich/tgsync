# Troubleshooting

Start here, every time:

1. Run `tgsync check` (Windows: `& $env:LOCALAPPDATA\tgsync\bin\tgsync.exe check`). Each `✗` line names the broken step.
2. Read the last log lines:
   - Linux: `journalctl --user -u tgsync -n 100`
   - macOS: `tail -n 100 ~/Library/Logs/tgsync.log`
   - Windows: `Get-Content -Tail 100 $env:APPDATA\tgsync\tgsync.log`
   - Docker: `docker compose logs --tail 100`

The quoted strings below are what you will see with the English interface (`BOT_LANGUAGE=en`, the default). With `BOT_LANGUAGE=ru` the same messages come in Russian: see the [Russian version](../ru/troubleshooting.md) of this page.

- [Configuration errors](#configuration-errors)
- [Telegram](#telegram)
- [Bot is silent](#bot-is-silent)
- [Claude Code](#claude-code)
- [Service does not start](#service-does-not-start)
- [Permissions and sudo](#permissions-and-sudo)
- [Voice messages](#voice-messages)
- [Files](#files)
- [Docker](#docker)

## Configuration errors

`tgsync check` prints `✗ config — …`, and `tgsync run` exits. All problems are listed at once.

| Message | Cause | Fix |
|---|---|---|
| `TELEGRAM_BOT_TOKEN is required` (also `ALLOWED_USER_IDS`, `GROUP_CHAT_ID`, `PROJECTS_ROOT`) | Variable missing or empty, **or `.env` was not found at all** | Check the folder on the first line of `tgsync check` (`folder …`). Put `.env` there, or set `TGSYNC_HOME`. See [node folder](configuration.md#node-folder). |
| `GROUP_CHAT_ID must be a negative supergroup id like -1001234567890` | Id is positive, has spaces or letters, or is a user id | Use the group id with the minus sign ([setup 2.5](setup.md#25-find-the-group-id)). |
| `ALLOWED_USER_IDS: invalid user id "…"` | Not a positive number (e.g. `@username`) | Use numeric ids from @userinfobot, comma separated. |
| `PROJECTS_ROOT must be an absolute path` | `~/projects` or a relative path | Write the full path: `/home/user/projects`. `~` is not expanded. |
| `MAX_PARALLEL_SESSIONS must be a positive integer` | `0`, negative, or text | Use `1` or more. |
| `IDLE_TIMEOUT must be a duration like 30m or 2h, got "…"` — also for `STALL_WARN`, `REMIND_EVERY`, `MAX_TURN_DURATION`, `STT_TIMEOUT` | Number without a unit (`120`), or negative | Add a unit: `120m`, `2h`. `0` alone disables. |
| `SUDO_MODE must be off, env or telegram` | Typo | Use one of the three. |
| `SUDO_MODE=env requires SUDO_PASSWORD` | Empty password in `env` mode | Set `SUDO_PASSWORD`, or use another mode. |
| `SHOW_HOOK_OUTPUT must be true or false` | Other value | `true`/`false`. |
| `STT_URL must be an http(s) URL like http://127.0.0.1:8000` | Missing scheme (`127.0.0.1:8000`) | Add `http://`. |
| `STT_MAX_SECONDS must be a positive integer` | `0`, negative, text | Use a positive number of seconds. |
| `TGSYNC_HOME=…: directory does not exist` | `TGSYNC_HOME` points to a missing folder | Create it or fix the path. |
| `.env: …` | Syntax error in `.env` (e.g. unbalanced quotes) | Keep lines as `NAME=value`, no quotes needed. |
| `✗ profiles — profile "…" not found in profiles.yaml` | `DEFAULT_PROFILE` or `projects.*.profile` names a missing profile | Define it in `profiles.yaml` or use `full`. |
| `✗ profiles — profiles.yaml: yaml: …` | YAML syntax error | Fix indentation; use spaces, not tabs. |

## Telegram

| Symptom | Cause | Fix |
|---|---|---|
| `✗ bot — token rejected or Telegram unreachable`, log shows `Unauthorized` | Wrong or revoked token, extra spaces or quotes | Copy the token again from @BotFather (`/token`). |
| Same message with a network error (`dial tcp`, `timeout`) | No access to `api.telegram.org` (firewall, proxy, DNS) | `curl -sI https://api.telegram.org` must work from this machine. Standard `HTTPS_PROXY` is honored. |
| `✗ group with topics — group … is unreachable: the bot is not added or GROUP_CHAT_ID is wrong`, log `chat not found` | Bot is not a member, or the id is wrong/outdated | Add the bot to the group. Re-read the id **after** enabling Topics: converting to a supergroup changes it. |
| `✗ group with topics — topics are not enabled in the group` | Topics are off | Group settings → Topics → on. Then re-check the id. |
| `✗ bot rights — make the bot an administrator with the “Manage topics” right`; `run` exits with `the bot must be a group administrator with the “Manage topics” right (can_manage_topics)` | Bot is a plain member, or the right is off | Administrators → your bot → enable **Manage topics**. |
| `✓ bot rights — can manage topics; consider adding …` and a `🔧` notice in the control topic | Optional rights missing | Add Pin messages / Change group info / Delete messages ([setup 2.3](setup.md#23-make-the-bot-an-administrator)). |
| No pinned card | No **Pin messages** right | Add it and restart. |
| Empty topics are closed, not deleted; 🧹 Clean up only closes | No **Delete messages** right | Add it. |
| `409 Conflict: terminated by other getUpdates request` in logs | Two processes use the same token: service + `./bin/tgsync run`, service + Docker, two machines, or `getUpdates` via curl | Stop the extra one. `pgrep -a tgsync` lists local processes. Each machine needs its own bot. |
| `429 Too Many Requests` / `retry after N` in logs, messages arrive late | Telegram rate limit (many edits or sessions at once) | Nothing to do: tgsync waits and retries (up to 5 minutes per call; status edits are skipped sooner). Lower `MAX_PARALLEL_SESSIONS` if it happens constantly. |
| `control topic: …` at start | Could not create or rename the control topic | Usually rights or Topics disabled; run `tgsync check`. |
| The General topic disappeared | tgsync hides it once on first start | Group → topic list → show General. It is not hidden again. |
| Can't find the control topic, or it was deleted | Many topics, or the topic was removed | Send `/control` in General: every node replies with a link to its control topic and recreates it if needed. |

## Bot is silent

Messages in the group get no reply at all.

1. **Your id is not allowed.** Messages from users outside `ALLOWED_USER_IDS` are ignored without any reply or log line. Compare your id from @userinfobot with `.env`.
2. **Node is not running.** Check the service status (setup 8.1) and the log.
3. **Wrong topic.** Each node answers only in its own topics: the control topic `🖥 <NODE_NAME>` and its session topics. In another node's topic, or in General (except `/control`), your node stays quiet.
4. **Wrong group.** The node ignores every chat except `GROUP_CHAT_ID`. If you created a new group, update the id.
5. **Messages from bots are ignored**, including other nodes' bots and messages sent "as the group" (anonymous admin). Turn off "Remain anonymous" in your own admin rights.
6. **Another process holds the token** (`409 Conflict` in logs): an old node elsewhere gets the updates instead.
7. **Plain text in the control topic** is not sent to any agent: use `/menu` or `/new <project> <task>`. Text reaches the agent only in session topics.
8. **Telegram is unreachable** from the machine: the log shows network errors; the node retries.

## Claude Code

| Symptom | Cause | Fix |
|---|---|---|
| `✗ claude CLI — claude not found in PATH; install Claude Code or set CLAUDE_CLI_PATH` | Not installed, or not on the `PATH` of the shell or service | Install Claude Code. Then either re-run `make install` from a shell where `claude --version` works (the unit stores that `PATH`), or set `CLAUDE_CLI_PATH=/home/user/.local/bin/claude`. |
| Works in the terminal, fails as a service | Service `PATH` was captured before you installed `claude`, `node` or switched Node versions (nvm, asdf) | Re-run `make install` from the current shell, or set `CLAUDE_CLI_PATH`. |
| Windows: `found claude.cmd (the npm wrapper), it does not start` | Claude Code installed via npm | Install with the native installer (`irm https://claude.ai/install.ps1 \| iex`) or set `CLAUDE_CLI_PATH` to a `claude.exe`. |
| Windows: `claude.exe not found in PATH` | Not installed or not on `PATH` of your logon session | Install, log off and on, run the installer again. |
| `✗ claude CLI — … --version: …` | Binary exists but fails to run | Run `claude --version` yourself and fix what it reports. |
| Session topic says the agent is not logged in / `Invalid API key` / `Please run /login` | Claude Code is not logged in for the user the service runs as | Run `claude` as that user and `/login`, or use `claude setup-token` and add `CLAUDE_CODE_OAUTH_TOKEN=…` to `.env`. Restart the node. |
| `❌ The claude process exited during a turn` | `claude` crashed or was killed (OOM, update in progress) | Send another message to continue. Check memory; lower `MAX_PARALLEL_SESSIONS`. |
| `⏸ The node restarted during a turn` | Service restart or upgrade | Send a message to continue. |
| Idle sessions take a few seconds to answer | `IDLE_TIMEOUT` stopped the `claude` process; it is resumed on the next message | Expected. Increase `IDLE_TIMEOUT` or set `0`. |
| MCP server shows `needs-auth` | OAuth cannot complete headless | Authorize it once in an interactive `claude` on that machine. |
| Hooks fail (`🪝` messages) with `command not found` | Hook needs a tool not on the service `PATH` | Re-run `make install` from a shell that has it, or disable that hook in a [profile](configuration.md#profilesyaml). |
| `/history` shows nothing | Transcripts are elsewhere | Set `CLAUDE_CONFIG_DIR` in the service environment if you use a non-default Claude config folder. |
| `this session is already open in another topic` | You tried to resume a session that already has a topic | Use the existing topic. |
| Subscription limit notices | Claude usage limits | `/usage` shows the windows; wait for the reset. |

## Service does not start

| Symptom | Cause | Fix |
|---|---|---|
| `make install` stops with `Another tgsync process is running` | A terminal `./bin/tgsync run` or a stray process | Stop it (`pkill -x tgsync` after checking `pgrep -a tgsync`), run again. |
| `make install` stops with `Fill in …/.env and run the install again` | No `.env` found; the template was copied to the node folder | Edit that file and run `make install` again. |
| `make install` stops with `Fix the errors above` | `tgsync check` failed | Fix the `✗` lines. |
| `Neither Go nor a prebuilt tgsync next to the script was found` | Neither Go nor a release binary | Install Go, or use a release archive. |
| `systemctl --user` says `Failed to connect to bus` | No user session bus (e.g. `su` or plain SSH without a login session) | Log in directly (SSH as that user), or `export XDG_RUNTIME_DIR=/run/user/$(id -u)`. |
| Linux: node stops when you log out | Linger is off | `sudo loginctl enable-linger "$USER"`. Check: `loginctl show-user "$USER" -p Linger`. |
| Service keeps restarting every 5 s | Fatal start error (rights, config, database) | `journalctl --user -u tgsync -n 50` shows the reason. |
| `migrate: …` or `database is locked` | Database copied while in use, disk full, or two processes on one DB | Stop all tgsync processes; check free space; restore from backup if corrupted. |
| `sudo: socket …: …` | Could not create `askpass.sock` (stale file, permissions) | Stop the node, delete the stale `askpass.sock` in the node folder, start again. |
| macOS: nothing in `launchctl print` | Agent not loaded | `make install` again; look at `~/Library/Logs/tgsync.log`. |
| macOS: node stops at night | Mac sleeps or you logged out | Keep logged in; disable sleep. |
| Windows: `running scripts is disabled on this system` | Execution policy | Use `powershell -ExecutionPolicy Bypass -File scripts\install.ps1` exactly. |
| Windows: task `Ready` but no process | Crash loop or not logged in | Read `%APPDATA%\tgsync\tgsync.log`; run the exe with `check`. |
| Windows: old node keeps answering after `Stop-ScheduledTask` | The `tgsync.exe` child outlived the task | `Get-Process tgsync \| Stop-Process`. |

## Permissions and sudo

| Symptom | Cause | Fix |
|---|---|---|
| The agent runs commands without asking | Approval mode is 🟢 or 🟡 (`/approve`), an "Always" rule matched, your Claude Code settings allow it (`permissions.allow`, `defaultMode`), or the session mode is `acceptEdits` (`/mode`) | Switch `/approve` to 🔴; review `~/.claude/settings.json` and the project's `.claude/settings*.json`; set `/mode default`. |
| A turn seems stuck and no button is visible | The request is higher up in the session topic, or it is in another session's topic | Scroll the session topic; `REMIND_EVERY` posts a reminder for unanswered requests. `STALL_WARN` does not fire while a request waits for you. |
| Buttons do nothing | You are not in `ALLOWED_USER_IDS`, or the node restarted and the request expired | Check the id; repeat the action. |
| `✓ sudo — SUDO_MODE=off: commands with sudo are rejected` and sudo commands are rejected | Default | Enable a mode ([configuration → sudo](configuration.md#sudo)) or use `NOPASSWD`. |
| `✗ sudo — SUDO_MODE=telegram: give the bot the “Delete messages” right` | Required so the password is deleted | Add the right. |
| `✓ sudo — not available on Windows, SUDO_MODE forced to off` | Windows has no sudo | Expected. |
| sudo asks again / "incorrect password" | Wrong password, or password changed | Up to 3 attempts per call. Update `SUDO_PASSWORD` for `env` mode. Beware `pam_faillock` lockouts. |
| sudo fails with `a terminal is required` | Command reached sudo without tgsync's askpass (e.g. inside a script the agent wrote) | Only `sudo` calls written directly in the approved command get the token. Ask the agent to run `sudo` explicitly. |
| Commands with the tgsync folder path always need a button, even in 🟢 | By design: tgsync protects its own files | Expected. See [security.md](security.md). |

## Voice messages

| Reply in the topic | Cause | Fix |
|---|---|---|
| `🎙 Speech recognition is not configured (STT_URL).` | `STT_URL` empty | Set it and restart ([configuration → voice](configuration.md#voice-messages)). |
| `🎙 Voice messages work in a session topic.` | Sent in the control topic | Send it in a session topic. |
| `🎙 Too long: N s, the maximum is M s.` | Longer than `STT_MAX_SECONDS` | Raise the limit or split the message. |
| `⚠️ The audio is over 20 MB …` | Telegram does not let bots download larger files | Send a shorter recording. |
| `⚠️ STT: … connection refused` | STT server is not running or listens elsewhere | `docker compose -f deploy/stt/compose.yaml ps`; `curl http://127.0.0.1:8000/v1/models`. In Docker, `127.0.0.1` is the container itself: use the host address. |
| `⚠️ STT: HTTP 404: …` / model not found | Model not downloaded, or `STT_MODEL` differs | `curl -X POST http://127.0.0.1:8000/v1/models/Systran/faster-whisper-small`, or fix `STT_MODEL`. |
| `⚠️ STT: context deadline exceeded` | Transcription slower than `STT_TIMEOUT` (first request loads the model) | Try again; raise `STT_TIMEOUT` (e.g. `180s`); use a smaller model or a GPU. |
| `⚠️ STT: unexpected server response` | `STT_URL` points to something that is not an OpenAI-compatible STT API | Point it to the server root, without `/v1`. |

## Files

| Message | Cause | Fix |
|---|---|---|
| `…: file is outside the project folder` / `link points outside the project` | `/file` only serves files inside the project | Copy the file into the project. |
| `…: tgsync's own files are not sent` | `.env` and the database are never sent | Expected. |
| `… MB, over Telegram's 50 MB limit` | Bot upload limit | Compress or split. |
| `the file is over 20 MB — Telegram's limit for bots` | Bot download limit for files you send | Send a smaller file, or put it on the machine another way. |
| Expected files are not sent at the end of a turn | Not matched by `AUTO_SEND_GLOBS`, or more than 5 in a turn | Adjust the globs; use the buttons or `/file path`. |

## Docker

| Symptom | Cause | Fix |
|---|---|---|
| `set TGSYNC_UID=$(id -u)` | Variables not passed | Prefix the command with `TGSYNC_UID=$(id -u) TGSYNC_GID=$(id -g)`. |
| `set PROJECTS_ROOT in the env file` | Compose did not get the env file | Pass `--env-file ~/.config/tgsync/.env`. |
| Mount error for `~/.claude.json`, or it became a folder | File did not exist | Remove the folder if created, `touch ~/.claude.json`, start again. |
| Agent is not logged in inside the container | macOS host (Keychain), or not logged in on the host | Use `claude setup-token` → `CLAUDE_CODE_OAUTH_TOKEN=…` in `.env`. |
| Hooks fail with `command not found` | Tool missing in the image | Rebuild with `--build-arg EXTRA_PACKAGES="…"`. |
| Permission denied on project files | Container user differs from file owner | Use your own `id -u`/`id -g`; files must be owned by you. |
| `409 Conflict` | systemd service also running | `systemctl --user disable --now tgsync`. |
