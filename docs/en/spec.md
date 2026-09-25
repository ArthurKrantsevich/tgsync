# tgsync — Technical Specification

This document describes tgsync as it is implemented in the current code
(`cmd/tgsync`, `internal/*`, `scripts/*`, `Dockerfile`, `.env.example`). Where
this document and the code disagree, the code is authoritative.

All identifiers below are placeholders: group `-1001234567890`, user
`123456789`, home folder `/home/user`, bot `ExampleNodeBot`.

---

## 1. Purpose, scope, non-goals

### Purpose

tgsync lets one person drive Claude Code agent sessions on their own
computers from Telegram. Each computer runs a *node*: a single Go process that
starts Claude Code through the Go Agent SDK, mirrors the agent's progress into
a Telegram forum group, and turns the agent's permission requests and
questions into inline buttons.

### Scope

- One Telegram supergroup with topics enabled, shared by any number of nodes.
- Per node: a control topic with a pinned status card, and one topic per
  agent session.
- Starting new sessions in projects under a configured root folder, and
  continuing Claude Code sessions started elsewhere (terminal, desktop app).
- Tool-permission approval, answering `AskUserQuestion`, a subagent panel,
  context and usage reporting, subscription limit notices.
- Turn summaries backed by git snapshots, with diff, commit and rollback.
- File exchange in both directions; voice messages through a self-hosted
  speech-to-text server.
- Optional `sudo` for the agent, approved per command.

### Non-goals

- Multi-tenant use. A node trusts every user in `ALLOWED_USER_IDS` fully;
  there are no roles.
- Sandboxing the agent. The agent runs as the node's OS user, with that
  user's rights. tgsync's checks reduce accidents; they are not an isolation
  boundary (see §5.9).
- Calling third-party AI or speech APIs. Model inference is done by the local
  Claude Code CLI; speech-to-text only talks to an endpoint the operator
  hosts.
- A web UI, a public API, or a relay between nodes. Nodes do not talk to each
  other; they only share the Telegram group.

---

## 2. Concepts

| Term | Meaning |
|---|---|
| **Node** | One running `tgsync` process on one machine, with its own bot token, `.env`, SQLite database and control topic. Named by `NODE_NAME` (default: hostname). |
| **Group** | A Telegram supergroup with forum topics enabled (`GROUP_CHAT_ID`, e.g. `-1001234567890`). Several nodes may share one group; each node has its own bot in it. |
| **Control topic** | The node's own topic, named `🖥 <node>`. It carries the pinned card, menus and node-level commands. Its id is stored in `kv.control_thread_id`; it is re-created if deleted. |
| **Pinned card** | A message in the control topic showing the node name, open/closed session counts and "online since", with buttons 🧹 Cleanup and 🧽 Clear topic. Refreshed every 10 s when its text changes and at least once a minute. |
| **Session topic** | One topic per agent session, named `<project> · <title>` (≤128 UTF-16 units). Its icon shows the state (💻 active, ✅ closed, ❌ failed, with fallbacks). |
| **Project** | A directory directly under `PROJECTS_ROOT` whose name matches `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`. The session's working directory. `/newproject` creates it and runs `git init`. |
| **Profile** | A named set of Claude Code settings from `profiles.yaml`: `setting_sources`, `env`, and a `settings` override passed as `--settings`. The built-in `full` profile uses `user, project, local`. Selected by `--profile`, then `projects.<name>.profile`, then `DEFAULT_PROFILE`. |
| **Turn** | One agent response cycle: a prompt is sent, events stream in, a result event ends it. A *continuation turn* is one the CLI starts by itself when a background subagent finishes. |
| **Inbox / queue** | Per-session FIFO of user messages waiting for the next turn. Across sessions, a node-wide FIFO of topics waiting for a turn slot (`MAX_PARALLEL_SESSIONS`, at most one running turn per project). |
| **Session state** | `queued`, `starting`, `running`, `waiting` (on the user), `idle`, `interrupted`, `failed`, `closed`. Persisted in `sessions.state`. |
| **Permission mode** | Claude Code's own mode for the session: `default`, `acceptEdits`, `plan` (changed by `/mode`). Passed to the CLI. |
| **Approve mode** | Node-wide tgsync setting (`/approve`): `ask` (🔴 a button for every request), `nosudo` (🟡 everything except sudo is allowed), `all` (🟢 everything, sudo included). Stored in `kv.approve_mode`, default `ask`. |
| **Rule ("Всегда" / Always)** | A per-project saved allowance created by the "♾ Всегда" button, stored in `permission_rules`. |

---

## 3. Architecture

### 3.1 Package map

| Package | Responsibility | Depends on |
|---|---|---|
| `cmd/tgsync` | Entry point: subcommands `run` (default), `check`, `profile`, `version`; askpass mode (`TGSYNC_ASKPASS=1`); home folder selection; wiring. | all below |
| `internal/config` | Parses and validates environment variables into `Config`; `FindHome` picks the node folder. | — |
| `internal/store` | SQLite persistence (schema, migrations, sessions, rules, usage, rate limits, tracked control messages, kv). | `modernc.org/sqlite` |
| `internal/telegram` | `API` interface; `Bot` (go-telegram/bot) implementation; update normalization; per-topic dispatcher; `Resilient` retry wrapper; error mapping (`ErrTopicGone`, `ErrMessageGone`, `RetryError`). | `github.com/go-telegram/bot` |
| `internal/topics` | Control topic lifecycle, session topic creation/rename/close/delete, icons, colours, topic names and links. | telegram, store |
| `internal/group` | Group decoration (hide General, avatar, description, missing-rights notice), pinned card, control-topic sweep, menu replacement, 🧹 cleanup. | telegram, store, topics, render |
| `internal/router` | Maps updates to actions: General, control topic, session topics, callbacks; commands; `/history`; voice. | session, permissions, group, topics, projects, history, stt, render, telegram |
| `internal/session` | Session manager: inbox and scheduling, starting Claude, event handling, status messages, turn end, files, turn summary buttons, subagent panel, context/usage, timers. | agent, permissions, files, render, store, telegram, topics |
| `internal/agent` | Agent abstraction (`Runner`, `Session`, `Event`, `CanUseToolFunc`); `SDKRunner` over the Go Agent SDK; SDK message conversion; a fake for tests. | `github.com/ProjAnvil/claude-agent-sdk-golang` |
| `internal/permissions` | Rule evaluation (`Evaluate`), protected paths, sensitive writes, "reach" check for tgsync's folder, Always rules, approve modes, the `Broker` (permission prompts, questions, sudo password prompt, reminders). | agent, files, store, sudo, render, telegram, `mvdan.cc/sh/v3` |
| `internal/sudo` | Detecting and rewriting sudo calls, one-time tokens, askpass socket server and client, peer checks (Linux, macOS). | `mvdan.cc/sh/v3`, `golang.org/x/sys` |
| `internal/files` | Changed-file tracker, safe file reading for upload, `/ls` path resolution, path helpers (`Abs`, `Within`, `SamePath`, `SameFile`), git snapshots, stats, diff, restore. | git binary |
| `internal/render` | Telegram HTML: escaping, Markdown subset conversion, chunking, status/result lines, agent panel, usage/context views. | agent |
| `internal/history` | Reads Claude Code transcripts under `<claude home>/projects/<encoded cwd>/*.jsonl` for `/history`. | — |
| `internal/limits` | Tracks subscription limit windows, persists them, sends notices. | store, telegram, render, agent |
| `internal/projects` | Lists, validates and creates project folders. | git binary |
| `internal/profiles` | Loads `profiles.yaml` and resolves profiles. | `gopkg.in/yaml.v3` |
| `internal/stt` | Client for an OpenAI-compatible `/v1/audio/transcriptions` endpoint. | — |
| `internal/check` | Checks run by `tgsync check` (claude CLI, folders). | — |
| `internal/brand` | Embedded avatar and bot/group description texts. | — |

