**English** · [Русский](../ru/security.md)

# Security

tgsync is a remote control for an agent that runs on your computer as your OS user. Whoever controls a node can, in effect, run commands as you. This document describes what tgsync protects against, what it does not, and how to run it carefully.

The bot's interface is in Russian; labels are quoted as they appear in Telegram, with an English explanation.

- [Threat model](#threat-model)
- [Who can control a node](#who-can-control-a-node)
- [Bot token](#bot-token)
- [What the agent can do in each mode](#what-the-agent-can-do-in-each-mode)
- [tgsync's own files](#tgsyncs-own-files)
- [Sensitive project files](#sensitive-project-files)
- [The «Всегда» (Always) button](#the-всегда-always-button)
- [sudo](#sudo)
- [Data stored and where](#data-stored-and-where)
- [What is sent to Telegram](#what-is-sent-to-telegram)
- [External services](#external-services)
- [Windows and Docker](#windows-and-docker)
- [Reporting a vulnerability](#reporting-a-vulnerability)

## Threat model

**tgsync trusts:**
- your Telegram account (its user ID in `ALLOWED_USER_IDS`);
- the machine the node runs on and your OS user;
- Claude Code and your `~/.claude` configuration (plugins, hooks and MCP servers run as in the terminal).

**It protects against:**
- other Telegram users controlling the node, even if they are in the group;
- the agent taking risky or irreversible actions without a tap, unless you turned auto-approve on;
- the agent reading the bot token, the sudo password or tgsync's database with its ordinary tools, or sending them to Telegram;
- the sudo password reaching the agent's environment, context, logs or database.

**It does not protect against:**
- a command you approved without reading it: tgsync is not a sandbox, and an approved command runs with your rights;
- code the agent writes and runs in the 🟢 and 🟡 auto-approve modes: it runs as your user and sees everything you can;
- an attacker who has your Telegram account or the bot token;
- Telegram's servers reading the traffic: Bot API messages are not end-to-end encrypted;
- malicious plugins, hooks or MCP servers in `~/.claude`.

## Who can control a node

- A node accepts messages and button presses only from users in `ALLOWED_USER_IDS`. Everyone else is silently ignored.
- A node only works in its own group (`GROUP_CHAT_ID`) and only in its own topics.
- Every member of the group sees all agent output: commands, answers, diffs, files. Do not add people or bots you do not trust.
- Protect your Telegram account: turn on two-step verification (cloud password) and review active sessions.

## Bot token

- The token gives full control of the bot. Keep it only in `.env` (mode `0600`, listed in `.gitignore`).
- The node removes the token from the environment of the `claude` process, so the agent does not receive it through variables.
- If the token leaks into a chat, a log or a repository, revoke it: @BotFather → `/revoke` → pick the bot. Put the new token into the node's `.env` (see [below](#data-stored-and-where)) and restart the node.
- Each node has its own bot. A leaked token of one node gives no access to other machines, but it does expose everything that bot can see in the group.

## What the agent can do in each mode

The auto-approve mode is set per node with `/approve` (see [usage.md](usage.md#permissions)).

| | 🔴 По запросу (On request, default) | 🟡 Всё, кроме sudo (All but sudo) | 🟢 Всё сам (Everything) |
|---|---|---|---|
| Read and search files | no prompt | no prompt | no prompt |
| Edit files inside the project | no prompt, except [sensitive files](#sensitive-project-files) | no prompt | no prompt |
| Edit files outside the project | button | no prompt | no prompt |
| Shell commands | button, unless an «Всегда» rule matches | no prompt | no prompt |
| sudo (if `SUDO_MODE` is not `off`) | button | button | no prompt |
| Commands that may touch tgsync's folder | button without «Всегда» | button without «Всегда» | button without «Всегда» |
| Questions from the agent | buttons | buttons | buttons |

In every mode, whatever your Claude Code settings allow (`~/.claude/settings.json`, project settings) runs without a prompt. Make sure there are no broad rules there that you would not trust the agent with unattended.

**In practice.** In 🟡 and 🟢 the agent can run any code as your user: delete files, send data over the network, read SSH keys. Use them only for projects and tasks you trust, and switch back to 🔴 when you are not watching. In 🟢 with sudo enabled, the agent gets root without confirmation.

`/mode acceptEdits` (Claude Code's own mode) lets Claude Code accept file edits without asking. In that mode tgsync's per-file checks may not be consulted, so keep `default` when working with unfamiliar or untrusted code.

## tgsync's own files

tgsync's own files are `.env` (bot token, sudo password) and the SQLite database (`tgsync.db` and its `-wal`/`-shm` files).

- Read, write and search tools (`Read`, `Grep`, `Glob`, `LS`, `Write`, `Edit`, `MultiEdit`, `NotebookEdit`) that point at these files, or at a folder that holds them or tgsync's folder, are denied. `~` is expanded and symlinks are followed first, so a link in the project that leads to tgsync's folder does not help; a `Glob` pattern that starts with such a folder is denied too.
- A shell command with the literal path of `.env` or the database is denied, with or without sudo.
- A shell command that may reach tgsync's folder another way always needs a button without «Всегда», in every auto-approve mode, and saved rules do not apply to it. Read such commands carefully. This covers `~`, `~user` and `$HOME`; `$XDG_CONFIG_HOME`, `$APPDATA`, `$PWD` and similar variables (expanded from the node's environment); relative paths, also after `cd`/`pushd`, and links inside the project; globs such as `tgs*`; unknown variables before `/tgsync/`; scripts run by `sh -c`, `eval`, `python -c`/`node -e` and heredocs fed to a shell or an interpreter; parent folders such as `~` or `/`; and commands the shell parser does not support.
- Not flagged: quoted text (`git commit -m "a / b"`, a heredoc written to a file) unless a shell or an interpreter runs it, paths inside the project, and the project's own parent (`ls ..`, `cd .. && ls`) unless the command walks folder trees (`grep -r`, `rg`, `find`, `tar` …).
- `send_file`, `/file`, `/ls` and turn diffs never send these files, even if they sit inside a project.

**Limitation.** The agent runs as the same OS user, so these are tgsync's rules, not OS permissions. This is defence in depth, not a boundary: a script the agent writes and runs (without a prompt in 🟡 and 🟢) can read these files and tgsync will not see it.

## Sensitive project files

Some project files make git, Claude Code or the shell run commands: `.git/` (config, hooks), `.gitattributes`, `.gitmodules`, `.claude/`, `.envrc` at any depth, and `.mcp.json` and `.vscode/tasks.json` at the project root. tgsync itself runs `git add` for turn snapshots, which runs `.gitattributes` filters.

So in 🔴 mode, writing such a file needs a button even inside the project, with no «Всегда», and saved rules do not apply. Symlinks are resolved: a file whose link leads outside the project or into one of these paths also needs a button.

## The «Всегда» (Always) button

- For shell, the command and its first argument are saved (`go test`). Chains, substitutions and redirections never match a rule.
- Shells and interpreters, `rm`, `dd`, `chmod`, `find`, wrappers (`env`, `xargs`, `eval`, `timeout` …), `ssh`, `sudo`, commands prefixed with `VAR=value` or named through a variable, `git` with global options and `git config`, commands with an `-o`/`--output` flag, and commands with an argument that points into `.git/`, `.claude/` or another sensitive project file (by name or through a link) get no «Всегда» button. Older rules such as `python3`, `rm`, `git log` or `go build` no longer apply to them. Quoting the name (`\rm`, `"rm"`) changes nothing.
- A rule such as `npm run` or `make` allows any script or target, so keep «Всегда» for narrow commands.
- For an edit outside the project, the rule covers only that file's folder, not subfolders; older rules without a folder no longer apply.
- Rules are stored in the database per project.

## sudo

The agent's Bash runs without a terminal, so `sudo` cannot prompt for a password itself. tgsync provides an askpass bridge:

1. When the agent wants to run a command with `sudo`, the topic shows a «🔐 sudo» request with the command and ✅/❌ buttons. There is no «Всегда».
2. After ✅, tgsync finds the real `sudo` calls in the command (with a shell parser, not a text search) and rewrites each as `SUDO_ASKPASS=<tgsync binary> TGSYNC_SUDO_TOKEN=<one-time token> sudo -A …`, after any `VAR=value` of the command itself, so tgsync's askpass always wins. The token is valid only for those calls, for 5 minutes and until the turn ends; a call inside a loop or a function may ask up to 5 times. sudo through wrappers (`env`, `xargs`, `find -exec`, `sh -c`) is not supported; the agent is asked to rewrite the command.
   A command that mentions `SUDO_ASKPASS`, assigns `PATH`, defines a `sudo` function or alias, or runs sudo from outside the system folders (`./sudo`, `/tmp/x/sudo`) is refused before any prompt: the program it chose would get the token and the password.
3. `sudo` runs `tgsync` as askpass, which passes the token to the node over the unix socket `askpass.sock` (mode 0600). The node releases the password only if the token is valid and the socket peer was started by `sudo`: its parent must be named `sudo` and run with effective uid 0, like a real setuid sudo (checked with `SO_PEERCRED` and `/proc/<pid>/status` on Linux, `LOCAL_PEERPID` and `sysctl` on macOS).

Modes (`SUDO_MODE` in `.env`):

| Mode | Password source | Risks |
|---|---|---|
| `off` (default) | sudo is refused | none |
| `env` | `SUDO_PASSWORD` from `.env`; the node removes it from the agent's environment | the password is stored in plain text on disk (mode 0600); the agent runs as the same user and can technically read the file — tgsync forbids that by rule only |
| `telegram` | after ✅ the bot asks for the password in the topic and deletes the message at once | the password passes through Telegram's servers and is visible to other bots in the group until deleted; the bot needs the "Delete messages" right (`tgsync check` verifies it) |

In `telegram` mode the password is asked for on every command: without a terminal sudo does not cache the login. Several sudo calls within one approved command share one answer; after a typo sudo asks again, up to 3 attempts per call. A wrong password counts as a failed login (`pam_faillock` may lock the account).

Keep in mind:
- An approved sudo command runs as root. The button is the safeguard: read the whole command before ✅. Other parts of the same command (e.g. `./configure` in `./configure && sudo make install`) do not see the token but run as your user.
- In the 🟢 auto-approve mode, sudo commands run without a button.
- The one-time token is part of the command line, so other processes of your user can see it in the process list while the command runs. It cannot be hidden there; what limits it: it works only for the approved calls (usually once), for 5 minutes and until the turn ends, and the node answers only a process started by a setuid-root `sudo`.
- Do not set `SUDO_PASSWORD` via systemd `Environment=` or compose `environment:`: it is visible in `/proc/<pid>/environ` from there.
- Safer than any password: a `NOPASSWD` rule in `/etc/sudoers.d/` for exactly the commands you need (e.g. `systemctl restart myapp`), with `SUDO_MODE=off` for everything else.
- Commands that touch tgsync's own files are denied with sudo too.

## Data stored and where

Node folder: `~/.config/tgsync` on Linux, `~/Library/Application Support/tgsync` on macOS, `%APPDATA%\tgsync` on Windows (or `TGSYNC_HOME`).

| What | Where | Contents |
|---|---|---|
| `.env` | node folder, mode 0600 | bot token, user and group IDs, settings, sudo password in `env` mode |
| `profiles.yaml` | node folder | plugin profiles |
| SQLite database | `DB_PATH` (default `data/tgsync.db` in the node folder) | sessions and their topics, «Всегда» rules, auto-approve mode, token usage, subscription limit state, IDs of control topic messages to clean up |
| incoming files | `.tgsync/inbox/` in the project | files you sent to the agent; excluded from git via `.git/info/exclude` |
| turn snapshots | git objects in the project repository | trees of the working directory before and after each turn; branches, index and stash are untouched, and `git gc` removes unreachable objects |
| logs | journald (`journalctl --user -u tgsync`), `~/Library/Logs/tgsync.log` on macOS, the `TGSYNC_LOG` file on Windows | node events and errors; they may contain paths, commands and error text — review before sharing |

Conversation history is stored by Claude Code itself in `~/.claude`, as for terminal sessions. tgsync reads it (for `/history`) but does not copy it.

## What is sent to Telegram

A session topic receives: your tasks, the agent's answers, its current actions (shell commands, file paths), permission requests with commands, questions, lists of changed files, diffs and files you requested or that match `AUTO_SEND_GLOBS`, voice transcripts, subagent results. All of it is stored on Telegram's servers and visible to every group member.

Do not ask the agent to print secrets into the chat, and be careful with `AUTO_SEND_GLOBS`: a pattern such as `**/*` sends everything the agent changes.

## External services

tgsync talks only to:
- the **Telegram Bot API**, to run the bot;
- **Claude Code**, the local `claude` CLI, which talks to Anthropic exactly as it does in the terminal;
- the **speech-to-text server** at `STT_URL`, if one is set.

There are no other AI services, analytics or telemetry. Speech recognition is meant to run on your own server (the bundled `deploy/stt/compose.yaml` listens on `127.0.0.1` only). If you point `STT_URL` at someone else's service, voice messages go there — which is against the design of this project.

## Windows and Docker

- Unix modes 0600/0700 have no effect on Windows. `.env` and the database are protected only by your user profile permissions: `%APPDATA%` is readable by you and by administrators.
- There is no sudo on Windows: `SUDO_MODE` is forced to `off` and no socket is created.
- The container built from `Dockerfile` has no sudo; keep `SUDO_MODE=off`. It runs with your uid and mounts `~/.claude` and `PROJECTS_ROOT`, so it does not isolate the agent from your files.

## Reporting a vulnerability

Please do not open a public issue. Report privately through GitHub as described in [SECURITY.md](../../SECURITY.md).
