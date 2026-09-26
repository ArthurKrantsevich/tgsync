package i18n

// Texts of the tgsync command line (cmd/tgsync, package check), the bot
// profile (package brand) and the errors of the telegram, stt, projects,
// profiles and agent packages.
func init() {
	register(map[string]Text{
		"cli.check.header":     {EN: "tgsync %s · folder %s", RU: "tgsync %s · папка %s"},
		"cli.check.config_bad": {EN: "✗ config — %v", RU: "✗ конфиг — %v"},
		"cli.check.fix_env":    {EN: "fix .env and run check again", RU: "исправь .env и запусти check ещё раз"},
		"cli.check.config_ok":  {EN: "✓ config — .env loaded", RU: "✓ конфиг — .env прочитан"},
		"cli.check.bot_token":  {EN: "token rejected or Telegram unreachable: %v", RU: "токен не принят или Telegram недоступен: %v"},
		"cli.check.group_unavailable": {
			EN: "group %d is unreachable: the bot is not added or GROUP_CHAT_ID is wrong (%v)",
			RU: "группа %d недоступна: бот не добавлен или неверный GROUP_CHAT_ID (%v)",
		},
		"cli.check.no_topics":      {EN: "topics are not enabled in the group", RU: "в группе не включены темы (Topics)"},
		"cli.check.need_admin":     {EN: "make the bot an administrator with the “Manage topics” right", RU: "сделай бота администратором с правом «Управление темами»"},
		"cli.check.right_pin":      {EN: "“Pin messages” — for the node card", RU: "«Закрепление сообщений» — для карточки ноды"},
		"cli.check.right_info":     {EN: "“Change group info” — for the avatar and description", RU: "«Изменение профиля группы» — для аватара и описания"},
		"cli.check.right_delete":   {EN: "“Delete messages” — to delete empty topics and for 🧹 Clean up", RU: "«Удаление сообщений» — для удаления пустых тем и уборки"},
		"cli.check.rights_partial": {EN: "can manage topics; consider adding %s", RU: "управление темами есть; стоит добавить %s"},
		"cli.check.rights_ok":      {EN: "administrator, all required rights", RU: "администратор, все нужные права"},
		"cli.check.profiles":       {EN: "%s (default %s)", RU: "%s (по умолчанию %s)"},
		"cli.check.sudo_windows":   {EN: "not available on Windows, SUDO_MODE forced to off", RU: "недоступен на Windows, SUDO_MODE принудительно off"},
		"cli.check.sudo_env":       {EN: "SUDO_MODE=env: password from .env, each sudo command asks", RU: "SUDO_MODE=env: пароль из .env, каждая команда с кнопкой"},
		"cli.check.sudo_no_bot":    {EN: "SUDO_MODE=telegram: the bot is unreachable", RU: "SUDO_MODE=telegram: бот недоступен"},
		"cli.check.sudo_no_delete": {
			EN: "SUDO_MODE=telegram: give the bot the “Delete messages” right, or the password stays in the chat",
			RU: "SUDO_MODE=telegram: дай боту право «Удаление сообщений», иначе пароль останется в чате",
		},
		"cli.check.sudo_telegram": {
			EN: "SUDO_MODE=telegram: the password is asked in the topic and deleted, each sudo command asks",
			RU: "SUDO_MODE=telegram: пароль спрашивается в теме и удаляется, каждая команда с кнопкой",
		},
		"cli.check.sudo_off": {EN: "SUDO_MODE=off: commands with sudo are rejected", RU: "SUDO_MODE=off: команды с sudo отклоняются"},
		"cli.check.failed":   {EN: "there are errors, see the lines with ✗", RU: "есть ошибки, см. строки с ✗"},
		"cli.check.ok":       {EN: "All good.", RU: "Всё в порядке."},
		"cli.rights_check":   {EN: "checking the bot's rights in group %d: %w", RU: "проверка прав бота в группе %d: %w"},
		"cli.need_admin": {
			EN: "the bot must be a group administrator with the “Manage topics” right (can_manage_topics)",
			RU: "бот должен быть администратором группы с правом «Управление темами» (can_manage_topics)",
		},
		"cli.sudo_socket":  {EN: "sudo: socket %s: %w", RU: "sudo: сокет %s: %w"},
		"cli.profile_done": {EN: "Done: the bot's avatar and description are updated.", RU: "Готово: аватар и описание бота обновлены."},

		"check.name.bot":      {EN: "bot", RU: "бот"},
		"check.name.forum":    {EN: "group with topics", RU: "группа с темами"},
		"check.name.rights":   {EN: "bot rights", RU: "права бота"},
		"check.name.profiles": {EN: "profiles", RU: "профили"},
		"check.name.db":       {EN: "database folder", RU: "папка базы"},
		"check.claude_missing": {
			EN: "claude not found in PATH; install Claude Code or set CLAUDE_CLI_PATH",
			RU: "claude не найден в PATH; установи Claude Code или задай CLAUDE_CLI_PATH",
		},
		"check.claude_npm": {
			EN: "found %s (the npm wrapper), it does not start; install Claude Code for Windows with the native installer or set CLAUDE_CLI_PATH",
			RU: "найден %s (обёртка npm), он не запускается; установи Claude Code для Windows нативным установщиком или задай CLAUDE_CLI_PATH",
		},
		"check.claude_exe_missing": {
			EN: "claude.exe not found in PATH; install Claude Code or set CLAUDE_CLI_PATH",
			RU: "claude.exe не найден в PATH; установи Claude Code или задай CLAUDE_CLI_PATH",
		},
		"check.dir_missing":  {EN: "%s: folder not found", RU: "%s: папка не найдена"},
		"check.dir_readonly": {EN: "%s: no write permission", RU: "%s: нет прав на запись"},

		"brand.short": {
			EN: "Remote control for Claude Code: the agent's tasks, progress, questions and permissions, right in Telegram.",
			RU: "Пульт для Claude Code: задачи, ход работы, вопросы и разрешения агента — прямо из Telegram.",
		},
		"brand.description": {
			EN: `tgsync — remote control for Claude Code on your computer.

• Start sessions in your projects and follow the progress
• Answer the agent's questions and grant permissions with buttons
• Get the files it writes and send your own

The bot works in a group with topics: every session has its own topic. Start with /menu in the control topic.`,
			RU: `tgsync — пульт для Claude Code на твоём компьютере.

• Запускай сессии в проектах и следи за ходом работы
• Отвечай на вопросы агента и выдавай разрешения кнопками
• Получай файлы, которые он пишет, и отправляй свои

Бот работает в группе с темами: у каждой сессии своя тема. Начни с /menu в теме ноды.`,
		},
		"brand.group": {
			EN: "Remote control for Claude Code: every session has its own topic. Start with /menu in the control topic 🖥; open a new session with /new project task.",
			RU: "Пульт Claude Code: у каждой сессии своя тема. Начни с /menu в теме ноды 🖥, новую сессию открывает /new проект задача.",
		},

		"tg.download":      {EN: "download file: %w", RU: "скачать файл: %w"},
		"tg.download_http": {EN: "download file: HTTP %d", RU: "скачать файл: HTTP %d"},
		"tg.too_big":       {EN: "the file is over 20 MB — Telegram's limit for bots", RU: "файл больше 20 МБ — лимит Telegram для ботов"},
		"tg.short_desc":    {EN: "short description: %w", RU: "короткое описание: %w"},
		"tg.description":   {EN: "description: %w", RU: "описание: %w"},
		"tg.photo":         {EN: "photo: %w", RU: "фото: %w"},
		"tg.topic_gone":    {EN: "topic deleted", RU: "тема удалена"},
		"tg.message_gone":  {EN: "message deleted", RU: "сообщение удалено"},
		"tg.retry":         {EN: "telegram: temporary error: %s", RU: "telegram: временная ошибка: %s"},

		"stt.read":     {EN: "reading the response: %w", RU: "чтение ответа: %w"},
		"stt.bad_json": {EN: "unexpected server response: %w", RU: "непонятный ответ сервера: %w"},

		"projects.bad_name": {
			EN: "project name: Latin letters, digits, “.”, “_”, “-”, up to 64 characters, starting with a letter or digit",
			RU: "имя проекта: латиница, цифры, «.», «_», «-», до 64 символов, первый символ — буква или цифра",
		},
		"projects.not_found": {EN: "project %q not found in %s", RU: "проект %q не найден в %s"},
		"projects.exists":    {EN: "project %q already exists", RU: "проект %q уже существует"},

		"profiles.not_found": {EN: "profile %q not found in profiles.yaml", RU: "профиль %q не найден в profiles.yaml"},

		"agent.convert_panic": {
			EN: "tgsync: a claude message was skipped because of an internal error: %v",
			RU: "tgsync: сообщение claude пропущено из-за внутренней ошибки: %v",
		},
	})
}