### 3.2 Process model

A node is one OS process. Goroutines:

| Goroutine | Started by | Behaviour |
|---|---|---|
| Long polling | `api.Run` (main goroutine) | `getUpdates` with `allowed_updates = [message, callback_query]`. Handlers run synchronously in this goroutine and only hand the update to the dispatcher. |
| Topic worker | dispatcher, one per topic with a backlog | Handles messages of one topic in arrival order (buffer 256). Exits when the backlog is empty. A long action (e.g. voice transcription) delays later messages of the same topic only. |
| Callback handler | dispatcher, one per button press | Button presses bypass the topic queue so they stay responsive while a message is being handled. |
| Session manager ticker | `mgr.Run` | Every 3 s (`EditInterval`): idle-process timeout, stall warnings, `MAX_TURN_DURATION`, prompt reminders, status message refresh, open agent-card refresh. Every 10 min: probe idle session topics for deletion. |
| Event reader | per started Claude process | `readEvents` consumes the agent's event channel and drives the session. |
| SDK pump | per started Claude process | Reads SDK responses turn after turn and converts them to events (channel buffer 256). |
| File delivery | per finished turn | Sends auto-files and the turn summary after the slot is freed. |
| Timer notifications | per tick with work | Sends stall warnings and enforces the turn limit without blocking the ticker. |
| Group ticker | `grp.Run` | Every 10 s: refresh the card. Every 1 min: sweep control-topic messages older than 1 h. |
| Sudo askpass server | `sudoSrv.Serve`, only when `SUDO_MODE` is not `off` | Accepts connections on the unix socket; one goroutine per connection. |
| Signal watcher | `run` | On the first SIGINT/SIGTERM cancels the root context; a second signal exits immediately. |

The limits tracker has no goroutine of its own; it is called from session
event handling.

### 3.3 Data flow: user message → agent turn → Telegram output

1. **Receive.** Long polling delivers an update. `normalize` drops updates from
   other chats and from bots, and extracts user id, topic id (0 = General),
   text or caption, document/photo, voice/audio, or callback data.
2. **Dispatch.** Messages go to the topic's ordered queue; callbacks run at
   once. A panic in a handler is recovered and logged.
3. **Authorize.** `Router.Handle` ignores users not in `ALLOWED_USER_IDS`.
4. **Route.** General topic: only `/control`. Control topic: node commands
   and menus. A topic owned by one of this node's sessions: session handling.
   Any other topic belongs to another node and is ignored.
5. **Session text.** In order: a pending sudo password prompt takes the text
   (and deletes the message); known session commands run; a pending "✍ Свой
   ответ" question takes the text as the answer; otherwise the text goes to
   `Manager.MessageFrom`.
6. **Queue.** The message gets a 👀 reaction and is appended to the session
   inbox (prefixed with the list of files received without a caption). A
   session titled `новая сессия` is renamed after the first line of the text
   (≤40 characters). If a turn is running or queued, a notice "📥 В очереди"
   with a "⚡ Отправить сейчас" button is posted.
7. **Schedule.** `schedule` picks the first queued topic whose project has no
   running turn while fewer than `MAX_PARALLEL_SESSIONS` turns run. Others
   become `queued`.
8. **Start.** If the session has no Claude process, one is started (see §4.1),
   resuming `claude_session_id` when present. A git snapshot of the project is
   taken (§6). A status message with ⏹ is posted; state becomes `running`.
   The prompt is sent (prefixed with a rollback note if the previous turn was
   rolled back).
9. **Stream.** Events update the status message (throttled to one edit per
   3 s; forced on wait-state changes): step counter, current tool line,
   subagent sub-line, the agent's latest remark. Only the final text of a turn
   is posted in full; intermediate texts become the status note.
10. **Permissions.** Tool calls that the CLI asks about call `CanUseTool`
    (§4.3, §5).
11. **Finish.** On the result event: open prompts of the topic are withdrawn,
    the state becomes `idle`, the status message is deleted (edited if it
    cannot be deleted), the 👀 reaction is removed, the answer is sent in
    chunks, and a result line (`✅ Ход завершён · time · steps · $cost`) with a
    `⋯` button is appended to the last chunk or sent separately. Usage is
    recorded. The next queued turn is scheduled. Files are delivered in the
    background. Then the context usage is queried (5 s timeout) and `· 🧠 N%`
    is appended to the result line; a compaction hint may follow.

---

## 4. Agent integration

### 4.1 Starting Claude Code

`agent.SDKRunner` uses `github.com/ProjAnvil/claude-agent-sdk-golang`. The
process lives on its own context, independent of the Telegram request that
started it.

| Option | Value |
|---|---|
| CLI | `claude` from `PATH`, or `CLAUDE_CLI_PATH` |
| Working dir | the project directory |
| Resume | `sessions.claude_session_id` if set; with `Fork` (attaching a session that is active elsewhere) the SDK's fork-session option continues a copy under a new id |
| Permission mode | `sessions.mode` (`default` / `acceptEdits` / `plan`) |
| Setting sources | from the profile; `full` = `user, project, local` |
| `--settings` | the profile's `settings` map as JSON, if any |
| Environment | profile `env`, then the sudo askpass variables (§5.8) |
| System prompt | preset `claude_code` with an appended tgsync prompt (below) |
| Hook events | enabled when `SHOW_HOOK_OUTPUT=true` |
| MCP server | in-process SDK server `tgsync` with one tool, `send_file` |
| Max buffer | 16 MiB per SDK message |

Before any agent starts, the node removes `TELEGRAM_BOT_TOKEN` and
`SUDO_PASSWORD` from its own environment, so neither Claude nor any tool it
runs inherits them.

**Appended system prompt.** The agent is told that it is driven from
Telegram; that it should call `mcp__tgsync__send_file` for documents the user
should read; that only the last message of a turn is posted in full, so it
should be short and lead with the outcome; and that the Bash `description`
is shown next to the command in approval prompts, so it should say briefly,
in the user's language, why the command runs. When sudo is enabled, it is
also told that sudo works through Telegram approval and `SUDO_ASKPASS`, and
not to probe with `sudo -n`.

**`send_file` tool.** Arguments: `path` (required, relative to the project or
absolute inside it) and `caption`. It uploads the file to the session topic
(§7). A version already sent (same SHA-256) is not sent again; the tool
reports that.

### 4.2 Events handled

