# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Russian version: [CHANGELOG.ru.md](CHANGELOG.ru.md).

## [0.1.0] - 2026-09-26

First public release.

### Added

#### Sessions
- A node per machine with its own bot; all nodes share one Telegram forum group.
- Control topic `🖥 <node>` with a button menu and commands: `/projects`, `/newproject`, `/new project [--profile name] [task]`, `/sessions`, `/history`, `/profiles`, `/usage`, `/approve`, `/help`; `/control` in General finds or re-creates the control topic.
- One topic per session with a fixed name `project · task`; sessions resume with full context after a crash, an idle shutdown or a node restart.
- Live status message with elapsed time, step count, current action, subagent action and the agent's latest remark; a stop button.
- One answer message per turn with duration, steps, cost and context fill.
- Message queue with a 👀 reaction and a «send now» button that interrupts the current turn; per-node parallel limit and one turn per project.
- `/stop`, `/mode default|acceptEdits|plan`, `/close`, and a collapsed `⋯` panel under answers (files, mode, close with confirmation).
- Idle timeout, stall warning with wait/stop buttons, optional hard turn limit, reminders for unanswered prompts.
- Deleting a session topic in Telegram closes the session; sessions closed without any answer are removed with their topic.
- MCP servers that need attention and hook output addressed to the user are shown in the topic.

#### Permissions
- Permission requests as buttons: allow once, deny (with a follow-up reason), or «Всегда» (always) saved per project.
- The agent's Bash description is shown with the command, so you can see why it is needed.
- Node-wide approve modes: on request (default), everything but sudo, everything.
- `AskUserQuestion` as buttons, with multiple choice, several questions in a row, and free-text or voice answers.
- `sudo` through an askpass bridge: per-command approval, password from `.env` (`SUDO_MODE=env`) or typed in the topic and deleted at once (`SUDO_MODE=telegram`).

#### Turn summary
- List of files changed in the turn with per-file and total `+/-` line stats.
- Per-turn git snapshots through a temporary index, leaving the user's index, refs and stash untouched.
- Whole-turn diff as a file, commit via the agent in the repository's style, and confirmed rollback to the state before the turn.

#### Files
- The agent sends documents with a `send_file` tool; documents it mentions and files matching `AUTO_SEND_GLOBS` are sent after the turn; each version is sent once.
- `/file path` and a `/ls` file browser with buttons.
- Documents, photos and screenshots sent to a session topic are stored in `.tgsync/inbox/` (excluded from git) and handed to the agent, with the caption as the task.

#### Voice
- Voice and audio messages transcribed by a self-hosted, OpenAI-compatible speech-to-text server (`STT_URL`); a compose file for a local server is included.
- The agent restates the transcribed task and waits for confirmation before acting.

#### Usage and limits
- Context fill in every turn footer, a compaction hint with a one-tap `/compact`, and a notice when history is compacted; `/context`.
- `/usage` per session (turns, tokens, estimated cost) and per node (subscription limits, usage today and over 7 days per project).
- Subscription limit alerts: approaching, reached and available again, once per window.

#### Subagents
- Live agents panel with a caller tree, per-agent card, stop button and full result as a file; `/agents`.
- Background agents keep running after the turn; their results are handled in continuation turns that do not take a parallel slot. Closing a session stops them.

#### History
- `/history` lists recent Claude Code sessions from the terminal, desktop app and bot, and attaches a topic to any of them.
- A session that is still active elsewhere is continued as a fork; a session already open in Telegram is linked instead of opened twice.

#### Profiles
- Plugin profiles in `profiles.yaml`: setting sources, environment and settings overrides, chosen per session, per project or by default; `full` mirrors the terminal setup.
- `/skills` lists the commands and skills available to the agent; unknown `/commands` are passed to the agent.

#### Group management
- Pinned control card with session counters, online-since time, and cleanup buttons.
- Confirmed cleanup of closed topics and of topics that failed without an answer.
- Control topic kept clean: only the latest menu stays, service messages are removed after about an hour, and a button clears them at once.
- Per-node topic icon colour and state icons; the General topic is hidden once; group avatar and description set once if missing; a notice lists missing admin rights.
- `tgsync profile` sets the bot's avatar and descriptions.

#### Platforms and packaging
- Linux (systemd user service), macOS (launchd agent) and Windows (Task Scheduler) installers and uninstallers.
- `tgsync check` diagnoses the configuration, Telegram rights and the `claude` CLI.
- Native config folders per OS, `TGSYNC_HOME`, `CLAUDE_CLI_PATH`, and `TGSYNC_LOG` for file logging.
- Dockerfile and compose file for running a node in a container.
- CI on Linux, macOS and Windows; GoReleaser archives for Linux, macOS and Windows on amd64 and arm64.
- Telegram requests retried on rate limits and network failures.

### Security
- Only users in `ALLOWED_USER_IDS` can control a node, and only in its own group and topics.
- tgsync's own files (`.env`, the database and its WAL files) are denied to file tools, search and literal-path shell commands, and are never sent to Telegram.
- Shell commands that may reach tgsync's folder in any spelling (`~`, `$HOME`, relative paths, globs, variables, nested shells, heredocs) need a tap in every approve mode, without «Всегда»; saved rules do not apply to them.
- Writes to files that make git, Claude Code or the shell run commands (`.git/`, `.gitattributes`, `.gitmodules`, `.claude/`, `.envrc`, `.mcp.json`, `.vscode/tasks.json`) need approval even inside the project in the default approve mode; symlinks are resolved before deciding.
- «Всегда» rules are kept as narrow as what was approved: command plus first argument, no chains, no rule at all for shells, interpreters, destructive commands, wrappers, `ssh`, `sudo` or `git -c`; file rules cover a single folder. Older, broader rules no longer apply.
- sudo tokens are one-time, bound to the approved calls and short-lived; the askpass socket only answers a peer whose parent is a real `sudo` running as root (Linux and macOS); sudo hidden in wrappers is refused; a pending password prompt is withdrawn when the turn ends; sudo is disabled on Windows.
- The bot token and the sudo password are removed from the agent's environment; the sudo password is never written to logs or the database.
- Path containment is OS-aware (Windows drive and Git Bash spellings, case folding, trailing dots, streams, hard links); sent files are read through one checked handle.
- Panics in update handlers are recovered; file downloads, speech recognition and transcript reads are bounded in time or size; button data stays within Telegram's limits; buttons kept per session are capped; double taps cannot create duplicate sessions or topics.

[0.1.0]: https://github.com/ArthurKrantsevich/tgsync/releases/tag/v0.1.0
