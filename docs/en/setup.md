# Installation and operations

This guide takes you from nothing to a working tgsync node, then covers day-to-day operations. Every variable mentioned here is described in [configuration.md](configuration.md); if something goes wrong, see [troubleshooting.md](troubleshooting.md).

**Terms.** A *node* is one machine running tgsync with its own Telegram bot. All nodes share one Telegram *forum group* (a supergroup with Topics). Each node has a *control topic* `🖥 <node name>`, and every Claude Code session gets its own topic.

> The bot's interface, the output of `tgsync check` and the install scripts speak English by default; set `BOT_LANGUAGE=ru` in `.env` for Russian (see [configuration.md](configuration.md)). Command names (`/menu`, `/new`, …) are the same in any language.

- [1. Prerequisites](#1-prerequisites)
- [2. Telegram: bot and group](#2-telegram-bot-and-group)
- [3. Get tgsync](#3-get-tgsync)
- [4. Configure](#4-configure)
- [5. Install as a service](#5-install-as-a-service)
- [6. First start and verification](#6-first-start-and-verification)
- [7. Optional features](#7-optional-features)
- [8. Operations](#8-operations)
- [9. Security checklist](#9-security-checklist)

## 1. Prerequisites

| What | Why |
|---|---|
| Linux, macOS or Windows 10/11 (amd64 or arm64) | Supported platforms. sudo support is Linux and macOS only. |
| Claude Code, installed **and logged in** | tgsync drives the `claude` CLI; it does not have its own AI access. |
| Git | Diffs and projects; on Windows Claude Code also needs Git for Windows (Git Bash). |
| Go 1.27.1 or newer | Only to build from source (version from `go.mod`). Not needed with a release archive. |
| A Telegram account | To create the bot and the group. |

### 1.1 Install and log in to Claude Code

Linux / macOS:

```bash
curl -fsSL https://claude.ai/install.sh | bash
claude            # opens an interactive session; run /login if asked
claude --version  # must work in the same shell you will install from
```

Windows (PowerShell):

```powershell
irm https://claude.ai/install.ps1 | iex
claude
```

Windows specifics:

- Use the **native** installer. The npm package installs a `claude.cmd` shim that tgsync cannot start (`tgsync check` says so explicitly).
- Install [Git for Windows](https://git-scm.com/download/win): Claude Code's Bash tool runs through Git Bash.

On a headless server, log in once interactively (`claude`, then `/login`), or create a long-lived token with `claude setup-token` and put `CLAUDE_CODE_OAUTH_TOKEN=…` into tgsync's `.env` (the `claude` process inherits the node environment).

MCP servers that need OAuth (`needs-auth`) must be authorized in an interactive `claude` session on that machine: a headless session cannot complete the browser flow.

### 1.2 Go (only for building from source)

Install Go from <https://go.dev/dl/> and check:

```bash
go version   # go1.27.1 or newer
```

## 2. Telegram: bot and group

### 2.1 Create the bot (one per node)

1. Open [@BotFather](https://t.me/BotFather) and send `/newbot`.
2. Choose a display name and a username ending in `bot`, e.g. `ExampleNodeBot`.
3. Copy the token (`123456:ABC-…`). Treat it like a password: whoever has it controls the bot.

Every machine needs **its own** bot. Telegram lets only one process receive updates for a token; a second one fails with `409 Conflict`.

You do **not** need to:

- change privacy mode: the bot will be an administrator, and administrators receive all group messages anyway;
- set commands with `/setcommands`: tgsync registers its command menu for the group itself on every start.

### 2.2 Create the forum group

1. In Telegram, create a new group (you can add the bot right away or later).
2. Open the group settings → **Topics** → enable. The group becomes a supergroup; its id changes at this moment, so find the id only **after** this step.
3. Keep the group private. Everyone in it sees all agent output.

### 2.3 Make the bot an administrator

Group settings → **Administrators** → **Add admin** → pick your bot. Enable these rights:

| Right | Required | What it enables |
|---|---|---|
| **Manage topics** | **yes** | Creating, renaming, closing topics; hiding General. Without it the node refuses to start. |
| **Pin messages** | recommended | Pinning the node card (counters and cleanup buttons) in the control topic. |
| **Change group info** | recommended | Setting the group avatar and description once, if the group has none. |
| **Delete messages** | recommended | Deleting empty topics and topics removed with 🧹 Clean up (otherwise they are only closed), tidying the control topic, and required for `SUDO_MODE=telegram` (the typed password is deleted). |

Other rights are not used. If an optional right is missing, the bot posts a notice in its control topic listing what does not work.

### 2.4 Find your user id

Message [@userinfobot](https://t.me/userinfobot) (or any similar bot) and copy the numeric `Id`, e.g. `123456789`. This goes into `ALLOWED_USER_IDS`.

### 2.5 Find the group id

Option A, Telegram Web: open the group at <https://web.telegram.org/a/>. The address ends with `#-1001234567890`; that number (with the minus) is the id.

Option B, Bot API (only while tgsync is **not** running with this token):

1. Send any message in the group.
2. Run, with your token:
   ```bash
   curl -s "https://api.telegram.org/bot123456:ABC-your-token/getUpdates"
   ```
3. Find `"chat":{"id":-100…` in the output.

A supergroup id always starts with `-100`. If you see a short negative id such as `-123456789`, Topics are not enabled yet (the group is not a supergroup).

### 2.6 Several nodes in one group

Repeat 2.1 and 2.3 for each machine: a new bot, added to the **same** group as an administrator with the same rights. All nodes use the same `GROUP_CHAT_ID` and `ALLOWED_USER_IDS`, and different `NODE_NAME`s. Each node answers only in its own topics.

## 3. Get tgsync

### Option A: build from source

```bash
git clone https://github.com/ArthurKrantsevich/tgsync.git
cd tgsync
make build          # produces ./bin/tgsync
./bin/tgsync version
```

On Windows without `make`: `go build -o bin\tgsync.exe .\cmd\tgsync`.

The install scripts (step 5) build the binary themselves when Go is available, so this step is optional if you go straight to a service install.

### Option B: release archive

When a release is published on the GitHub Releases page, it contains archives for `linux`, `darwin` and `windows` on `amd64` and `arm64`, plus `checksums.txt`. Each archive holds the `tgsync` binary, `scripts/`, `docs/`, `.env.example` and `profiles.example.yaml`.

```bash
sha256sum -c checksums.txt --ignore-missing
mkdir tgsync && tar xzf tgsync_*_linux_amd64.tar.gz -C tgsync && cd tgsync
```

On macOS a downloaded binary may be quarantined by Gatekeeper; clear it with `xattr -d com.apple.quarantine tgsync`.

From a release archive, run the install script directly (there is no Makefile): `sh scripts/install.sh`, or `scripts\install.ps1` on Windows. Without `go.mod` next to them, the scripts copy the bundled binary instead of building.

## 4. Configure

The install scripts expect `.env` in the repository (or unpacked archive) folder and **move** it into the node folder, so no second copy of the token stays behind. Alternatively create it directly in the node folder (see the table in [configuration.md](configuration.md#node-folder)).

1. Copy the template:
   ```bash
   cp .env.example .env
   chmod 600 .env
   ```
2. Fill in the four required values:
   ```
   TELEGRAM_BOT_TOKEN=123456:ABC-replace-with-your-token
   ALLOWED_USER_IDS=123456789
   GROUP_CHAT_ID=-1001234567890
   PROJECTS_ROOT=/home/user/projects
   ```
   `PROJECTS_ROOT` must be absolute; each subfolder is a project.
3. Optionally set `NODE_NAME` (default: hostname) and `BOT_LANGUAGE` (`en`, the default, or `ru` for a Russian interface), and look through the rest of `.env.example`.
4. Check before installing:
   ```bash
   make check          # builds ./bin/tgsync and runs "tgsync check"
   ```

Example `tgsync check` output:

```
tgsync dev · folder /home/user/tgsync
✓ config — .env loaded
✓ bot — @ExampleNodeBot
✓ group with topics — -1001234567890
✓ bot rights — administrator, all required rights
✓ profiles — full (default full)
✓ sudo — SUDO_MODE=off: commands with sudo are rejected
✓ claude CLI — /home/user/.local/bin/claude 2.x.x (Claude Code)
✓ PROJECTS_ROOT — /home/user/projects
✓ database folder — data
All good.
```

Each line starting with `✗` says what to fix. Lines: config, bot, group with topics, bot rights, profiles, sudo, claude CLI, projects folder, database folder. [troubleshooting.md](troubleshooting.md) explains each failure.

## 5. Install as a service

Pick one. Never run two instances with the same token (service + terminal, or service + Docker).

### 5.1 Linux (systemd user service)

```bash
make install        # same as: sh scripts/install.sh
```

What the script does:

1. Refuses to continue if another `tgsync` process of your user is running (e.g. `./bin/tgsync run` in a terminal).
2. Builds `~/.local/bin/tgsync` (or copies the release binary).
3. Creates `~/.config/tgsync` and `~/.config/tgsync/data` with mode `0700`.
4. Moves `.env` from the repository into `~/.config/tgsync/.env` (mode `0600`) if it is not there yet. With no `.env` anywhere it copies `.env.example` there and stops: fill it in and run `make install` again.
5. Moves an existing `./data/tgsync.db` from the repository too, so your topics survive.
6. Runs `tgsync check` and stops on any error.
7. Writes `~/.config/systemd/user/tgsync.service` with the **current `PATH`**, so the service finds `claude`, `node` and `python` for plugin hooks exactly as your shell does. Run the install from a shell where `claude --version` works.
8. Enables and (re)starts the service.
9. Enables *linger* (`loginctl enable-linger`) so the node runs without an active login and after reboot. If that fails, run it yourself:
   ```bash
   sudo loginctl enable-linger "$USER"
   ```

The generated unit:

```ini
[Service]
Type=simple
WorkingDirectory=%h/.config/tgsync
Environment="PATH=…your PATH…"
ExecStart="/home/user/.local/bin/tgsync" run
Restart=on-failure
RestartSec=5
```

Make sure `~/.local/bin` is in your `PATH` so that `tgsync check` works from any folder.

### 5.2 macOS (launchd agent)

```bash
make install
```

Same steps as Linux, except:

- node folder: `~/Library/Application Support/tgsync`;
- service: launchd agent `~/Library/LaunchAgents/dev.tgsync.plist` (label `dev.tgsync`), started at login and restarted after a crash;
- logs: `~/Library/Logs/tgsync.log`.

A launchd user agent runs only while you are logged in to macOS. To keep the node up, stay logged in (screen lock is fine) and disable sleep for the power source you use.

### 5.3 Windows (Task Scheduler)

Requirements: native Claude Code (`claude.exe`), Git for Windows, and Go (unless you use a release zip). In PowerShell, from the repository or unpacked archive folder:

```powershell
Copy-Item .env.example .env
notepad .env
powershell -ExecutionPolicy Bypass -File scripts\install.ps1
```

What the script does:

1. Refuses to continue if another `tgsync` is running, stops the old task.
2. Builds `%LOCALAPPDATA%\tgsync\bin\tgsync.exe` (or copies `tgsync.exe` from the release zip).
3. Moves `.env` into `%APPDATA%\tgsync\.env` if it is not there (or copies `.env.example` there and stops).
4. Runs `tgsync check` and stops on any error.
5. Registers the task `tgsync` for your user: starts at logon, hidden window, restarts after a crash, no administrator rights needed. Logs go to `%APPDATA%\tgsync\tgsync.log`.

Limits on Windows:

- The node runs only while you are logged in.
- There is no sudo: `SUDO_MODE` is always `off`.
- File permissions `0600/0700` do not apply; `.env` is protected by your user profile's ACL (you and administrators).

### 5.4 Docker (compose)

An alternative for Linux hosts. The container runs as your user, sees the same paths as the host and shares your `~/.claude` (login, plugins, transcripts).

1. Create the config in the node folder on the host: `~/.config/tgsync/.env` (step 4). `PROJECTS_ROOT` is required: compose mounts it at the same path.
2. Make sure `~/.claude.json` exists, otherwise Docker would create a folder in its place:
   ```bash
   test -f ~/.claude.json || touch ~/.claude.json
   ```
3. From the repository folder, build and start (`UID` cannot be exported in bash, hence the other names):
   ```bash
   TGSYNC_UID=$(id -u) TGSYNC_GID=$(id -g) \
     docker compose --env-file ~/.config/tgsync/.env up -d --build
   ```
4. Check and watch logs:
   ```bash
   TGSYNC_UID=$(id -u) TGSYNC_GID=$(id -g) \
     docker compose --env-file ~/.config/tgsync/.env run --rm tgsync check
   docker compose logs -f
   ```

Mounted volumes:

| Host | Container | Purpose |
|---|---|---|
| `~/.config/tgsync` | `/config` | `.env`, `profiles.yaml`, database |
| `~/.claude` | same path | Claude Code login (`.credentials.json` on Linux), plugins, transcripts |
| `~/.claude.json` | same path | Claude Code account state |
| `$PROJECTS_ROOT` | same path | Your projects |

Limits:

- **Claude Code login.** The image installs Claude Code via npm and reuses the host's login from `~/.claude`. On Linux this works when you are logged in on the host. On macOS the login lives in the Keychain, not in `~/.claude`, so the container is not logged in: use `claude setup-token` and add `CLAUDE_CODE_OAUTH_TOKEN=…` to `.env`.
- **Tools.** The image has `git`, `node`, `python3` and `curl`. Anything else your hooks or projects need must be added at build time:
  ```bash
  TGSYNC_UID=$(id -u) TGSYNC_GID=$(id -g) \
    docker compose --env-file ~/.config/tgsync/.env build --build-arg EXTRA_PACKAGES="golang make"
  ```
- **No sudo** inside the container: keep `SUDO_MODE=off`. For "install a system package" tasks use the systemd install.
- Only files under `PROJECTS_ROOT`, `~/.claude` and the node folder are visible to the agent.
- Do not run the systemd service and the container at the same time.

### 5.5 Running in a terminal (no service)

Useful for a first try or debugging:

```bash
make build
./bin/tgsync run
```

The node folder is chosen as described in [configuration.md](configuration.md#node-folder) (the current folder if it has `.env`, otherwise `~/.config/tgsync`, …). Stop with Ctrl+C. Do not run this while the service is running.

Subcommands:

| Command | What it does |
|---|---|
| `tgsync run` (default) | Run the node. |
| `tgsync check` | Diagnose configuration, Telegram, rights, `claude`, folders. Exit code 1 on any problem. |
| `tgsync profile` | Set the bot's own avatar and descriptions (see 6.2). |
| `tgsync version` | Print the version. |

## 6. First start and verification

### 6.1 What happens on the first start

1. The node reads `.env`, opens (or creates) the database and applies migrations.
2. It checks that the bot is an administrator with **Manage topics**; otherwise it exits with an error (and the service keeps restarting it).
3. It creates the control topic `🖥 <NODE_NAME>` (or renames the existing one; recreates it if you deleted it).
4. It hides the **General** topic, once per group. If you unhide it later, it stays visible.
5. If optional rights are missing, it posts a notice in the control topic.
6. If it has **Change group info** and the group has no photo or description, it sets them, once.
7. It posts the node card in the control topic and pins it (needs **Pin messages**). The card shows counters and the 🧹 Clean up buttons.
8. It registers the command menu (`/menu`, `/new`, `/help`, …) for the group.
9. It restores sessions that were open before a restart.

### 6.2 Bot avatar and description (optional, once)

```bash
tgsync profile
```

Sets the bot's profile photo, short description (profile page) and description (shown in an empty chat with the bot). The bot's display name is changed in @BotFather.

### 6.3 Verify

1. `tgsync check` prints `All good.`
2. Service is running:
   - Linux: `systemctl --user status tgsync`
   - macOS: `launchctl print gui/$(id -u)/dev.tgsync`
   - Windows: `Get-ScheduledTask tgsync`
3. The group has a topic `🖥 <NODE_NAME>` with a pinned card.
4. In that topic send `/menu`: the bot answers with the main menu.
5. Start a session: `/new <project> say hello`. A new topic appears and the agent's reply streams into it.

If the bot does not answer, see [Bot is silent](troubleshooting.md#bot-is-silent).

## 7. Optional features

All of these are `.env` settings; details and defaults are in [configuration.md](configuration.md).

- **Voice messages** with a self-hosted speech-to-text server: `deploy/stt/compose.yaml`, `STT_URL`. Step-by-step in [configuration.md → Voice messages](configuration.md#voice-messages). No cloud services are used.
- **Profiles**: `profiles.yaml` next to `.env` to switch plugins, settings sources and environment per session or per project. See [configuration.md → profiles.yaml](configuration.md#profilesyaml).
- **sudo**: `SUDO_MODE=off|env|telegram`. Read the risks in [configuration.md → sudo](configuration.md#sudo) first. The safest choice is `off` plus a narrow `NOPASSWD` rule.
- **Auto-send files**: `AUTO_SEND_GLOBS=**/*.md,**/*.pdf` sends matching changed files at the end of each turn.
- **Limits**: `MAX_PARALLEL_SESSIONS` (concurrent turns), `IDLE_TIMEOUT` (stop idle `claude` processes), `MAX_TURN_DURATION` (hard turn limit), `STALL_WARN` (silence warning), `REMIND_EVERY` (reminders about unanswered requests).
- **Approval mode**: chosen in Telegram with `/approve` (🟢 Allow all / 🟡 All but sudo / 🔴 Ask me, the default). Stored in the database, not in `.env`.

Restart the node after changing `.env` or `profiles.yaml`.

## 8. Operations

### 8.1 Logs, status, restart

| Action | Linux | macOS | Windows (PowerShell) |
|---|---|---|---|
| Status | `systemctl --user status tgsync` or `make status` | `launchctl print gui/$(id -u)/dev.tgsync` or `make status` | `Get-ScheduledTask tgsync` |
| Live logs | `journalctl --user -u tgsync -f` or `make logs` | `tail -f ~/Library/Logs/tgsync.log` or `make logs` | `Get-Content -Wait $env:APPDATA\tgsync\tgsync.log` |
| Last 100 lines | `journalctl --user -u tgsync -n 100` | `tail -n 100 ~/Library/Logs/tgsync.log` | `Get-Content -Tail 100 $env:APPDATA\tgsync\tgsync.log` |
| Restart | `systemctl --user restart tgsync` | `launchctl kickstart -k gui/$(id -u)/dev.tgsync` | `Stop-ScheduledTask tgsync; Get-Process tgsync -ErrorAction SilentlyContinue \| Stop-Process; Start-ScheduledTask tgsync` |
| Stop | `systemctl --user stop tgsync` | `launchctl bootout gui/$(id -u)/dev.tgsync` | `Stop-ScheduledTask tgsync; Get-Process tgsync -ErrorAction SilentlyContinue \| Stop-Process` |
| Diagnose | `tgsync check` | `tgsync check` | `& $env:LOCALAPPDATA\tgsync\bin\tgsync.exe check` |

Docker: `docker compose logs -f`, `docker compose restart`, `docker compose down`.

On stop, running turns are interrupted; open sessions resume on the next start.

### 8.2 Upgrade

```bash
git pull
make install          # Linux/macOS: rebuild, check, restart
```

Windows: `git pull`, then `powershell -ExecutionPolicy Bypass -File scripts\install.ps1`.
Release archive: unpack the new version and run its `scripts/install.sh` (or `install.ps1`).
Docker: `git pull`, then the `up -d --build` command from 5.4.

Configuration and database are never overwritten. Database migrations run automatically at start. Read the changelog for new `.env` variables; defaults apply when a variable is missing.

### 8.3 Backup

Back up the node folder:

| File | Why |
|---|---|
| `.env` | Token and settings (secret: store the backup encrypted) |
| `profiles.yaml` | Profiles, if you use them |
| `data/tgsync.db`, `-wal`, `-shm` | Topics ↔ sessions mapping, "Always" rules, approval mode, card state |

The database is in WAL mode. For a consistent copy either stop the node first, or use SQLite's online backup:

```bash
sqlite3 ~/.config/tgsync/data/tgsync.db ".backup '/home/user/tgsync-backup.db'"
```

Claude Code transcripts (`~/.claude/projects`) are what sessions resume from; include `~/.claude` in your usual home backup.

### 8.4 Uninstall

```bash
make uninstall        # Linux/macOS
```
```powershell
powershell -ExecutionPolicy Bypass -File scripts\uninstall.ps1   # Windows
```

This removes the service and the binary. The node folder (config and database) stays; delete it yourself if you want. To remove the node from Telegram too: delete its topics in the group, remove the bot from the group, and delete the bot in @BotFather (`/deletebot`). For Docker: `docker compose down` and delete the image.

On Linux you may also turn linger off if nothing else needs it: `loginctl disable-linger "$USER"`.

### 8.5 Adding another node

On the new machine:

1. Create a new bot in @BotFather (2.1).
2. Add it to the **same** group as an administrator with the same rights (2.3).
3. Install tgsync, copy `.env` from the first node, and change `TELEGRAM_BOT_TOKEN`, `NODE_NAME` and `PROJECTS_ROOT` if needed.
4. `make install` (or the Windows/Docker equivalent).

A new topic `🖥 <new name>` appears. Nodes do not talk to each other; each serves its own topics.

### 8.6 Moving a node to another machine

1. Stop the old node (`systemctl --user stop tgsync`, or `make uninstall`). Two nodes with one token conflict.
2. Copy the node folder (`.env`, `profiles.yaml`, `data/`) to the new machine's node folder, keeping mode `0600` on `.env`.
3. To resume old sessions, also copy `~/.claude` (at least `~/.claude/projects`) and keep the projects at the same absolute paths. Otherwise just start new sessions; old topics remain as history.
4. Adjust `PROJECTS_ROOT` and `CLAUDE_CLI_PATH` if paths differ. Keep `NODE_NAME` to keep the same control topic name.
5. Install and log in to Claude Code, then run `make install`.

With the same token and database, the node reuses its existing control topic and session topics.

## 9. Security checklist

- [ ] The bot token lives only in `.env`, with mode `0600`, in a folder with mode `0700`. It is not committed anywhere (`.env` is in `.gitignore`), and not in shell history, systemd units or compose files.
- [ ] If the token leaked, revoke it: @BotFather → `/revoke` → your bot, then update `.env` and restart.
- [ ] `ALLOWED_USER_IDS` contains only your own id (and people you fully trust with shell access to this machine).
- [ ] The group is private, and only trusted people are members: everyone in it sees code, command output and file contents.
- [ ] Each node has its own bot; unused bots are removed from the group.
- [ ] The approval mode stays 🔴 (ask) unless you understand that 🟢/🟡 let the agent run any command as your user without asking.
- [ ] `SUDO_MODE=off` unless needed; prefer narrow `NOPASSWD` rules. Never put `SUDO_PASSWORD` into systemd or compose environment blocks.
- [ ] The STT server (if any) listens on `127.0.0.1` or is reachable only through a VPN/TLS.
- [ ] Backups of `.env` are encrypted.
- [ ] You read every command before pressing ✅, especially ones touching the tgsync folder or using sudo.
- [ ] Remember that Bot API traffic is not end-to-end encrypted: code and output pass through Telegram's servers.

More in [security.md](security.md).
