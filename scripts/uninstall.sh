#!/bin/sh
# Removes the tgsync service and binary. Configuration and database stay.
set -eu

# Interface language of the messages below: BOT_LANGUAGE from the environment,
# else the last BOT_LANGUAGE= line of the node's .env, else en.
ui_lang() {
	v=${BOT_LANGUAGE:-}
	if [ -z "$v" ] && [ -f "$1" ]; then
		v=$(sed -n 's/^[[:space:]]*\(export[[:space:]][[:space:]]*\)\{0,1\}BOT_LANGUAGE[[:space:]]*=//p' "$1" 2>/dev/null |
			tail -n 1 | sed 's/#.*//' | tr -d "\"' \r\t") || v=
	fi
	case "$v" in [Rr][Uu]) echo ru ;; *) echo en ;; esac
}
# msg "English" "Русский" prints the line in the interface language.
msg() { if [ "$UI_LANG" = ru ]; then printf '%s\n' "$2"; else printf '%s\n' "$1"; fi; }

case "$(uname -s)" in
Linux)
	UI_LANG=$(ui_lang "$HOME/.config/tgsync/.env")
	systemctl --user disable --now tgsync 2>/dev/null || true
	rm -f "$HOME/.config/systemd/user/tgsync.service" "$HOME/.local/bin/tgsync"
	systemctl --user daemon-reload
	msg "Service removed. Configuration and database remain in $HOME/.config/tgsync" \
		"Сервис удалён. Конфиг и база остались в $HOME/.config/tgsync"
	;;
Darwin)
	UI_LANG=$(ui_lang "$HOME/Library/Application Support/tgsync/.env")
	launchctl bootout "gui/$(id -u)/dev.tgsync" 2>/dev/null || true
	rm -f "$HOME/Library/LaunchAgents/dev.tgsync.plist" "$HOME/.local/bin/tgsync"
	msg "Service removed. Configuration and database remain in $HOME/Library/Application Support/tgsync" \
		"Сервис удалён. Конфиг и база остались в $HOME/Library/Application Support/tgsync"
	;;
*)
	UI_LANG=$(ui_lang "")
	msg "! On Windows run scripts/uninstall.ps1." "! На Windows запусти scripts/uninstall.ps1."
	exit 1
	;;
esac