| SDK message | Event | Handling |
|---|---|---|
| `system/init` | `Init` | Stores the Claude session id; remembers slash commands for `/skills`; once per session object, reports MCP servers whose status is neither `connected` nor `pending`. |
| `system/compact_boundary` | `Compacted` | Posts "🗜 История сжата (авто/вручную), было N"; clears the context snapshot. |
| rate limit event | `RateLimit` | Passed to the limits tracker (§4.7). |
| task started / progress / notification / terminal update | `TaskStarted`, `TaskProgress`, `TaskDone` | Subagent panel (§4.5). Only `local_agent`, `local_workflow` and untyped tasks are tracked; shells are ignored. |
| assistant text | `Text` | Top-level only: kept as pending text; becomes the status note when the next tool call starts, or the posted answer at turn end. Subagent text is not posted. |
| assistant tool use | `ToolUse` | Status line; changed-file tracking; Agent/Task calls recorded for the panel. |
| assistant error | `Error` | Posted as `⚠️ …`. |
| user tool result | `ToolResult` | Keeps the result text of Agent calls for the panel. |
| hook response | `Hook` | The hook's `systemMessage`, or a short failure note (stderr ≤300 characters), posted as `🪝 …` when enabled. Injected context is not shown. |
| result | `Result` | Ends the turn (§3.3 step 11). |

A top-level `Text`, `ToolUse` or non-empty `Result` that arrives while no turn
is running starts a **continuation turn**: it gets a status message and a
start snapshot, but takes no `MAX_PARALLEL_SESSIONS` slot (the CLI is already
running it). A result with no text and no error outside a turn is ignored.

When the event channel closes (the CLI exited), open prompts are withdrawn,
running subagents are marked stopped, and if a turn was in progress the state
becomes `failed` with a notice. The next message starts a new process that
resumes the session.

### 4.3 Permission callback (`CanUseTool`)

Each started process gets a callback bound to its session (topic, project,
project dir, protected paths). For every call the CLI forwards:

1. `AskUserQuestion` → question flow (§4.4). Not affected by approve modes.
2. Otherwise `permissions.Evaluate` returns `Allow`, `Deny`, `Ask` or
   `Confirm` using the project's saved rules (§5.3).
3. `Allow` → allowed. `Deny` → denied with a reason the agent sees, except a
   sudo command when sudo is enabled, which becomes an approval request.
4. For sudo requests the command is test-rewritten first; a command whose
   sudo calls cannot be rewritten (e.g. sudo behind `env`, `xargs`, `sh -c`)
   is denied with an explanation.
5. If the decision is not `Confirm` and the approve mode allows it (`all`, or
   `nosudo` for non-sudo requests), the call is allowed and a silent note
   "✅ авто: …" is posted.
6. Otherwise a prompt is posted: tool name, title, and for Bash the
   description (≤300 characters) and command (≤700); for Edit the path and
   old/new strings (≤350 each); for Write the path and content (≤700); for
   other tools the JSON input (≤700). Buttons: ✅ Разрешить, ❌ Отклонить, and
   ♾ Всегда: `<rule>` when an Always rule can be built and the request is not
   sudo or `Confirm`.
7. The callback blocks until a button is pressed, the turn ends, the process
   exits or the session closes. While it waits the session state is
   `waiting`.

A deny from the user tells the agent that the user may give a reason in the
next message.

### 4.4 `AskUserQuestion`

- The input's `questions` are shown one at a time: optional header, text,
  "Вопрос i из n".
- Single choice: one button per option; a press answers.
- Multiple choice: toggle buttons (☐/☑) and "Готово"; at least one option
  must be chosen.
- Every question has "✍ Свой ответ": the next text message (or transcribed
  voice message) in the topic becomes the answer.
- After the last answer the call is allowed with `UpdatedInput` = original
  input plus `answers` (question text → answer, multiple choices joined with
  ", ").
- An input without parsable questions is allowed unchanged.

### 4.5 Subagent panel

- An `Agent`/`Task` tool call records its `subagent_type` (default `agent`)
  and its caller. When the matching task starts, it is added to the session's
  panel. Unmatched calls older than 10 min are dropped.
- A new panel message is started when a task starts and the previous panel
  has no running agent; the old panel loses its buttons.
- **List view:** running agents first, then the most recently finished, at
  most 12, shown as a tree under their calling agents; the rest are counted.
  One button per listed agent (3 per row) opens its card.
