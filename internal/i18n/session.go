package i18n

// Session lifecycle texts: internal/session/manager.go and recover.go.
func init() {
	register(map[string]Text{
		"session.err.unknown":      {EN: "session not found on this node", RU: "сессия не найдена на этой ноде"},
		"session.err.already_open": {EN: "this session is already open in another topic", RU: "эта сессия уже открыта в другой теме"},
		"session.err.create_topic": {EN: "create topic", RU: "создать тему"},
		"session.err.mode":         {EN: "mode: default, acceptEdits or plan", RU: "режим: default, acceptEdits или plan"},
		"session.err.file_path":    {EN: "give a path: /file docs/plan.md", RU: "укажи путь: /file docs/plan.md"},
		"session.default_title":    {EN: "new session", RU: "новая сессия"},

		"session.internal_error":          {EN: "❌ Internal tgsync error, details are in the node log.", RU: "❌ Внутренняя ошибка tgsync, подробности в логе ноды."},
		"session.internal_error_continue": {EN: "❌ Internal tgsync error, details are in the node log. Send a message to continue.", RU: "❌ Внутренняя ошибка tgsync, подробности в логе ноды. Напиши сообщение, чтобы продолжить."},
		"session.internal_error_summary":  {EN: "❌ Internal tgsync error, details are in the node log. The turn summary was not collected.", RU: "❌ Внутренняя ошибка tgsync, подробности в логе ноды. Сводка хода не собрана."},

		"session.node_restarted": {EN: "⏸ The node restarted during a turn. Send a message to continue.", RU: "⏸ Нода перезапустилась во время хода. Напиши сообщение, чтобы продолжить."},
		"session.profile":        {EN: "🧩 profile %s", RU: "🧩 профиль %s"},
		"session.ready":          {EN: "🧵 Session ready. Describe a task and the agent will get to work. Tips: /help", RU: "🧵 Сессия готова. Напиши задачу — агент начнёт работу. Подсказки: /help"},
		"session.start_failed":   {EN: "❌ Failed to start claude: %s", RU: "❌ Не удалось запустить claude: %s"},
		"session.send_failed":    {EN: "❌ Failed to send the message to the agent: %s", RU: "❌ Не удалось отправить сообщение агенту: %s"},
		"session.message_kept":   {EN: "%s\nYour message is saved and will be sent with the next one.", RU: "%s\nСообщение сохранено и уйдёт вместе со следующим."},
		"session.claude_exited":  {EN: "❌ The claude process exited during a turn. Send a message to continue.", RU: "❌ Процесс claude завершился во время хода. Напиши сообщение, чтобы продолжить."},

		"session.nothing_running":  {EN: "Nothing is running right now.", RU: "Сейчас ничего не выполняется."},
		"session.interrupting":     {EN: "⏹ Interrupting the turn…", RU: "⏹ Прерываю ход…"},
		"session.agents_running":   {EN: "🤖 Agents are still running: %d. You can stop them in the panel (/agents).", RU: "🤖 Агенты ещё работают: %d. Остановить можно в панели (/agents)."},
		"session.mode_changed":     {EN: "⚙ Permission mode: <b>%s</b>", RU: "⚙ Режим разрешений: <b>%s</b>"},
		"session.mode_pick":        {EN: "⚙ Current permission mode: <b>%s</b>. Choose a new one:", RU: "⚙ Режим разрешений сейчас: <b>%s</b>. Выбери новый:"},
		"session.mode.default":     {EN: "default — risky actions ask for permission", RU: "default — опасные действия спрашивают разрешение"},
		"session.mode.acceptEdits": {EN: "acceptEdits — edits files without asking", RU: "acceptEdits — правки файлов без вопросов"},
		"session.mode.plan":        {EN: "plan — plan only, no changes", RU: "plan — только план, без изменений"},
		// send_file tool results go to the agent; tests pin the Russian wording.
		"session.tool.internal_error": {EN: "tgsync: internal error while sending the file", RU: "tgsync: внутренняя ошибка при отправке файла"},
		"session.tool.already_sent":   {EN: "This version of the file was already sent to the user.", RU: "Эта версия файла уже была отправлена пользователю."},
		"session.tool.sent":           {EN: "File sent to the user.", RU: "Файл отправлен пользователю."},

		"session.closed": {EN: "✅ Session closed.", RU: "✅ Сессия закрыта."},

		"session.changed":        {EN: "📎 Changed this turn: %d", RU: "📎 Изменено за ход: %d"},
		"session.more":           {EN: "…and %d more", RU: "…и ещё %d"},
		"session.button_expired": {EN: "Button expired", RU: "Кнопка устарела"},
		"session.btn.wait":       {EN: "⏳ Keep waiting", RU: "⏳ Ждать"},
		"session.btn.stop":       {EN: "⏹ Stop", RU: "⏹ Остановить"},
		"session.stall":          {EN: "⏳ No activity for %s. The agent may be waiting on a long command.", RU: "⏳ Нет активности %s. Агент может ждать долгую команду."},
		"session.overtime_failed": {EN: "⚠️ The turn is running longer than MAX_TURN_DURATION (%s), but interrupting it failed: %s. Retrying in a minute.",
			RU: "⚠️ Ход идёт дольше MAX_TURN_DURATION (%s), но прервать его не удалось: %s. Попробую снова через минуту."},
		"session.overtime":       {EN: "⏱ The turn is running longer than MAX_TURN_DURATION (%s), interrupting.", RU: "⏱ Ход идёт дольше MAX_TURN_DURATION (%s), прерываю."},
		"session.waiting_longer": {EN: "OK, still waiting", RU: "Жду дальше"},

		"session.mcp_failed":     {EN: "🔌 MCP: %s\nAuthorize these servers in an interactive claude session: OAuth can't be completed from the bot.", RU: "🔌 MCP: %s\nАвторизуй их в интерактивном claude: в сессиях бота OAuth не пройти."},
		"session.commands_later": {EN: "The command list will appear after the agent's first reply.", RU: "Список команд появится после первого ответа агента."},
		"session.commands":       {EN: "Agent commands: %d. Send any of them as a message.", RU: "Команд агента: %d. Любую можно отправить сообщением."},
		"session.file_received":  {EN: "📎 Got <code>%s</code>, it goes to the agent with your next message.", RU: "📎 Получен <code>%s</code>, передам со следующим сообщением."},
		"session.folder_empty":   {EN: "The folder is empty.", RU: "Папка пуста."},
		"session.btn.more":       {EN: "➡ More", RU: "➡ ещё"},

		"session.btn.send_now":      {EN: "⚡ Send now", RU: "⚡ Отправить сейчас"},
		"session.queued":            {EN: "📥 Queued for the next turn.", RU: "📥 В очереди, уйдёт следующим ходом."},
		"session.already_sent":      {EN: "Already sent", RU: "Уже отправлено"},
		"session.goes_first":        {EN: "📥 Moved to the front of the queue.", RU: "📥 Уйдёт первым."},
		"session.interrupt_sending": {EN: "⚡ Interrupting the turn, sending…", RU: "⚡ Прерываю ход, отправляю…"},

		"session.btn.files":        {EN: "📂 Files", RU: "📂 Файлы"},
		"session.btn.mode":         {EN: "⚙ Mode", RU: "⚙ Режим"},
		"session.btn.close":        {EN: "✖ Close", RU: "✖ Закрыть"},
		"session.btn.close_yes":    {EN: "Yes, close", RU: "Да, закрыть"},
		"session.btn.close_cancel": {EN: "Cancel", RU: "Отмена"},
		"session.already_closed":   {EN: "Session already closed", RU: "Сессия уже закрыта"},
		"session.close_confirm":    {EN: "Close the session? The process will stop and the topic will close. You can resume it later with /history.", RU: "Закрыть сессию? Процесс остановится, тема закроется. Продолжить её потом можно через /history."},
		"session.stays_open":       {EN: "The session stays open.", RU: "Сессия остаётся открытой."},
		"session.closing":          {EN: "Closing the session.", RU: "Закрываю сессию."},
	})
}
