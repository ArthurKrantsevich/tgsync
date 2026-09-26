package i18n

// Texts of package router: help, menus, buttons and replies in the control
// and session topics.
func init() {
	register(map[string]Text{
		"router.help.session": {
			EN: `<b>This is a session topic</b>
Send a task or a reply and the agent gets it. If the agent is busy, your message waits in the queue.
Send a file or a screenshot and the agent reads it (the caption becomes the task).
🎙 Voice message: the agent restates the task and waits for your “yes”.

⏹ or /stop — interrupt the turn
/ls — project files · /file path — get a file
/mode — permission mode · /skills — agent commands
/agents — subagents: progress, ⏹ stop, 📄 result
/context — context usage · /usage — session usage
/close — close the session
Other /commands go to the agent as is.`,
			RU: `<b>Это тема сессии агента</b>
Пиши задачу или ответ — агент получит. Пока он работает, сообщение ждёт в очереди.
Кинь файл или скриншот — агент прочитает (подпись станет задачей).
🎙 Голосовое — агент перескажет, как понял задачу, и дождётся «да».

⏹ или /stop — прервать ход
/ls — файлы проекта · /file путь — прислать файл
/mode — режим разрешений · /skills — команды агента
/agents — субагенты: ход работы, ⏹ остановить, 📄 результат
/context — заполненность контекста · /usage — расход сессии
/close — закрыть сессию
Остальные /команды уходят агенту как есть.`,
		},
		"router.help.control": {
			EN: `<b>tgsync — remote control for Claude Code</b>

<b>Quick start</b>
/menu — everything as buttons
/new project task — start a session with a task

<b>Here, in the control topic</b>
/projects · /newproject · /sessions · /history · /profiles · /usage
/approve — auto-approve agent commands

<b>In a session topic</b>
Send a message and the agent gets to work. Send a file or a screenshot and it reads it.
🎙 Voice message: the agent restates the task and waits for your “yes”.
⏹ or /stop — interrupt · /close — close
/ls — files · /file path — get a file
/mode — permission mode · /skills — agent commands

Control topic gone? Send /control in General.`,
			RU: `<b>tgsync — пульт для Claude Code</b>

<b>Быстрый старт</b>
/menu — всё кнопками
/new проект задача — новая сессия сразу с задачей

<b>Здесь, в теме ноды</b>
/projects · /newproject · /sessions · /history · /profiles · /usage
/approve — автоодобрение команд агента

<b>В теме сессии</b>
Пиши — агент работает. Файл или скриншот — он прочитает.
🎙 Голосовое — агент перескажет задачу и дождётся «да».
⏹ или /stop — прервать · /close — закрыть
/ls — файлы · /file путь — прислать файл
/mode — режим · /skills — команды агента

Тема ноды пропала? Напиши /control в общей теме.`,
		},

		"router.control_link":     {EN: `🖥 Node control: <a href="%s">%s</a>`, RU: `🖥 Управление нодой: <a href="%s">%s</a>`},
		"router.cancelled":        {EN: "Canceled.", RU: "Отменено."},
		"router.session_created":  {EN: `🧵 Session created: <a href="%s">%s</a>`, RU: `🧵 Сессия создана: <a href="%s">%s</a>`},
		"router.no_projects":      {EN: "No projects yet. Create one: /newproject &lt;name&gt;", RU: "Проектов нет. Создай: /newproject &lt;name&gt;"},
		"router.projects_title":   {EN: "📁 <b>Projects</b> — pick a project", RU: "📁 <b>Проекты</b> — выбери проект"},
		"router.no_sessions":      {EN: "No open sessions.", RU: "Открытых сессий нет."},
		"router.sessions_title":   {EN: "<b>Sessions</b>", RU: "<b>Сессии</b>"},
		"router.error_toast":      {EN: "Error: %s", RU: "Ошибка: %s"},
		"router.session_creating": {EN: "This session is already starting", RU: "Сессия уже создаётся"},
		"router.unknown_button":   {EN: "Unknown button", RU: "Неизвестная кнопка"},

		"router.history.none":    {EN: "No sessions found.", RU: "Сессий не найдено."},
		"router.history.title":   {EN: "<b>Claude Code sessions</b> — tap a number to continue in Telegram", RU: "<b>Сессии Claude Code</b> — нажми номер, чтобы продолжить в Telegram"},
		"router.history.active":  {EN: " · 🟢 active", RU: " · 🟢 активна"},
		"router.history.stale":   {EN: "This list is outdated, run /history again", RU: "Список устарел, повтори /history"},
		"router.history.busy":    {EN: "This session is already being attached", RU: "Сессия уже подключается"},
		"router.history.open":    {EN: `This session is already open: <a href="%s">%s</a>`, RU: `Сессия уже открыта: <a href="%s">%s</a>`},
		"router.attach.title":    {EN: "🔗 Attached to session “%s”", RU: "🔗 Подключено к сессии «%s»"},
		"router.attach.last":     {EN: "\nLast prompt: %s", RU: "\nПоследний запрос: %s"},
		"router.attach.fork":     {EN: "\n\n🟢 This session is active elsewhere right now, so a copy of it continues here; the original stays unchanged.", RU: "\n\n🟢 Сессия сейчас активна в другом месте, поэтому здесь продолжится её копия: исходная сессия не изменится."},
		"router.attach.continue": {EN: "\n\nSend a message to continue.", RU: "\n\nНапиши сообщение, чтобы продолжить."},
		"router.attach.link":     {EN: `🔗 Session attached: <a href="%s">%s</a>`, RU: `🔗 Сессия подключена: <a href="%s">%s</a>`},

		"router.ago.now":       {EN: "just now", RU: "только что"},
		"router.ago.n.minutes": {EN: "%d min ago|%d min ago", RU: "%d мин назад|%d мин назад|%d мин назад"},
		"router.ago.n.hours":   {EN: "%d hour ago|%d hours ago", RU: "%d ч назад|%d ч назад|%d ч назад"},
		"router.ago.n.days":    {EN: "%d day ago|%d days ago", RU: "%d дн назад|%d дн назад|%d дн назад"},

		"router.profiles": {
			EN: "<b>Profiles</b>: %s\nNew session with a profile: /new &lt;project&gt; --profile &lt;name&gt; [task]\nProfiles are defined in profiles.yaml next to .env.",
			RU: "<b>Профили</b>: %s\nНовая сессия с профилем: /new &lt;project&gt; --profile &lt;имя&gt; [задача]\nПрофили задаются в profiles.yaml рядом с .env.",
		},
		"router.file.control": {EN: "Send files in a session topic: the agent can read them there.", RU: "Файлы принимаются в теме сессии: там агент сможет их прочитать."},
		"router.file.too_big": {EN: "⚠️ The file is over 20 MB, and Telegram does not let bots download files that large. Put it into the project another way.", RU: "⚠️ Файл больше 20 МБ — Telegram не даёт ботам скачивать такие. Положи его в проект другим способом."},

		"router.cmd.menu":       {EN: "Main menu", RU: "Главное меню ноды"},
		"router.cmd.projects":   {EN: "Projects", RU: "Проекты"},
		"router.cmd.new":        {EN: "New session: /new project task", RU: "Новая сессия: /new проект задача"},
		"router.cmd.history":    {EN: "Continue a session from the terminal or the app", RU: "Продолжить сессию из терминала или приложения"},
		"router.cmd.sessions":   {EN: "Open sessions", RU: "Открытые сессии"},
		"router.cmd.newproject": {EN: "Create a project", RU: "Создать проект"},
		"router.cmd.profiles":   {EN: "Plugin profiles", RU: "Профили плагинов"},
		"router.cmd.stop":       {EN: "In a session: interrupt the turn", RU: "В сессии: прервать ход"},
		"router.cmd.mode":       {EN: "In a session: permission mode", RU: "В сессии: режим разрешений"},
		"router.cmd.ls":         {EN: "In a session: project files", RU: "В сессии: файлы проекта"},
		"router.cmd.file":       {EN: "In a session: get a project file, /file path", RU: "В сессии: прислать файл, /file путь"},
		"router.cmd.skills":     {EN: "In a session: agent commands", RU: "В сессии: команды агента"},
		"router.cmd.agents":     {EN: "In a session: subagents", RU: "В сессии: субагенты"},
		"router.cmd.context":    {EN: "In a session: context usage", RU: "В сессии: заполненность контекста"},
		"router.cmd.usage":      {EN: "Claude usage and subscription limits", RU: "Расход Claude и лимиты подписки"},
		"router.cmd.approve":    {EN: "Auto-approve agent commands", RU: "Автоодобрение команд агента"},
		"router.cmd.close":      {EN: "In a session: close the session", RU: "В сессии: закрыть сессию"},
		"router.cmd.control":    {EN: "In General: find the control topic", RU: "В общей теме: найти тему управления"},
		"router.cmd.help":       {EN: "Help", RU: "Справка"},

		"router.btn.projects":      {EN: "📁 Projects", RU: "📁 Проекты"},
		"router.btn.history":       {EN: "🕘 History", RU: "🕘 История"},
		"router.btn.sessions":      {EN: "🧵 Sessions", RU: "🧵 Сессии"},
		"router.btn.new_project":   {EN: "➕ New project", RU: "➕ Новый проект"},
		"router.btn.approve":       {EN: "🔐 Auto-approve", RU: "🔐 Автоодобрение"},
		"router.btn.help":          {EN: "❓ Help", RU: "❓ Справка"},
		"router.btn.menu":          {EN: "🏠 Menu", RU: "🏠 Меню"},
		"router.btn.new_session":   {EN: "▶ New session", RU: "▶ Новая сессия"},
		"router.btn.back_projects": {EN: "⬅ Projects", RU: "⬅ Проекты"},

		"router.approve": {
			EN: "🔐 <b>Auto-approve commands</b>\nNow: <b>%s</b>\n\n" +
				"🟢 Allow all — every command runs without asking, sudo included\n" +
				"🟡 All but sudo — sudo still asks\n" +
				"🔴 Ask me — every command asks\n\n" +
				"Agent questions and commands that touch the tgsync folder always ask, in every mode. " +
				"Still, in 🟢 and 🟡 the agent can run any code as your user, so this is no hard barrier: tgsync's own files are not fully protected there.",
			RU: "🔐 <b>Автоодобрение команд</b>\nСейчас: <b>%s</b>\n\n" +
				"🟢 Всё сам — любые команды без вопросов, включая sudo\n" +
				"🟡 Всё, кроме sudo — на sudo придёт кнопка\n" +
				"🔴 По запросу — кнопка на каждую команду\n\n" +
				"Вопросы агента и команды, которые обращаются к папке tgsync, приходят кнопкой в любом режиме. " +
				"Но в режимах 🟢 и 🟡 агент может запустить любой код от твоего пользователя, так что это не стена: служебные файлы tgsync там защищены не полностью.",
		},
		"router.menu":         {EN: "🖥 <b>%s</b> — what's next?", RU: "🖥 <b>%s</b> — что делаем?"},
		"router.project_menu": {EN: "📁 <b>%s</b>\nNew session — then send the task in its topic. Or start with a task: /new %s task", RU: "📁 <b>%s</b>\nНовая сессия — задачу напишешь в её теме. С задачей сразу: /new %s задача"},
		"router.ask_project_name": {
			EN: "➕ What should the project be called? Send the name as a message: Latin letters, digits, “.”, “_”, “-”. To cancel, send /cancel.",
			RU: "➕ Как назвать проект? Отправь имя сообщением: латиница, цифры, «.», «_», «-». Отмена — /cancel.",
		},
		"router.bad_project_name": {EN: "%v. Try another name or /cancel", RU: "%v. Попробуй другое имя или /cancel"},
		"router.project_created":  {EN: "📁 Project <b>%s</b> created\n%s", RU: "📁 Создан проект <b>%s</b>\n%s"},

		"router.voice.control":    {EN: "🎙 Voice messages work in a session topic.", RU: "🎙 Голосовые работают в теме сессии."},
		"router.voice.no_stt":     {EN: "🎙 Speech recognition is not configured (STT_URL).", RU: "🎙 Распознавание не настроено (STT_URL)."},
		"router.voice.too_long":   {EN: "🎙 Too long: %d s, the maximum is %d s.", RU: "🎙 Слишком длинное: %d с, максимум %d с."},
		"router.voice.too_big":    {EN: "⚠️ The audio is over 20 MB, and Telegram does not let bots download files that large.", RU: "⚠️ Аудио больше 20 МБ — Telegram не даёт ботам скачивать такие."},
		"router.voice.working":    {EN: "🎙 Transcribing…", RU: "🎙 Распознаю…"},
		"router.voice.empty":      {EN: "🎙 Couldn't make that out, please say it again.", RU: "🎙 Не расслышал, повтори."},
		"router.voice.recognized": {EN: "🎙 Transcript:\n<blockquote expandable>%s</blockquote>", RU: "🎙 Распознано:\n<blockquote expandable>%s</blockquote>"},
	})
}