- **Card view:** name, description, state, caller, current action (the
  subagent's latest tool call), summary (≤1000 characters), start time,
  duration, tool-use count. Buttons: ⬅ Назад; ⏹ Остановить (`StopTask`) while
  running; 📄 Результат when finished — the last assistant text of the task
  transcript (only its last 8 MiB are read) or else the tool result, sent as
  `agent-<name>-<n>.md`.
- Panel edits are throttled like status messages; an open card of a running
  agent is refreshed by the ticker.
- `/agents` reposts the panel at the bottom of the topic.
- On `/stop`, running agents are not stopped; the user is told how many are
  still running. On session close all running agents are stopped. When the
  process exits, running agents are marked stopped.
- An idle process is not stopped by `IDLE_TIMEOUT` while any agent runs.

### 4.6 Context and usage

- **Usage.** The CLI reports cumulative per-model usage for the process. At
  each result the node stores the difference from the previous totals (a
  total that went down, e.g. after `/clear`, counts in full) as rows in
  `usage` (input, output, cache read, cache creation, cost).
- **Context.** After each turn `GetContextUsage` is queried (5 s timeout).
  The percentage is appended to the result line.
- **Compaction hint.** When the context crosses 70 % — or 10 points below the
  auto-compaction threshold if that is lower — a hint with a 🗜 Сжать button
  is posted once per crossing. The button queues `/compact` as a message
  (not twice).
- `/context` shows the model, totals, per-category breakdown and the
  auto-compaction threshold; with no live process it shows the last snapshot,
  marked stale.
- `/usage` in a session topic: turns, tokens, cache reads, cost, latest
  context figure. In the control topic: subscription windows and per-project
  totals for today and for the last 7 days.

### 4.7 Subscription rate limits

- Windows: `five_hour`, `seven_day`, `seven_day_opus`, `seven_day_sonnet`,
  `overage`. Statuses: `allowed`, `allowed_warning`, `rejected`.
- Every event is saved to `rate_limits`. Notices are deduplicated by
  `status|reset time`; the dedup state is rebuilt from the database at start.
- `allowed_warning` → silent notice with utilization and reset time;
  `rejected` → notice with sound; `allowed` after `rejected` → "✅ снова
  доступен" in the control topic only. Warning/rejection notices go to the
  session topic that reported them and to the control topic. If the control
  topic notice fails, it is retried on the next event.
- An event without a window name: a warning or rejection is shown but not
  stored; an unnamed `allowed` clears every limited window.

---

## 5. Permissions and security model

### 5.1 Who can control a node

- Updates from chats other than `GROUP_CHAT_ID` and from bots are dropped.
- Updates from users not in `ALLOWED_USER_IDS` are ignored silently.
- Every allowed user has full control; there are no roles.
- Callback buttons additionally check that they are pressed in the topic they
  belong to; stale buttons answer "Кнопка устарела" / "Запрос устарел".

### 5.2 Protected tgsync files

The protected list is: the absolute paths of `.env`, the database, the
database's `-wal` and `-shm` files, `profiles.yaml` and the askpass socket.
They are:

- **Denied** for Bash commands that contain the path literally (on Windows
  also with `/`, doubled slashes and Git Bash `/c/…` forms; case-folded on
  Windows and macOS);
- **Denied** for file tools (`file_path`, `notebook_path`) naming the file,
  compared after normalization, and by file identity (hard links, symlinks);
- **Denied** for tools with a `path` argument inside a protected path;
- never uploaded (`send_file`, `/file`, `/ls`, auto-send, diff) and hidden in
  `/ls`.

### 5.3 Rule evaluation (`Evaluate`)

In this order:

1. **Bash:** protected path mentioned → `Deny`. A sudo call (parsed with a
   shell parser, including `env sudo`, `xargs sudo`, `find -exec sudo` and
   `sh -c` scripts; a regex fallback for unparsable commands) → `Deny` with
   the "sudo off" reason (turned into a prompt by the broker when sudo is on).
   A command that may reach tgsync's folder (§5.5) → `Confirm`.
2. Protected file/path argument → `Deny`.
3. Read-only tools (`Read`, `Glob`, `Grep`, `LS`, `NotebookRead`,
   `TodoWrite`, `Task`, `Agent`, `Skill`, `mcp__tgsync__send_file`) → `Allow`.
4. File-write tools (`Edit`, `MultiEdit`, `Write`, `NotebookEdit`):
   - sensitive write (§5.4) → `Ask`;
   - target inside the project after resolving symlinks → `Allow`;
   - target directly in a folder saved by an Always rule for this tool (by
     name and after resolving symlinks) → `Allow`;
   - otherwise → `Ask`.
5. Any other tool: a matching saved rule → `Allow`; otherwise `Ask`.

`Confirm` requires a button in every approve mode, offers no Always button,
and saved rules do not apply to it.

### 5.4 Sensitive writes

A write is sensitive when the path, by name or after resolving symlinks, is
inside the project and:

- has a `.git` or `.claude` path component anywhere;
- is named `.gitattributes`, `.gitmodules` or `.envrc` at any depth;
- is `.mcp.json` at the project root or `.vscode/tasks.json`.

These files make git, Claude Code, direnv or an editor run commands (tgsync
itself runs `git add` for snapshots, which runs clean filters). A sensitive
write is `Ask`, never auto-allowed by a project rule, and gets no Always
button. **Note:** `Ask` is still auto-allowed in the 🟢 and 🟡 approve modes.

### 5.5 Reach check (defense in depth)

For Bash commands, `reachesTgsync` looks for spellings of tgsync's folder
that a literal check misses: `~` and `$HOME`, relative paths, globs,
variables (`$X/tgsync/.env`), `--opt=path` and `VAR=path` forms, nested
`sh -c` scripts and heredocs (up to depth 3), and `folder/file` tails such as
`tgsync/.env`. Paths inside the project are ignored unless tgsync's folder is
itself inside the project. A hit makes the request `Confirm`. For sudo
commands a hit also forces a button.

### 5.6 Approve modes

| Mode | Stored value | Behaviour |
|---|---|---|
| 🔴 По запросу | `ask` (default) | Every `Ask` request shows buttons. |
| 🟡 Всё, кроме sudo | `nosudo` | `Ask` requests are auto-allowed with a silent "✅ авто" note; sudo requests show buttons. |
| 🟢 Всё сам | `all` | `Ask` requests and sudo requests are auto-allowed (sudo still needs the password flow). |

In all modes: `Deny` stays denied, `Confirm` shows buttons, and
`AskUserQuestion` is always shown.

### 5.7 Always rules

| Tool | Saved pattern | Matches | Refused when |
|---|---|---|---|
| Bash | first word plus first argument, e.g. `go test` | the command equals the pattern or starts with `pattern + " "`, and contains no `; & | \` newline < > $(` | the command contains those characters; the first word is a shell, interpreter (`python*`, `node`, `perl`, …), destructive or wrapper command (`rm`, `dd`, `chmod`, `find`, `env`, `xargs`, `sudo`, `ssh`, …); a `VAR=value` prefix; `git` with a leading option |
| Edit / MultiEdit / Write / NotebookEdit | the absolute folder of the file | files directly in that folder (not subfolders), same tool | no path, or a sensitive write |
| Other tools | tool name only | any input of that tool | — |

Rules are per project and have no expiry. There is no command to list or
delete them; they live in the `permission_rules` table.

### 5.8 sudo

`SUDO_MODE`:

| Mode | Password source |
|---|---|
| `off` (default) | sudo commands are denied with a message asking the user to run them manually. Forced on Windows. |
| `env` | `SUDO_PASSWORD` from `.env`. |
| `telegram` | asked in the session topic; the user's reply is deleted at once (needs the "Delete messages" right; `tgsync check` fails without it). |

Flow when a sudo command is approved (by button or by the 🟢 mode):

1. A random 128-bit one-time token is generated. Every sudo call in the
   command is rewritten to `TGSYNC_SUDO_TOKEN=<token> sudo -A …`; `-n`,
   `--non-interactive`, `-S`, `--stdin` (also inside short-flag clusters) are
   removed. The rewritten command replaces the original input.
2. A grant is registered: session topic, number of sudo calls, valid for
   5 minutes.
3. The agent's environment (set when the process starts) contains
   `SUDO_ASKPASS=<tgsync binary>`, `TGSYNC_ASKPASS=1`,
   `TGSYNC_ASKPASS_SOCK=<socket>`. sudo runs the tgsync binary as askpass,
   which sends `token <token>` over the unix socket and prints the password
   it receives.
4. The server checks the caller: on Linux (`SO_PEERCRED`, `/proc`) and macOS
   (`LOCAL_PEERPID`, `sysctl`) the caller's parent must be a process named
   `sudo` running with effective uid 0. On other systems there is no peer
   check (a warning is logged at start).
5. Each new sudo process consumes one use of the grant; the same sudo process
   may ask up to 3 times (mistyped password). In `telegram` mode prompts of
   one command are serialized, and later sudo calls of the same command reuse
   the answer unless their own previous answer was wrong. The prompt waits up
   to 5 minutes.
6. When the turn ends (or the process exits, or the session closes) all
   grants and any open password prompt of the topic are revoked.

Socket: `<node folder>/askpass.sock` with mode 0600, or
`<temp>/tgsync-<hash>.sock` when that path would be ≥100 bytes. The password
is never placed in the agent's environment or context. A message starting
with `/` is never taken as the password.

### 5.9 Limits of the model (read this)

- **Auto modes are not a security boundary.** In 🟢 and 🟡 modes the agent can
  run any code as the node's OS user. That code can read `.env` (bot token,
  sudo password in `env` mode), the database and anything else the user can
  read. The literal, parsed and reach checks only make the obvious spellings
  need a tap.
- tgsync only sees the tool calls the CLI forwards to `CanUseTool`. Calls the
  CLI allows by itself (its own permission mode such as `acceptEdits`, allow
  rules in Claude Code settings, read access it grants without asking) are
  not evaluated by tgsync.
- Other files in tgsync's folder are not in the protected list; the reach check covers Bash access to the folder, but a file-tool
  write outside the project is an ordinary `Ask`.
- `SUDO_MODE=env` keeps a root-capable password on disk in `.env`.
- Anyone who can post as an allowed user in the group controls the node.

---

## 6. Turn summary (git snapshots)

### 6.1 Snapshots

`files.Snapshot(dir)`:

