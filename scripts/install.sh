#!/bin/sh
# Installs tgsync as a user service: systemd on Linux, launchd on macOS.
# Safe to run again to update. On Windows use scripts/install.ps1.
set -eu

REPO=$(cd "$(dirname "$0")/.." && pwd)
OS=$(uname -s)
BIN_DIR="$HOME/.local/bin"
BIN="$BIN_DIR/tgsync"
case "$OS" in
Linux)
	CONF="$HOME/.config/tgsync"
	UNIT_DIR="$HOME/.config/systemd/user"
	UNIT="$UNIT_DIR/tgsync.service"
	;;
Darwin)
	CONF="$HOME/Library/Application Support/tgsync"
	LABEL=dev.tgsync
	PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
	LOG="$HOME/Library/Logs/tgsync.log"
	;;
*)
	echo "! $OS не поддерживается этим скриптом. На Windows запусти scripts/install.ps1."
	exit 1
	;;
esac

service_pid() {
	if [ "$OS" = Linux ]; then
		systemctl --user show -p MainPID --value tgsync 2>/dev/null || echo 0
	else
		launchctl print "gui/$(id -u)/$LABEL" 2>/dev/null | awk '$1 == "pid" {print $3; exit}' || true
	fi
}
service_stop() {
	if [ "$OS" = Linux ]; then
		systemctl --user stop tgsync 2>/dev/null || true
	else
		launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
	fi
}

# Another tgsync (for example ./bin/tgsync run in a terminal) would poll with
# the same token (409 Conflict) and write to the database while it is moved.
MAIN=$(service_pid)
for pid in $(pgrep -u "$(id -un)" -x tgsync 2>/dev/null || true); do
	if [ "$pid" != "$MAIN" ]; then
		echo "! Запущен другой процесс tgsync (pid $pid), например ./bin/tgsync run в терминале."
		echo "  Останови его и запусти установку ещё раз (make install или sh scripts/install.sh)."
		exit 1
	fi
done

mkdir -p "$BIN_DIR"
if command -v go >/dev/null 2>&1 && [ -f "$REPO/go.mod" ]; then
	echo "→ сборка $BIN"
	(cd "$REPO" && go build -o "$BIN" ./cmd/tgsync)
elif [ -x "$REPO/tgsync" ]; then
	# Release archive: the binary sits next to scripts/.
	cp "$REPO/tgsync" "$BIN"
else
	echo "! Нет Go и нет готового tgsync рядом со скриптом."
	exit 1
fi

mkdir -p "$CONF/data"
chmod 700 "$CONF" "$CONF/data"
if [ ! -f "$CONF/.env" ]; then
	if [ -f "$REPO/.env" ]; then
		# Moved, not copied: a second copy of the token must not stay in a project folder.
		mv "$REPO/.env" "$CONF/.env"
		echo "→ .env перенесён в $CONF"
	else
		cp "$REPO/.env.example" "$CONF/.env"
		chmod 600 "$CONF/.env"
		echo "Заполни $CONF/.env и запусти установку ещё раз (make install или sh scripts/install.sh)."
		exit 1
	fi
fi
chmod 600 "$CONF/.env"
if [ ! -f "$CONF/data/tgsync.db" ] && [ -f "$REPO/data/tgsync.db" ]; then
	service_stop
	mv "$REPO"/data/tgsync.db* "$CONF/data/"
	chmod 600 "$CONF"/data/tgsync.db*
	echo "→ база перенесена в $CONF/data (темы сохранятся)"
fi

echo "→ проверка установки"
if ! (cd "$CONF" && TGSYNC_HOME="$CONF" "$BIN" check); then
	echo "Исправь ошибки выше и запусти установку ещё раз (make install или sh scripts/install.sh)."
	exit 1
fi

if [ "$OS" = Linux ]; then
	mkdir -p "$UNIT_DIR"
	# systemd expands % specifiers, so literal % is doubled; values are quoted
	# so paths with spaces survive. Written to a temp file and moved into place,
	# so a failure never leaves a broken unit behind.
	unit_escape() { printf '%s' "$1" | sed 's/%/%%/g'; }
	UNIT_PATH=$(unit_escape "$PATH")
	UNIT_BIN=$(unit_escape "$BIN")
	cat > "$UNIT.tmp" <<UNIT
[Unit]
Description=tgsync — Claude Code over Telegram

[Service]
Type=simple
WorkingDirectory=%h/.config/tgsync
Environment="PATH=$UNIT_PATH"
ExecStart="$UNIT_BIN" run
Restart=on-failure
RestartSec=5
TimeoutStopSec=20

[Install]
WantedBy=default.target
UNIT
	mv "$UNIT.tmp" "$UNIT"

	systemctl --user daemon-reload
	systemctl --user enable tgsync >/dev/null
	systemctl --user restart tgsync
	echo "→ сервис запущен: systemctl --user status tgsync"

	if loginctl enable-linger "$(id -un)" 2>/dev/null; then
		echo "→ linger включён: нода работает без входа в систему"
	else
		echo "! Не удалось включить linger. Нода будет работать только пока ты залогинен."
		echo "  Включи вручную: sudo loginctl enable-linger $(id -un)"
	fi
fi

if [ "$OS" = Darwin ]; then
	# plist values are XML text: escape &, < and >.
	xml() { printf '%s' "$1" | sed -e 's/&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g'; }
	mkdir -p "$(dirname "$PLIST")" "$(dirname "$LOG")"
	cat > "$PLIST.tmp" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key><string>$LABEL</string>
	<key>ProgramArguments</key>
	<array><string>$(xml "$BIN")</string><string>run</string></array>
	<key>WorkingDirectory</key><string>$(xml "$CONF")</string>
	<key>EnvironmentVariables</key>
	<dict><key>PATH</key><string>$(xml "$PATH")</string></dict>
	<key>RunAtLoad</key><true/>
	<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
	<key>ThrottleInterval</key><integer>5</integer>
	<key>StandardOutPath</key><string>$(xml "$LOG")</string>
	<key>StandardErrorPath</key><string>$(xml "$LOG")</string>
</dict>
</plist>
PLIST
	mv "$PLIST.tmp" "$PLIST"
	service_stop
	# bootout returns before the job is gone; bootstrap fails while it exists.
	i=0
	while launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1 && [ "$i" -lt 20 ]; do
		sleep 0.5
		i=$((i + 1))
	done
	launchctl bootstrap "gui/$(id -u)" "$PLIST"
	echo "→ сервис запущен: launchctl print gui/$(id -u)/$LABEL"
	echo "  логи: tail -f \"$LOG\""
	echo "  нода работает, пока ты залогинен в macOS"
fi
