#!/bin/sh
# Removes the tgsync service and binary. Configuration and database stay.
set -eu

case "$(uname -s)" in
Linux)
	systemctl --user disable --now tgsync 2>/dev/null || true
	rm -f "$HOME/.config/systemd/user/tgsync.service" "$HOME/.local/bin/tgsync"
	systemctl --user daemon-reload
	echo "Сервис удалён. Конфиг и база остались в $HOME/.config/tgsync"
	;;
Darwin)
	launchctl bootout "gui/$(id -u)/dev.tgsync" 2>/dev/null || true
	rm -f "$HOME/Library/LaunchAgents/dev.tgsync.plist" "$HOME/.local/bin/tgsync"
	echo "Сервис удалён. Конфиг и база остались в $HOME/Library/Application Support/tgsync"
	;;
*)
	echo "! На Windows запусти scripts/uninstall.ps1."
	exit 1
	;;
esac