1. If `dir` is not in a git work tree, returns `""` (no summary buttons).
2. Uses a side index kept per repository and project directory in the user
   cache folder (`tgsync/snapshots/<hash>.index`, mode 0600). A missing one
   is seeded from a copy of the repository's index with its mtime kept;
   after that only git writes it, so its stat cache covers untracked files
   too and git's racy-entry check stays intact. Files tracked in the side
   index but now ignored (and not tracked by the user) are dropped; files
   the user tracks despite ignore rules are added. With `GIT_INDEX_FILE`
   set to it, it runs `git --literal-pathspecs add -A -- .` in the project
   directory and `git write-tree`. The tree hash is the snapshot. The
   user's index, refs, stash and working tree are not changed. Ignored
   files are excluded; only the project directory is added. A side index
   git rejects is removed and seeded again once.
3. Each git call has a 30 s timeout.

A snapshot is taken when a turn starts (and for continuation turns). At turn
end a second snapshot is taken only if the turn changed files. When a
snapshot fails, the turn summary has no figures and buttons, and the topic
gets a short note once (again only after a snapshot has worked since).

### 6.2 Changed files and statistics

- Changed files are collected from `Write`, `Edit`, `MultiEdit` and
  `NotebookEdit` calls with a path inside the project. Changes made by Bash
  commands are not collected.
- The summary message: `📎 Изменено за ход: N · +A −D`, then up to 10 files,
  each with `+a −d`, `(новый)`, `(удалён)` or `(бинарный)` and ✓ if a version
  was sent. Figures come from `git diff --numstat` / `--name-status`
  (`--no-renames --relative`) between the two snapshots.
- With stats available, the buttons are 🔀 Diff, ✅ Коммит, ↩ Откатить. The
  last 5 summaries per session keep live buttons.

### 6.3 Diff

`git diff --no-renames --relative <base> <end> -- <files>` for the listed
files minus protected ones, sent as `turn-<n>.diff` (≤50 MB). Works for any
of the last 5 summaries.

### 6.4 Commit

Commit is not done by tgsync. The button sends the agent a message asking it
to commit the listed files, giving the base tree hash so that
`git diff <base> -- <file>` shows only this turn's edits, to ask the user
about older uncommitted edits in the same files, and to follow the
repository's message style. Allowed once per summary, only for the latest
turn of an idle session.

### 6.5 Rollback

- Only for the latest turn, only when the session is idle with an empty
  inbox; requires a confirmation (Да, откатить / Нет).
- While it runs, no turn starts; a continuation turn waits for it to finish.
- A current snapshot is taken. For each file:
  - differs between the end snapshot and now (changed after the turn) → left
    as is, reported;
  - added in the turn → deleted (and now-empty parent folders removed),
    unless a `.gitignore` changed during the turn, in which case it is kept
    and reported;
  - modified, deleted or type-changed → `git restore --source=<base>
    --worktree -- <file>`;
  - not in the diff and ignored by git → reported as skipped.
- The index is not touched. The operation stops at the first error.
- The next prompt to the agent is prefixed with a note listing the restored
  files.

---

## 7. Files

### 7.1 Sending files to the user

All uploads go through `files.Read`:

- the path must resolve inside the project; a symlink's target must also be
  inside the project;
- protected files are refused;
- only regular files, at most 50 MB (Bot API upload limit);
- the file is opened without following a final symlink and without blocking
  on FIFOs (`O_NOFOLLOW | O_NONBLOCK` on Unix), and the handle must be the
  same file that was checked; at most 50 MB are read.

Sources:

| Source | Behaviour |
|---|---|
| `send_file` tool | Sent with caption; skipped if this exact version (SHA-256) was sent before in the session. |
| End of turn: mentioned documents | Files changed in the session whose path appears in the final answer and whose extension is `.md .markdown .txt .pdf .html .htm .csv .png .jpg .jpeg .svg`. |
| End of turn: `AUTO_SEND_GLOBS` | Files changed in the turn matching a glob; `**/` prefix means "in any folder". |
| `/file <path>` | Sent even if sent before. |
| `/ls` file button | Sent even if sent before. |

At most 5 files are sent automatically per turn.

### 7.2 `/ls` browser

- Lists a project folder (default: root): folders first, then files with
  sizes; 30 entries per page with "⬆ .." and "➡ ещё" buttons.
- Hidden: `.git`, `node_modules`, `.venv`, `__pycache__` and protected files.
- The path must resolve (with symlinks) inside the project.
- Navigating edits the message in place. The last 5 listing messages per
  session keep live buttons.

### 7.3 Files received from the user

- Documents and photos (largest size, saved as `photo.jpg`) in a session
  topic; refused in the control topic.
- Bot API download limit 20 MB; larger files are refused with a message.
- Stored as `<project>/.tgsync/inbox/YYYYMMDD-HHMMSS-<safe name>` (folder
  0700, file 0600). The name is reduced to letters, digits, `.`, `-`, `_`,
  at most 100 characters.
- `.tgsync/` is appended to `.git/info/exclude` when the project has a `.git`
  folder.
- With a caption: the caption is sent to the agent immediately, with the file
  list. Without: the file is announced and attached to the user's next
  message as "Пользователь прислал файлы …".

---

## 8. Voice messages

- Enabled when `STT_URL` is set. Only a self-hosted, OpenAI-compatible server
  is used (a compose file for speaches/faster-whisper is in
  `deploy/stt/compose.yaml`, listening on `127.0.0.1:8000`).
- Accepted: voice messages and audio files in a session topic. Refused: in
  the control topic, longer than `STT_MAX_SECONDS` (default 300), larger than
  20 MB.
- Request: `POST <STT_URL>/v1/audio/transcriptions`, multipart with `file`,
  `model` (`STT_MODEL`) and `response_format=json`; the response body is read
  up to 1 MB; the `text` field is used.
- Timeouts: `STT_TIMEOUT` (default 60 s) bounds download plus transcription;
  the HTTP client timeout is `STT_TIMEOUT`, or 2 minutes when it is 0.
- The transcript (≤3500 characters) is shown in an expandable quote. If a
  "✍ Свой ответ" question is pending, the transcript is its answer.
  Otherwise the agent receives the transcript with a note that it may contain
  recognition errors, the caption if any, and an instruction to restate the
  task in one or two sentences and wait for confirmation.
- Transcription runs in the topic's worker: messages sent in that topic
  meanwhile are handled after it.

---

## 9. Group management

### 9.1 Rights

- At start the node reads its admin rights (`getChatMember`). Without
  **Manage topics** the node refuses to start.
- Optional rights and what they enable:

| Right | Used for |
|---|---|
| Pin messages | pinning the card |
| Change group info | group avatar and description |
| Delete messages | deleting empty topics, 🧹 cleanup, sweep, deleting the sudo password message |

- Missing optional rights are posted once to the control topic; the notice is
  posted again only when the set of missing rights changes
  (`kv.missing_rights`).

### 9.2 One-time group setup

- Topic icon stickers are loaded (`getForumTopicIconStickers`) and mapped to
  roles: active 💻/🤖/💬, closed ✅/✔/👍/🏁, failed ❌/❗/‼/⚠.
- General is hidden once per database (`kv.general_hidden`); if the user shows
  it again, it stays visible.
- If the bot can change group info and the group has no photo/description,
  the embedded avatar and description are set once (`kv.group_profile_done`).
- Topic colour: one of the six Telegram colours, chosen by a hash of the node
  name.

### 9.3 Control topic and card

- `EnsureControl` renames the stored topic to `🖥 <node>`; if Telegram reports
  it gone, a new topic is created and stored. `/control` in General does the
  same and replies with a link.
