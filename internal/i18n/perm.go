package i18n

// Permission prompts, approve modes, the sudo password flow and the deny
// reasons the agent reads.
func init() {
	register(map[string]Text{
		// Permission prompt.
		"perm.request":        {EN: "🔐 <b>Permission request</b>: %s", RU: "🔐 <b>Запрос разрешения</b>: %s"},
		"perm.sudo_root":      {EN: "🔐 <b>sudo</b> — the command will run as root\n", RU: "🔐 <b>sudo</b> — команда выполнится с правами root\n"},
		"perm.btn.allow":      {EN: "✅ Allow", RU: "✅ Разрешить"},
		"perm.btn.deny":       {EN: "❌ Deny", RU: "❌ Отклонить"},
		"perm.btn.always":     {EN: "♾ Always: %s", RU: "♾ Всегда: %s"},
		"perm.always_in":      {EN: "%s in %s", RU: "%s в %s"},
		"perm.reach":          {EN: "the command may touch the tgsync folder (.env, database), so it asks in every mode", RU: "команда может обращаться к папке tgsync (.env, база) — нужна кнопка в любом режиме"},
		"perm.session_ended":  {EN: "⌛ Session ended, request withdrawn", RU: "⌛ Сессия завершена, запрос снят"},
		"perm.withdrawn":      {EN: "⌛ Request withdrawn", RU: "⌛ Запрос снят"},
		"perm.stale":          {EN: "Request expired", RU: "Запрос устарел"},
		"perm.allowed":        {EN: "✅ Allowed", RU: "✅ Разрешено"},
		"perm.allowed_always": {EN: "✅ Always allowed", RU: "✅ Разрешено всегда"},
		"perm.unparsed":       {EN: "⚠️ Could not parse the command, denied", RU: "⚠️ Команду не удалось разобрать, отклонено"},
		"perm.rule_not_saved": {EN: "⚠️ rule not saved: %s\n", RU: "⚠️ правило не сохранено: %s\n"},
		"perm.denied":         {EN: "❌ Denied. You can send the reason as your next message.", RU: "❌ Отклонено. Причину можно написать следующим сообщением."},
		"perm.remind":         {EN: "⏰ Waiting for your answer to the request above.", RU: "⏰ Жду твоего ответа на запрос выше."},
		"perm.auto":           {EN: "✅ auto: %s", RU: "✅ авто: %s"},

		// Questions (AskUserQuestion).
		"perm.question_of":     {EN: "\n<i>Question %d of %d</i>", RU: "\n<i>Вопрос %d из %d</i>"},
		"perm.btn.own_answer":  {EN: "✍ Type answer", RU: "✍ Свой ответ"},
		"perm.btn.done":        {EN: "Done", RU: "Готово"},
		"perm.pick_one":        {EN: "Pick at least one option", RU: "Выбери хотя бы один вариант"},
		"perm.awaiting_answer": {EN: "✍ Send your answer as the next message", RU: "✍ Жду ответ следующим сообщением"},

		// Approve modes.
		"perm.mode.all":     {EN: "🟢 Allow all", RU: "🟢 Всё сам"},
		"perm.mode.no_sudo": {EN: "🟡 All but sudo", RU: "🟡 Всё, кроме sudo"},
		"perm.mode.ask":     {EN: "🔴 Ask me", RU: "🔴 По запросу"},
		"perm.mode.unknown": {EN: "unknown mode: %s", RU: "неизвестный режим: %s"},

		// sudo password in Telegram.
		"perm.pw.busy":          {EN: "already waiting for a password in this topic", RU: "уже жду пароль в этой теме"},
		"perm.pw.ask":           {EN: "🔑 sudo password needed. Send it as your next message — I'll delete it from the chat right away.", RU: "🔑 Нужен пароль sudo. Отправь его следующим сообщением — я сразу удалю его из чата."},
		"perm.pw.got":           {EN: "🔑 Password received, message deleted.", RU: "🔑 Пароль получен, сообщение удалено."},
		"perm.pw.got_undeleted": {EN: "🔑 Password received, but the message could not be deleted — delete it yourself and give the bot the “Delete messages” right.", RU: "🔑 Пароль получен, но удалить сообщение не удалось — удали его вручную и дай боту право «Удаление сообщений»."},
		"perm.pw.timeout_note":  {EN: "⌛ No password received, the sudo command was not run.", RU: "⌛ Пароль не получен, sudo-команда не выполнена."},
		"perm.pw.timeout":       {EN: "password not received in time", RU: "пароль не получен вовремя"},

		// Deny reasons returned to the agent.
		"perm.agent.denied":       {EN: "The user denied this action. They may give the reason in their next message.", RU: "Пользователь отклонил это действие. Причину он может написать следующим сообщением."},
		"perm.agent.protected":    {EN: "access to tgsync's own files is forbidden", RU: "доступ к служебным файлам tgsync запрещён"},
		"perm.agent.sudo_windows": {EN: "sudo is not available on Windows. Ask the user to run the command manually.", RU: "sudo недоступен на Windows. Попроси пользователя выполнить команду вручную."},
		"perm.agent.sudo_off":     {EN: "sudo is not available: SUDO_MODE=off on this node. Ask the user to run the command manually.", RU: "sudo недоступен: на этой ноде SUDO_MODE=off. Попроси пользователя выполнить команду вручную."},
		"perm.agent.sudo_wrapped": {EN: "tgsync: sudo is supported only as a direct call (sudo command …), " +
			"not through env, xargs, find -exec, sh -c or similar wrappers. Rewrite the command.",
			RU: "tgsync: sudo поддерживается только прямым вызовом (sudo команда …), " +
				"не через env, xargs, find -exec, sh -c и подобные обёртки. Перепиши команду."},

		// Package sudo: command rewriting and the askpass socket.
		"sudo.askpass_override": {EN: "tgsync: a sudo command must not set SUDO_ASKPASS or PATH, " +
			"declare a function or alias named sudo, or call sudo from outside a system folder (./sudo, /tmp/…/sudo): " +
			"tgsync supplies its own askpass program. Rewrite the command.",
			RU: "tgsync: в sudo-команде нельзя задавать SUDO_ASKPASS или PATH, " +
				"объявлять функцию или alias с именем sudo и вызывать sudo не из системной папки (./sudo, /tmp/…/sudo): " +
				"tgsync сам подставляет свою программу-askpass. Перепиши команду."},
		"sudo.parse":       {EN: "could not parse the command: %v", RU: "не удалось разобрать команду: %v"},
		"sudo.no_exe":      {EN: "tgsync: cannot find own executable for askpass: %v", RU: "tgsync: не найден свой исполняемый файл для askpass: %v"},
		"sudo.quote":       {EN: "tgsync: cannot write the askpass path into the command: %v", RU: "tgsync: путь askpass не записать в команду: %v"},
		"sudo.not_sudo":    {EN: "denied: the request is not from sudo", RU: "отказано: запрос не от sudo"},
		"sudo.no_grant":    {EN: "no approved sudo command with this token, or no attempts left", RU: "нет одобренной sudo-команды с таким токеном или попытки кончились"},
		"sudo.off":         {EN: "sudo is off", RU: "sudo выключен"},
		"sudo.unreachable": {EN: "tgsync askpass: node unreachable: %v", RU: "tgsync askpass: нода недоступна: %v"},
	})
}
