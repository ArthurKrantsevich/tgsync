**English** · [Русский](../ru/usage.md)

# User guide

The bot's interface is in English or Russian, set by `BOT_LANGUAGE` in `.env` (`en` by default, see [configuration.md](configuration.md)). This guide quotes the English labels as they appear in Telegram. When you type `/`, Telegram lists the commands with short descriptions; the ones marked "In a session" work in a session topic.

- [How the group is organised](#how-the-group-is-organised)
- [Control topic](#control-topic)
- [Session topic](#session-topic)
- [While a turn runs](#while-a-turn-runs)
- [Turn summary: diff, commit, rollback](#turn-summary-diff-commit-rollback)
- [Files](#files)
- [Voice messages](#voice-messages)
- [Permissions](#permissions)
- [Questions from the agent](#questions-from-the-agent)
- [sudo](#sudo)
- [Context, usage and limits](#context-usage-and-limits)
- [Subagents](#subagents)
- [History: continue a terminal session](#history-continue-a-terminal-session)
- [Profiles](#profiles)
- [Several nodes](#several-nodes)
- [Typical flows](#typical-flows)
- [Limitations](#limitations)

## How the group is organised

Each machine running tgsync is a **node** with its own bot. All nodes share one Telegram group with Topics enabled:

- each node has a control topic `🖥 <node name>`;
- each agent session has its own topic named `project · task`.

A node only reacts to users listed in `ALLOWED_USER_IDS`, and only in its own topics. It leaves topics of other nodes alone.

Every node uses one icon colour for its topics, so topics from different machines are easy to tell apart. The topic icon shows the session state: 💻 open, ❌ failed (send a message to continue), ✅ closed.

## Control topic

### Menu

Send `/menu` or any text to get the menu:

| Button | What it does |
|---|---|
| **📁 Projects** | lists projects; a project offers **▶ New session** or **🕘 History** |
| **🕘 History** | recent Claude Code sessions across projects (see [History](#history-continue-a-terminal-session)) |
| **🧵 Sessions** | open sessions with links to their topics |
| **➕ New project** | the bot asks for a name; reply with it (Latin letters, digits, `.`, `_`, `-`); `/cancel` aborts |
| **🔐 Auto-approve** | the node-wide approve mode (see [Permissions](#permissions)) |
| **❓ Help** | short help |

### Commands

| Command | What it does |
|---|---|
| `/menu` | the button menu |
| `/projects` | list projects; a button opens the project |
| `/newproject name` | create the project folder in `PROJECTS_ROOT` and run `git init`; without a name the bot asks for one |
| `/new project task` | new session in its own topic; the agent starts on the task right away |
| `/new project` | new session; send the task as the first message in its topic |
| `/new project --profile name task` | new session with a profile from `profiles.yaml` |
| `/profiles` | list profiles |
| `/sessions` | open sessions with links |
| `/history` | the 10 most recent Claude Code sessions across all projects |
| `/history project` | the same for one project |
| `/usage` | subscription limits (percentage, reset time) and token usage for today and the last 7 days per project |
| `/approve` | the auto-approve mode |
| `/cancel` | cancel entering a project name |
| `/help` | help |

Deleted the control topic by accident? Send `/control` in the General topic: every node re-creates its control topic if it is missing and replies with a link. A node also notices a missing control topic within about a minute and re-creates it on its own.

### Pinned card

The control topic has a pinned card with the node name, the number of open and closed sessions, and when the node started ("🟢 online since …"). It has two buttons:

- **🧹 Clean up** lists topics that can be deleted: closed sessions and sessions that failed before the agent ever answered. Nothing is deleted until you press **🗑 Delete**; **Cancel** leaves everything as is. A deleted topic takes its message history with it.
- **🧽 Clear topic** immediately deletes the service messages in the control topic (bot replies and your commands).

### Keeping the control topic tidy

- Messages in the control topic (your commands and the bot's replies) are deleted automatically after about an hour. The card and the rights notice stay.
- A new menu with buttons replaces the previous one, so there is only ever one current menu.
- The bot hides the General topic once; if you unhide it, it stays visible. It sets the group avatar and description once, and only if the group has none.
- If the bot lacks optional admin rights, it says once which rights are missing and what does not work without them.

## Session topic

A session is one conversation with Claude Code in a project folder. Whatever you write in its topic goes to the agent.

| Action | Result |
|---|---|
| plain text | a message to the agent. If a turn is running, it is queued (see below) |
| document, photo, screenshot | saved into the project and handed to the agent (see [Files](#files)) |
| voice message 🎙 | transcribed by your own STT server (see [Voice messages](#voice-messages)) |
| `/stop` | interrupt the current turn; the session stays |
| `/mode default` | Claude Code's normal mode: risky actions ask for permission |
| `/mode acceptEdits` | file edits without asking |
| `/mode plan` | planning only, no changes |
| `/ls [folder]` | browse project files with buttons: 📁 open folder, 📄 send file, ⬆ .. up, ➡ More |
| `/file path` | send a project file, e.g. `/file docs/plan.md` |
| `/skills` | commands and skills available to the agent (plugins, `/code-review`, etc.) |
| `/agents` | post the subagents panel again at the bottom of the chat |
| `/context` | how full the context is, with a **🗜 Compact** button |
| `/usage` | this session's usage: turns, tokens, estimated cost |
| `/close` | end the session and close the topic |
| `/help` | session help |
| `/any-other` | passed to the agent as is (plugin commands such as `/code-review`) |

### Queue

While the agent is working, new messages are queued for the next turn. A message that is waiting or being processed carries a 👀 reaction. The "📥 Queued" notice has a **⚡ Send now** button: it interrupts the current turn and sends that message first.

Turns of different sessions run in parallel, up to `MAX_PARALLEL_SESSIONS` per node and one per project. A session that has to wait shows ⏳.

### Stop and close

- **⏹ Stop** under the status message, or `/stop`, interrupts the turn. The session and its context stay.
- `/close` or **✖ Close**, confirmed with **Yes, close** / **Cancel**, stops the process and closes the topic. You can continue a closed session later through `/history`.
- You can also delete the topic in Telegram: the node stops the agent and closes the session itself — at once for an active session, within about 10 minutes for an idle one.
- A session closed before the agent ever answered is deleted together with its topic.

### Buttons under an answer

Each agent answer has a single **⋯** button. It expands into:

- **📂 Files** — same as `/ls`;
- **⚙ Mode** — pick the Claude Code mode (`default`, `acceptEdits`, `plan`), each with a short explanation;
- **✖ Close** — close the session, with confirmation.

## While a turn runs

The **status message** updates about every 3 seconds: elapsed time, step number, the current action, the current subagent action, and 💬 the agent's latest remark between steps. It has a **⏹ Stop** button and is removed when the turn ends.

The session state is shown as an emoji in the status message and in `/sessions`:

| Emoji | Meaning |
|---|---|
| 🔄 | the `claude` process is starting |
| ▶ | the agent is working |
| ❓ | the agent is waiting for your answer or permission |
| 💤 | the turn is over; you can write again |
| ⏳ | queued: the parallel limit or the project is busy |
| ⏸ | the node restarted during the turn |
| ❌ | error; the next message continues the session |
| ✅ | the session is closed |

The **agent's answer** is its last message of the turn, sent as one message with sound. An italic footer shows duration, number of steps, cost, and `🧠 N%` context usage.

**Long turns.** By default there is no time limit (`MAX_TURN_DURATION=0`). If a turn is silent for longer than `STALL_WARN` (20 minutes), you get a warning with **⏳ Keep waiting** and **⏹ Stop**. An unanswered permission request or question is repeated every `REMIND_EVERY` (2 hours). An idle `claude` process is shut down after `IDLE_TIMEOUT` (2 hours); the next message resumes the session with the same context.

**Plugins and hooks.** Plugins, skills and hooks from `~/.claude` work as in the terminal. If an MCP server needs authorisation, a 🔌 message appears — authorise it in an interactive `claude`. Hook messages meant for the user and hook failures appear in grey italics with 🪝. Turn them off with `SHOW_HOOK_OUTPUT=false`.

## Turn summary: diff, commit, rollback

If the agent changed files, a summary follows the answer: "📎 Changed this turn: N · +added −deleted" with up to 10 files and per-file stats: `+3 −1`, `(new)`, `(deleted)`, `(binary)`. ✓ marks files already sent to the topic.

In a git project tgsync snapshots the working tree before and after each turn (through a temporary index, so your index, branches and stash are untouched). That enables the buttons under the summary:

- **🔀 Diff** sends `turn-N.diff` with all changes of the turn.
- **✅ Commit** asks the agent to commit the turn's changes in the repository's style. If the same files also hold older uncommitted edits, the agent asks whether to include them.
- **↩ Roll back**, after confirmation (**Yes, roll back** / **No**), restores the files to their state before the turn; files created in the turn are deleted. Files changed after the turn are left alone and listed. The agent is told about the rollback with your next message.

Commit and rollback work only for the latest turn and only while the agent is idle. Buttons are kept for the last 5 turns of a session. Outside git the summary has no buttons.

## Files

### From the agent to you

- The agent sends documents meant for you (spec, plan, report) with its `send_file` tool; the system prompt tells it to.
- If the agent mentions the path of a document it created or changed in this session, the bot sends it on its own.
- Files matching `AUTO_SEND_GLOBS` (for example `**/*.md`) are sent at the end of every turn.
- `/file path` and `/ls` send any project file on request.

At most 5 files per turn are sent unasked, and the same version of a file is never sent twice. Only files inside the project folder up to 50 MB are sent; tgsync's own files are never sent.

### From you to the agent

Send a document, photo or screenshot to a session topic:

- **with a caption** — the agent gets the file path right away, with the caption as the task;
- **without a caption** — the bot replies "📎 Received …" and hands the file to the agent with your next message. You can send several files and then write the task.

Files are stored in `.tgsync/inbox/` inside the project. The folder is added to `.git/info/exclude` automatically, so it never ends up in commits. Telegram lets bots download files up to 20 MB.

## Voice messages

Voice messages work when `STT_URL` points to your own speech-to-text server (a ready `deploy/stt/compose.yaml` is included; see [setup](setup.md)). No cloud services are used.

1. Send a voice message to a session topic. The bot shows "🎙 Transcribing…" and then the transcript.
2. The agent receives the transcript, marked as possibly imperfect, restates how it understood the task and waits for confirmation.
3. Reply "yes" by text or voice, or correct it.

If the agent is waiting for a typed answer (**✍ Type answer**), the voice message becomes that answer. The maximum length is `STT_MAX_SECONDS` (300 s).

## Permissions

### Allowed without asking

In the **🔴 Ask me** mode:

- reading and searching files;
- editing files inside the project folder, except files that make git, Claude Code or the shell run commands (`.git/`, `.gitattributes`, `.gitmodules`, `.claude/`, `.envrc`, `.mcp.json`, `.vscode/tasks.json`);
- whatever you allowed earlier with **Always**;
- whatever your Claude Code settings allow (`~/.claude/settings.json`, project settings).

Everything else arrives as a "🔐 Permission request" with the command, the agent's explanation of it, and buttons:

- **✅ Allow** — once;
- **❌ Deny** — the agent is refused; you can explain why in your next message;
- **♾ Always: …** — remember for this project.

### What Always remembers

- For shell commands: the command with its first argument, e.g. `go test` or `ls -la`. Chains (`&&`, `;`, `|`, substitutions, redirections) never match a rule.
- Shells and interpreters (`bash`, `python3`, `node` …), destructive commands (`rm`, `dd`, `chmod`, `find` …), wrappers (`env`, `xargs`, `eval`, `timeout` …), `ssh`, `sudo`, and `git` with global options (`git -c …`) get no **Always** button — only one-off approval.
- For an edit outside the project: the folder of that file (not its subfolders).
- A rule such as `npm run` allows every script in `package.json`, so prefer **Always** for narrow commands.

### Auto-approve

`/approve` or **🔐 Auto-approve** in the menu sets the mode for the whole node:

| Mode | Behaviour |
|---|---|
| **🟢 Allow all** | every command runs without asking, sudo included (if sudo is enabled) |
| **🟡 All but sudo** | everything runs without asking; sudo gets a button |
| **🔴 Ask me** (default) | a button for every action the rules above do not allow |

In 🟢 and 🟡 the status line shows what the agent is doing ("▶ Running the tests"), not the command itself. Destructive commands (deleting files, `git push`, `git reset`, stopping processes, sudo, …) still leave a silent note "✅ auto: …" with the command. In every mode, questions from the agent and commands that may touch tgsync's folder (`.env`, database) still come as buttons. See [security.md](security.md) for the risks.

`/mode` in a session topic is something else: Claude Code's own mode (`default`, `acceptEdits`, `plan`) for one session.

## Questions from the agent

When the agent asks a question (AskUserQuestion), it arrives with buttons:

- tap an option;
- for multiple choice, tick options and press **Done**;
- **✍ Type answer** — your next message (or voice message) becomes the answer. `/stop`, `/close` and `/mode` still work as commands.

Several questions come one at a time ("Question 1 of 3").

## sudo

How `sudo` works depends on `SUDO_MODE` on the node (see [security.md](security.md#sudo)):

- `off` (default, and always on Windows) — sudo commands are refused and the agent asks you to run them by hand;
- `env` — the password comes from `SUDO_PASSWORD` in `.env`;
- `telegram` — the password is asked for in the topic.

Every sudo command arrives as a separate request "🔐 sudo — the command will run as root" with only ✅/❌: there is never **Always** for sudo (in the 🟢 auto-approve mode there is no button at all).

In `telegram` mode, after ✅ the bot writes "🔑 sudo password needed". Send the password as your next message; the bot deletes it from the chat right away ("🔑 Password received, message deleted."). Several sudo calls within one approved command share one answer. If the password is wrong, sudo asks again, up to 3 attempts. When the turn ends, a pending password prompt is withdrawn.

## Context, usage and limits

- **Context.** The turn footer shows `🧠 N%`. At about 70% (or earlier, if auto-compaction is configured earlier) you get "💡 Context N%" with a **🗜 Compact** button that sends `/compact` to the agent. After compaction the topic shows "🗜 History compacted". `/context` shows the details.
- **Usage.** `/usage` in a session topic shows turns, tokens and estimated cost for that session. `/usage` in the control topic shows subscription limits and usage for today and the last 7 days per project.
- **Subscription limits.** When a subscription window (5 hours, 7 days, …) nears its limit, ⚠️ with the percentage and reset time goes to the session topic and the control topic; when the limit is reached, ⛔ with sound; when it is available again, ✅ in the control topic. Each warning is sent once per window.

## Subagents

When the agent starts subagents, an **agents panel** appears: which agent, what it is working on, and who started it (nested agents appear under their caller, indented with ↳). Up to 12 agents are listed; the rest are collapsed.

Tapping an agent opens its card in the same message: time, tools, what it is doing now or its result. Buttons: **⏹ Stop**, **📄 Result** (full result as a file), **⬅ Back**.

`/agents` posts the panel again at the bottom of the chat. Background agents keep running after the turn ends; the agent processes their result in a follow-up turn ("🤖 continuing after agent …"), which does not take a `MAX_PARALLEL_SESSIONS` slot. `/stop` does not stop background agents — the bot reminds you that the panel can. `/close` stops all of them.

## History: continue a terminal session

`/history` lists recent Claude Code sessions of the projects in `PROJECTS_ROOT`, including ones started in the terminal or the desktop app. The icon shows the source: 🖥 desktop app, ⌨ terminal, 🤖 bot. Each line shows the last prompt.

Tap a session number and a topic opens with "🔗 Attached to the session …" and the last prompt. Your next message continues the session with its full context.

- If the session is **🟢 active** (changed in the last 2 minutes), Telegram continues a **copy** (fork): the original session is not changed, and two processes never write to the same history.
- If the session is already open in Telegram, the bot sends a link to its topic instead of opening a second one.
- Buttons of old lists eventually expire; run `/history` again.

## Profiles

A profile decides which settings, plugins and hooks `claude` starts with in bot sessions. The `full` profile always exists: everything from `~/.claude`, as in the terminal.

Profiles live in `profiles.yaml` next to `.env` (see `profiles.example.yaml`): setting sources, environment variables for the process, and settings overrides (for example, turning a plugin off). The profile is chosen by `/new project --profile name`, otherwise `projects.<project>.profile` in `profiles.yaml`, otherwise `DEFAULT_PROFILE`. The session starts with "🧩 profile …". Details are in [configuration.md](configuration.md).

## Several nodes

Each machine runs its own node with its own bot and control topic `🖥 <name>`, all in one group. `GROUP_CHAT_ID` is the same for every node; `NODE_NAME` differs. Each node answers only in its own topics, so send commands to the control topic of the node you want. Every node sees `/control` in General and replies with a link to its own topic. Run one node per machine: a second one with the same folder refuses to start.

## Typical flows

**A new task from your phone.** In the control topic: `/new myapp add tests for auth`. Open the new topic from the link and watch it work.

**A long task.** Leave and come back later: the turn result arrives as a notification. If the agent is waiting for permission, the status shows ❓ and the bot reminds you.

**Change course.** `/stop`, then a new message with the correction. Or send the correction and press **⚡ Send now**.

**Review and commit.** Under the turn summary: **🔀 Diff** → review → **✅ Commit**. Don't like it? **↩ Roll back**.

**The process crashed or the node restarted.** Send any message to the topic: the session continues with the same context.

## Limitations

- You cannot watch or steer a session that is running in a terminal *right now*: `/history` continues it as a copy.
- `/history` only sees projects inside `PROJECTS_ROOT`.
