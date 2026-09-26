# Security policy

## Supported versions

tgsync is at an early stage. Security fixes go into the latest release only.

| Version | Supported |
|---|---|
| 0.1.x | yes |
| older | no |

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub:

1. Open the repository's **Security** tab.
2. Choose **Report a vulnerability** (private vulnerability reporting / security advisories).
3. Describe the issue, the affected version or commit, the platform, and steps to reproduce. A proof of concept helps, but keep real tokens, user IDs and passwords out of it.

Do not open a public issue, pull request or discussion for a vulnerability before a fix is released.

You will get a reply in the advisory. Once the issue is confirmed, a fix is prepared in a private fork where possible, released, and the advisory is published with credit to the reporter unless you ask otherwise.

## Scope

Of particular interest:

- bypassing `ALLOWED_USER_IDS` or acting in another node's topics;
- the agent running commands or writing files that should need approval in the current approve mode;
- the agent reading or sending tgsync's own files (`.env`, the database) through its ordinary tools;
- leaks of the bot token or the sudo password, or abuse of the askpass socket;
- path traversal in file sending, `/ls`, `/file`, the inbox or rollback.

Out of scope: actions you approved yourself, anything the agent does in the 🟢/🟡 auto-approve modes (see [docs/en/security.md](docs/en/security.md) for what those modes allow), and issues in Telegram, Claude Code or third-party plugins.