- The card is edited in place; if its message is gone, a new one is sent and
  pinned silently; if the topic is gone, the topic is re-created first.

### 9.4 Control topic sweep and menus

- Messages the bot sends to the control topic (router replies, `/usage`,
  subscription limit notices), and the user's messages there, are recorded in `control_msgs`. Every minute,
  recorded messages older than 1 hour are deleted. Messages that cannot be
  deleted (already gone, or too old for the Bot API) are forgotten; after a
  temporary failure they are retried. One sweep is limited to 1 minute.
- 🧽 Очистить тему deletes all recorded messages now.
- Sending a new message with buttons to the control topic deletes the previous
  one (only the latest menu stays; `kv.control_menu_msg`).
- The card and the rights notice are never swept.

### 9.5 Session topics

- Created with the node's colour and the active icon.
- Renamed once, from `новая сессия` to the first message's first line.
- On close: a topic with no recorded usage and no Claude session id is
  deleted (closed instead if deletion fails); otherwise it gets the closed
  icon and is closed. Titles are not changed on close (every rename posts a
  service message).
- Failed state sets the failed icon; leaving it restores the active icon.

### 9.6 🧹 Cleanup

Candidates: closed sessions whose topic still exists, and failed sessions with
no recorded turn. The button lists up to 10 and asks for confirmation; on
confirmation the list is read again, each topic is deleted
(`deleteForumTopic`), marked `topic_deleted_at`, and any loaded session for it
is dropped. The result is reported in the same message and the card is
refreshed.

---

## 10. Persistence

### 10.1 Database

SQLite via `modernc.org/sqlite` (pure Go), opened with `journal_mode(WAL)`,
`busy_timeout(5000)` and a single connection. The folder is created with mode
0700. Default path `./data/tgsync.db` relative to the node folder.

| Table | Key columns | Purpose |
|---|---|---|
| `kv` | `key` PK, `value` | Small node settings (below). |
| `sessions` | `id` PK, `thread_id` UNIQUE, `project`, `cwd`, `title`, `claude_session_id`, `state`, `mode`, `profile`, `topic_deleted_at`, `created_at`, `updated_at` | One row per session topic. |
| `permission_rules` | PK (`project`, `tool`, `pattern`), `created_at` | Always rules. |
| `usage` | `id` PK, `at` (unix ms), `thread_id`, `project`, `model`, `input`, `output`, `cache_read`, `cache_create`, `cost` | Per-turn, per-model usage. Indexed by `at` and `thread_id`. |
| `rate_limits` | `name` PK, `status`, `utilization`, `resets_at`, `updated_at` | Last known state of each limit window. |
| `control_msgs` | `msg_id` PK, `sent_at` | Control-topic messages to sweep. |

`kv` keys: `control_thread_id`, `approve_mode`, `control_card_msg`,
`control_menu_msg`, `general_hidden`, `group_profile_done`, `missing_rights`.

### 10.2 Migrations

The schema uses `CREATE TABLE IF NOT EXISTS`. Migrations are a list of
`ALTER TABLE … ADD COLUMN` statements run at every start; "duplicate column"
errors are ignored. Current migrations add `sessions.profile` and
`sessions.topic_deleted_at`.

### 10.3 What survives a restart

| Survives | Lost |
|---|---|
| Sessions and their topics, Claude session ids, modes, profiles | Running Claude processes (resumed on the next message) |
| Approve mode, Always rules | Inbox messages not yet sent to the agent |
| Usage, rate limit windows and notice dedup | Pending permission prompts and questions (their buttons answer "Запрос устарел") |
| Control topic, card and menu message ids, sweep list | Turn summaries' buttons, `/ls` buttons, subagent panels, `/history` lists |
| Group setup flags | Sudo grants, context snapshots, "sent file" hashes |

---

## 11. Telegram API usage

### 11.1 Methods

`getUpdates` (long polling), `getMe`, `getChat`, `getChatMember`,
`sendMessage`, `editMessageText`, `editMessageReplyMarkup`, `deleteMessage`,
`setMessageReaction`, `pinChatMessage`, `sendDocument`, `getFile` (plus the
file download URL), `createForumTopic`, `editForumTopic`, `closeForumTopic`,
`deleteForumTopic`, `hideGeneralForumTopic`, `getForumTopicIconStickers`,
`setChatPhoto`, `setChatDescription`, `setMyCommands` (scope: the group),
`answerCallbackQuery`. The `profile` subcommand also uses
`setMyShortDescription`, `setMyDescription` and `setMyProfilePhoto`.

All messages use HTML parse mode with link previews disabled.

### 11.2 Errors, retries and fallbacks

- **Error mapping:** 429 → `RetryError` with `retry_after`; dial errors →
  retryable and safe; other network/URL errors → retryable but *unsafe* (the
  request may have been delivered); "message thread not found",
  `TOPIC_ID_INVALID`, `TOPIC_DELETED` → `ErrTopicGone`; "message to edit not
  found", `MESSAGE_ID_INVALID` → `ErrMessageGone`; "not modified" → success.
- **`Resilient`** wraps the API used everywhere after start. It retries
  `RetryError`s with `retry_after` or exponential backoff (1 s doubling to
  30 s), within a total budget of 5 minutes for sends, documents, topic
  create/close/delete and message deletes, and 30 s for edits and icon
  changes. Creating calls (send message, send document, create topic) are not
  retried after an unsafe error, to avoid duplicates. `answerCallbackQuery`,
  reactions, pinning, downloads and `getChatMember` are not retried.
