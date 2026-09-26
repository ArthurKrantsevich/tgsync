## Install · Установка

**English** · [Русский ниже](#установка)

You need [Claude Code](https://docs.claude.com/en/docs/claude-code) installed and logged in (`claude` in `PATH`) and a Telegram account. Go is **not** needed.

### 1. Pick your archive

| System | Archive |
|---|---|
| Linux, x86-64 | `tgsync_{{ .Version }}_linux_amd64.tar.gz` |
| Linux, ARM (Raspberry Pi 4/5, ARM servers) | `tgsync_{{ .Version }}_linux_arm64.tar.gz` |
| macOS, Apple Silicon (M1–M4) | `tgsync_{{ .Version }}_darwin_arm64.tar.gz` |
| macOS, Intel | `tgsync_{{ .Version }}_darwin_amd64.tar.gz` |
| Windows 10/11, x86-64 | `tgsync_{{ .Version }}_windows_amd64.zip` |
| Windows on ARM | `tgsync_{{ .Version }}_windows_arm64.zip` |

Unpack it into a folder and, optionally, verify it against `checksums.txt`:

```sh
sha256sum -c --ignore-missing checksums.txt        # Linux
shasum -a 256 -c --ignore-missing checksums.txt    # macOS
tar xzf tgsync_{{ .Version }}_linux_amd64.tar.gz
```

On macOS clear the download quarantine: `xattr -d com.apple.quarantine tgsync`.

### 2. Prepare Telegram

1. Create a bot with [@BotFather](https://t.me/BotFather) (`/newbot`) and copy its token. One bot per machine.
2. Create a group and turn on **Topics** in its settings.
3. Add the bot as an **administrator**: *Manage topics* is required; *Pin messages*, *Delete messages* and *Change group info* are recommended.
4. Get your user id from [@userinfobot](https://t.me/userinfobot) and the group id (`-100…`) as described in the [setup guide, section 2](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/en/setup.md#2-telegram-bot-and-group).

### 3. Configure

In the unpacked folder:

```sh
cp .env.example .env
```

Fill at least `TELEGRAM_BOT_TOKEN`, `ALLOWED_USER_IDS`, `GROUP_CHAT_ID` and `PROJECTS_ROOT` (the folder with your projects). The bot speaks English by default; set `BOT_LANGUAGE=ru` for Russian. Then check:

```sh
./tgsync check          # Windows: .\tgsync.exe check
```

### 4. Install as a service

| System | Command (from the unpacked folder) | Runs as |
|---|---|---|
| Linux | `sh scripts/install.sh` | systemd user service |
| macOS | `sh scripts/install.sh` | launchd agent |
| Windows | `powershell -ExecutionPolicy Bypass -File scripts\install.ps1` | Task Scheduler, at logon |

The script copies the binary, moves `.env` into the config folder (`~/.config/tgsync`, `~/Library/Application Support/tgsync` or `%APPDATA%\tgsync`), runs `check` and starts the service. On Linux it also enables *linger* so the node keeps running without a login; if that step fails, run `sudo loginctl enable-linger "$USER"`.

Open the group: the node topic `🖥 <name>` with a pinned card appears. Send `/menu` there.

### Update · Uninstall

- **Update:** unpack the new archive and run the install script again. Settings, sessions and the database are kept.
- **Uninstall:** `sh scripts/uninstall.sh` or `scripts\uninstall.ps1`.

Guides: [setup](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/en/setup.md) · [configuration](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/en/configuration.md) · [usage](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/en/usage.md) · [troubleshooting](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/en/troubleshooting.md) · [security](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/en/security.md)

---

### Установка

Нужен установленный и авторизованный [Claude Code](https://docs.claude.com/en/docs/claude-code) (`claude` в `PATH`) и Telegram. Go **не** нужен.

**1. Архив.** Выбери по таблице выше: `linux_amd64` — обычный Linux, `darwin_arm64` — Mac на M-чипе, `darwin_amd64` — Mac на Intel, `windows_amd64.zip` — Windows. Распакуй в папку. Проверка по желанию: `sha256sum -c --ignore-missing checksums.txt` (на macOS `shasum -a 256 -c --ignore-missing checksums.txt`). На macOS сними карантин: `xattr -d com.apple.quarantine tgsync`.

**2. Telegram.**
1. Создай бота в [@BotFather](https://t.me/BotFather) (`/newbot`) и сохрани токен. Один бот на один компьютер.
2. Создай группу и включи в настройках **Темы**.
3. Добавь бота **администратором**: обязательно *Управление темами*; желательно *Закрепление сообщений*, *Удаление сообщений*, *Изменение профиля группы*.
4. Свой id возьми у [@userinfobot](https://t.me/userinfobot), id группы (`-100…`) — по [гайду, раздел 2](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/ru/setup.md).

**3. Настройка.** В распакованной папке `cp .env.example .env`, заполни минимум `TELEGRAM_BOT_TOKEN`, `ALLOWED_USER_IDS`, `GROUP_CHAT_ID`, `PROJECTS_ROOT` (папка с проектами). Для русского интерфейса добавь `BOT_LANGUAGE=ru` (по умолчанию английский). Проверь: `./tgsync check` (на Windows `.\tgsync.exe check`).

**4. Установка сервисом** (из распакованной папки):
- Linux и macOS: `sh scripts/install.sh` — systemd / launchd. На Linux скрипт сам включит *linger*, чтобы нода работала без входа в систему; если не вышло — `sudo loginctl enable-linger "$USER"`.
- Windows: `powershell -ExecutionPolicy Bypass -File scripts\install.ps1` — Планировщик заданий, запуск при входе.

Скрипт копирует бинарник, переносит `.env` в папку конфига, запускает `check` и стартует сервис. В группе появится тема `🖥 <имя>` с закреплённой карточкой — напиши там `/menu`.

**Обновление:** распакуй новый архив и снова запусти скрипт установки — настройки и база сохранятся. **Удаление:** `sh scripts/uninstall.sh` или `scripts\uninstall.ps1`.

Подробно: [установка](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/ru/setup.md) · [настройки](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/ru/configuration.md) · [использование](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/ru/usage.md) · [проблемы](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/ru/troubleshooting.md) · [безопасность](https://github.com/ArthurKrantsevich/tgsync/blob/main/docs/ru/security.md)