- **Parse-error fallback:** if Telegram rejects the HTML ("can't parse
  entities"), the message, edit or caption is resent as plain text with tags
  stripped and entities unescaped.
- **Deleted topic:** any `ErrTopicGone` on a session call closes the session
  (§14).

### 11.3 Size limits

| Limit | Value |
|---|---|
| Message text | 4096 characters; answers are split into HTML chunks of ≤4000 characters, long lines cut at 800 characters; an open code block is closed and reopened across chunks |
| Answer + result line | merged into one message when the last chunk plus the result line plus 40 characters fit 4096 |
| Callback data | 64 bytes (project buttons whose data would exceed it are omitted) |
| Callback alert | 200 characters (errors cut at 190) |
| Topic name | 128 UTF-16 units, cut with "…" |
| Session title | first line of the first message, 40 characters |
| Upload | 50 MB |
| Download | 20 MB |

### 11.4 Callback data prefixes

| Prefix / format | Where | Meaning |
|---|---|---|
| `p:<id>:a` / `:d` / `:A` | session | Permission prompt: allow / deny / allow and save Always rule |
| `q:<id>:<qi>:o:<i>` | session | Question: choose option `i` (single choice) |
| `q:<id>:<qi>:t:<i>` | session | Question: toggle option `i` (multiple choice) |
| `q:<id>:<qi>:ok` | session | Question: submit multiple choice |
| `q:<id>:<qi>:w` | session | Question: answer with the next text message |
| `x:<thread>` | session | Stop the running turn (status message, stall warning) |
| `w:<thread>` | session | Stall warning: keep waiting |
| `qn:<item>` | session | Queued message: send now (move to front and interrupt) |
| `mo:<thread>` | session | Expand the `⋯` result panel |
| `ls:<thread>` | session | Open the file browser at the project root |
| `md:<thread>` | session | Show the permission mode picker |
| `ms:<thread>:<mode>` | session | Set permission mode |
| `cl:<thread>` | session | Ask to close the session |
| `cy:<thread>` / `cn:<thread>` | session | Confirm / cancel closing |
| `l:<n>` | session | `/ls`: open folder or page `n` |
| `f:<n>` | session | `/ls`: send file `n` |
| `td:<n>` | session | Turn summary: send diff |
| `tc:<n>` | session | Turn summary: ask the agent to commit |
| `tr:<n>` | session | Turn summary: ask to roll back |
| `ty:<n>` / `tn:<n>` | session | Rollback: confirm / cancel |
| `ag:<n>:c` / `:b` / `:s` / `:o` | session | Agent panel: open card / back to list / stop agent / send result |
| `cx:<thread>` | session | Compact the history (queue `/compact`) |
| `h:<n>` | control | `/history`: attach session `n` |
| `new:<project>` | control | New session in project (double press within 10 s ignored) |
| `pr:<project>` | control | Project menu |
| `ph:<project>` | control | History of one project |
| `m:<action>` | control | Main menu action: `projects`, `history`, `sessions`, `newproject`, `approve`, `help`, `menu` |
| `ap:<mode>` | control | Set approve mode: `all`, `nosudo`, `ask` |
| `cl:ask` / `cl:sweep` / `cl:yes` / `cl:no` | control (card) | 🧹 Cleanup: list / 🧽 sweep now / confirm / cancel |

Callbacks are tried in this order: permission broker (`p`, `q`), session
buttons, `h:`, control-topic buttons. `cl:` with a numeric key is a session
button; with a word it is the card's cleanup button.

---

## 12. Configuration reference

The node folder is chosen by `FindHome`: `TGSYNC_HOME` if set (must exist);
otherwise the first of the current directory, `~/.config/tgsync` and
`<user config dir>/tgsync` (`%AppData%` on Windows,
`~/Library/Application Support` on macOS) that contains `.env`; otherwise the
current directory. The process changes into that folder, loads `.env` from it
(missing is fine; variables already in the environment are not overridden)
and reads `profiles.yaml` from it. All configuration errors are reported
together.

| Variable | Type | Default | Meaning |
|---|---|---|---|
| `TELEGRAM_BOT_TOKEN` | string | — (required) | The node's bot token. Removed from the process environment after loading. |
| `ALLOWED_USER_IDS` | comma-separated positive int64 | — (required) | Telegram user ids allowed to control the node, e.g. `123456789`. |
| `GROUP_CHAT_ID` | negative int64 | — (required) | The forum supergroup, e.g. `-1001234567890`. |
| `NODE_NAME` | string | hostname | Node name in the control topic name and card. |
| `PROJECTS_ROOT` | absolute path | — (required) | Folder containing project folders, e.g. `/home/user/projects`. |
| `DB_PATH` | path | `./data/tgsync.db` | SQLite database. |
| `MAX_PARALLEL_SESSIONS` | int ≥1 | `3` | Turns running at once on this node (additionally one per project). |
| `AUTO_SEND_GLOBS` | comma-separated globs | empty | Changed files sent at the end of a turn; `**/` = any folder. |
| `IDLE_TIMEOUT` | duration, `0` = never | `2h` | Stop an idle Claude process; the next message resumes the session. |
| `STALL_WARN` | duration, `0` = off | `20m` | Warn when a turn shows no activity (not while waiting on the user). |
| `REMIND_EVERY` | duration, `0` = off | `2h` | Remind about unanswered permission prompts/questions. |
| `MAX_TURN_DURATION` | duration, `0` = no limit | `0` | Interrupt turns running longer than this. |
| `DEFAULT_PROFILE` | string | `full` | Profile used when neither `--profile` nor the project sets one. |
| `SHOW_HOOK_OUTPUT` | `true/1/yes` or `false/0/no` | `true` | Post hook `systemMessage`s and hook failures. |
| `SUDO_MODE` | `off` / `env` / `telegram` | `off` | sudo policy (§5.8). Forced to `off` on Windows. |
| `SUDO_PASSWORD` | string (not trimmed) | empty | Password for `SUDO_MODE=env` (required there). Removed from the process environment after loading. |
| `STT_URL` | `http(s)://host[:port]` | empty (voice off) | Self-hosted speech-to-text base URL. |
| `STT_MODEL` | string | `Systran/faster-whisper-small` | Model name sent to the STT server. |
| `STT_TIMEOUT` | duration, `0` = no context deadline | `60s` | Bound on download plus transcription of one voice message. |
| `STT_MAX_SECONDS` | int ≥1 | `300` | Longest accepted voice message. |
| `CLAUDE_CLI_PATH` | path | empty (`claude` from `PATH`) | Path to the Claude Code CLI. |
| `TGSYNC_HOME` | path | — | Node folder override (read before `.env`). |
| `TGSYNC_LOG` | path | — | Append stderr and logs to this file (used by the Windows task). |
| `CLAUDE_CONFIG_DIR` | path | `~/.claude` | Where `/history` reads Claude Code transcripts. |

Durations use Go syntax (`30s`, `20m`, `2h`). Internal variables set by the
node for the agent: `SUDO_ASKPASS`, `TGSYNC_ASKPASS`, `TGSYNC_ASKPASS_SOCK`,
`TGSYNC_SUDO_TOKEN`.

`profiles.yaml` format:

```yaml
profiles:
  lean:
    setting_sources: [user, project, local]   # empty → user, project, local
    env: { SOME_VAR: "value" }
    settings: { enabledPlugins: { "example@example": false } }
projects:
  some-project:
    profile: lean
```

Fixed internal values: status edit interval 3 s; topic probe every 10 min;
card refresh 10 s; sweep every 1 min for messages older than 1 h.

---

## 13. Commands reference

### 13.1 Process subcommands

| Command | Action |
|---|---|
| `tgsync` / `tgsync run` | Run the node. |
| `tgsync check` | Validate config; check the bot, forum mode, admin rights, profiles, sudo mode, claude CLI, `PROJECTS_ROOT` and database folder; exit non-zero on any error. |
| `tgsync profile` | Set the bot's avatar, short description and description. |
| `tgsync version` | Print the version. |

### 13.2 General topic

| Command | Action |
|---|---|
| `/control` | Every node re-creates its control topic if needed and replies with a link. Anything else is ignored. |

### 13.3 Control topic

| Command | Action |
|---|---|
| `/start`, `/menu` | Main menu (buttons). |
| `/projects` | Project list with buttons (project menu: new session, history). |
| `/newproject [name]` | Create a project folder with `git init`; without a name, the next message is the name. |
| `/new <project> [--profile <name>] [task]` | New session; with a task the first turn starts at once. Without arguments shows projects. |
| `/profiles` | List profiles. |
| `/sessions` | Open sessions with state and links. |
| `/history [project]` | Recent Claude Code sessions (10 per project, 10 in total, newest first) with buttons to continue them in a topic. A session modified in the last 2 minutes is continued as a fork. |
| `/usage` | Subscription windows and usage today / last 7 days by project. |
| `/approve` | Approve mode menu. |
| `/cancel` | Cancel naming a new project. |
| other `/command` | Help text. |
| plain text | Main menu (or the project name after `/newproject`). |

### 13.4 Session topic

| Command | Action |
|---|---|
| any text | Sent to the agent now or queued for the next turn. |
| `/stop` | Interrupt the running turn (also ⏹). |
| `/mode <default\|acceptEdits\|plan>` | Set the permission mode (applied to the live process first). |
| `/ls [path]` | File browser. |
| `/file <path>` | Send a project file. |
| `/skills` | Slash commands the agent offers (from the last init). |
| `/agents` | Repost the subagent panel. |
| `/context` | Context window usage. |
| `/usage` | Session usage. |
| `/close` | Close the session and its topic. |
| `/help` | Session help. |
| other `/command` | Sent to the agent as is (e.g. `/compact`). |
| file / photo | Stored in the project inbox (§7.3). |
| voice / audio | Transcribed (§8). |

The `/` menu registered with Telegram lists: menu, projects, new, history,
sessions, newproject, profiles, stop, mode, ls, file, skills, agents,
context, usage, approve, close, control, help.

---

## 14. Failure handling and recovery

| Situation | Behaviour |
|---|---|
| Node restart | `Restore` loads all non-closed sessions. Sessions that were not `idle`, `failed` or `interrupted` become `interrupted` with a notice. No Claude process is started until the next message, which resumes the session by id. |
| Claude process exits during a turn | Open prompts and sudo grants of the topic are withdrawn, running subagents marked stopped, pending text posted, state `failed` with a notice. Queued messages are rescheduled; the next message starts a new process that resumes the session. |
| Claude fails to start / a prompt cannot be sent | The message is put back at the front of the inbox, state `failed`, notice "Сообщение сохранено и уйдёт вместе со следующим". |
| Idle process | Closed after `IDLE_TIMEOUT` (not while subagents run); resumed on the next message. |
| Long silence / long turn | `STALL_WARN` notice with ⏳/⏹; `MAX_TURN_DURATION` interrupts (retried on the next tick if the interrupt fails). |
| Session topic deleted | Detected on any Telegram call (`ErrTopicGone`) or by the 10-minute probe of idle sessions (re-applying the same topic name). The session is closed: process stopped, subagents stopped, prompts withdrawn, state `closed`, `topic_deleted_at` set. |
| Control topic deleted | Re-created by the card refresh (within ~1 minute) or by `/control` in General. |
| Card deleted | A new card is sent and pinned on the next refresh. |
| Telegram unreachable or rate limiting | `Resilient` waits and retries within its budget (§11.2); the agent's events back up in buffers meanwhile. After the budget, the call fails and is logged; that output is lost. Long polling reconnects on its own. |
| Handler panic | Recovered in the dispatcher and logged with the stack; the node keeps running. Panics in other goroutines are not recovered and end the process; the service manager restarts it. |
| Shutdown (SIGINT/SIGTERM) | Claude processes are closed; session states are kept so running turns become `interrupted` on the next start. A second signal exits immediately. |
| Two processes with one token | Telegram returns 409 for polling; install scripts refuse to install while another `tgsync` runs. |

---

## 15. Platform support and build

### 15.1 Build

- Go **1.27.1** (from `go.mod`); `CGO_ENABLED=0` (pure-Go SQLite).
- `make build` → `bin/tgsync`; `make test` runs `go test -race ./...`;
  `make it` runs integration tests against a real, authorized `claude`.
- Releases (goreleaser): linux, darwin, windows × amd64, arm64; archives
  include `README.md`, `LICENSE`, `.env.example`, `profiles.example.yaml`,
  `docs/*`, `scripts/*`.
- Runtime requirements: Claude Code CLI (authorized), `git` in `PATH`
  (snapshots, project creation, rollback).

### 15.2 Platforms

| Platform | Service | Node folder | Notes |
|---|---|---|---|
| Linux | systemd user unit `tgsync.service` (`scripts/install.sh`): `Restart=on-failure`, `RestartSec=5`, `TimeoutStopSec=20`; `loginctl enable-linger` so it runs without login | `~/.config/tgsync` | Binary in `~/.local/bin`. `.env` 0600, folders 0700. Peer check via `/proc`. |
| macOS | launchd agent `dev.tgsync` (`scripts/install.sh`): `RunAtLoad`, `KeepAlive` on non-zero exit, throttle 5 s | `~/Library/Application Support/tgsync` | Logs `~/Library/Logs/tgsync.log`. Runs while the user is logged in. Peer check via `sysctl`. |
| Windows | Task Scheduler task `tgsync` at logon (`scripts/install.ps1`), hidden PowerShell loop restarting after 5 s on non-zero exit | `%AppData%\tgsync` | Binary in `%LocalAppData%\tgsync\bin`; logs via `TGSYNC_LOG`. sudo is always off. Path checks fold case and Windows/Git Bash spellings. Runs while the user is logged in. |
| Docker | `Dockerfile` (Go build stage; runtime `node:22-bookworm-slim` with Claude Code from npm, git, python3) and `docker-compose.yml` (`restart: unless-stopped`) | `/config` (`TGSYNC_HOME`) | Runs as the host user's uid/gid; mounts `~/.config/tgsync`, `~/.claude`, `~/.claude.json` and `PROJECTS_ROOT` at the same paths as on the host. No sudo in the container; keep `SUDO_MODE=off`. `EXTRA_PACKAGES` build arg for tools the projects need. |

The install scripts build from source when Go is present, otherwise use a
release binary next to the script; move `.env` (and an old database) into
the node folder; run `tgsync check` before registering the service.

---

## 16. Limitations and known issues

- **Snapshot cost.** Untracked files are not in the index copy's stat cache,
  so every snapshot re-hashes all untracked, non-ignored files of the
  project directory: twice per turn that changed files, once per other turn.
  Large untracked files make turns slower.
- **Changed-file tracking** only sees `Write`/`Edit`/`MultiEdit`/
  `NotebookEdit`. Files changed by Bash commands (generators, `sed -i`,
  formatters) are not in the turn summary, not auto-sent, and not rolled
  back.
- **Abandoned permission prompts.** A prompt stays open until it is answered
  or the turn ends, the process exits or the session closes. If the CLI
  abandons a single request mid-turn, its message keeps live buttons until
  the turn ends; pressing them then has no effect on the agent.
- **After a node restart**, prompts posted before the restart keep their
  buttons; pressing them only answers "Запрос устарел".
- **History reads only the tail.** `/history` reads the custom title and last
  prompt from the last 256 KiB of each transcript; the subagent result reads
  the last 8 MiB of its transcript. Older values beyond those windows are not
  seen.
- **One turn per project.** Two sessions in the same project never run turns
  at the same time, even with free slots.
- **Always rules** cannot be listed or removed from Telegram.
- **Sensitive writes** are auto-allowed in 🟢/🟡 modes (they are `Ask`, not
  `Confirm`).
- **Cleanup has no age threshold.** Every closed session with an existing
  topic is offered for deletion, including ones closed a minute ago.
- **Peer check** exists only on Linux and macOS; elsewhere the one-time token
  and socket permissions are the only protection (sudo is off on Windows).
- **Telegram limits:** 20 MB downloads, 50 MB uploads, messages older than
  48 hours cannot be deleted by bots (the sweep forgets them).
- **MCP servers needing OAuth** cannot be authorized from a bot session; the
  user is told to authorize them in an interactive `claude`.
- **Voice** blocks the topic's message queue while it is transcribed.
- **Not covered by panic recovery:** session event readers and tickers; a
  panic there restarts the whole node through the service manager.
