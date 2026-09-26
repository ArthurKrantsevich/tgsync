// Package router maps Telegram updates to node actions.
package router

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/group"
	"github.com/ArthurKrantsevich/tgsync/internal/history"
	"github.com/ArthurKrantsevich/tgsync/internal/permissions"
	"github.com/ArthurKrantsevich/tgsync/internal/projects"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/session"
	"github.com/ArthurKrantsevich/tgsync/internal/stt"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

// Router handles the control topic and the node's session topics.
// Other topics belong to other nodes and are ignored.
type Router struct {
	Allowed  func(userID int64) bool
	API      telegram.API
	ChatID   int64
	Topics   *topics.Manager
	Projects projects.Registry
	Sessions *session.Manager
	Broker   *permissions.Broker
	// Group runs the 🧹 cleanup buttons of the control card; nil disables them.
	Group *group.Group
	// ClaudeHome is ~/.claude; /history reads transcripts from it.
	ClaudeHome string
	// ProfileNames lists profiles from profiles.yaml for /profiles.
	ProfileNames func() []string
	// STT transcribes voice messages; nil disables them.
	STT stt.Transcriber
	// STTTimeout bounds download plus transcription; 0 means no limit.
	STTTimeout time.Duration
	// STTMaxSeconds is the longest accepted voice message; 0 means no limit.
	STTMaxSeconds int

	mu         sync.Mutex
	seq        int
	picks      map[string]pick      // history buttons: h:<n> → session
	attaching  map[string]bool      // Claude session ids being attached now
	pressed    map[string]time.Time // recent presses: msgID:data → when
	askingName bool                 // ➕ Новый проект: the next text is the name
}

// maxPicks is how many /history buttons stay valid; older ones say the list
// is stale.
const maxPicks = 50

// pick is a session offered by /history.
type pick struct {
	project, dir string
	entry        history.Entry
}

const helpSession = `<b>Это тема сессии агента</b>
Пиши задачу или ответ — агент получит. Пока он работает, сообщение ждёт в очереди.
Кинь файл или скриншот — агент прочитает (подпись станет задачей).
🎙 Голосовое — агент перескажет, как понял задачу, и дождётся «да».

⏹ или /stop — прервать ход
/ls — файлы проекта · /file путь — прислать файл
/mode — режим разрешений · /skills — команды агента
/agents — субагенты: ход работы, ⏹ остановить, 📄 результат
/context — заполненность контекста · /usage — расход сессии
/close — закрыть сессию
Остальные /команды уходят агенту как есть.`

const helpControl = `<b>tgsync — пульт для Claude Code</b>

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

Тема ноды пропала? Напиши /control в общей теме.`

var (
	cmdRe  = regexp.MustCompile(`(?s)^/([A-Za-z_]+)(?:@\S+)?\s*(.*)$`)
	argsRe = regexp.MustCompile(`(?s)^(\S+)\s*(.*)$`)
)

func parseCommand(text string) (cmd, args string, ok bool) {
	m := cmdRe.FindStringSubmatch(text)
	if m == nil {
		return "", "", false
	}
	return strings.ToLower(m[1]), strings.TrimSpace(m[2]), true
}

// Handle processes one update.
func (r *Router) Handle(ctx context.Context, u telegram.Update) {
	if !r.Allowed(u.UserID) {
		return
	}
	if u.CallbackID != "" {
		r.callback(ctx, u)
		return
	}
	if u.ThreadID == r.Topics.Control() && u.MessageID != 0 && r.Group != nil {
		r.Group.Track(ctx, u.MessageID) // the user's commands go with the bot's answers
	}
	if u.File != nil {
		r.file(ctx, u)
		return
	}
	if u.Voice != nil {
		r.voice(ctx, u)
		return
	}
	text := strings.TrimSpace(u.Text)
	if text == "" {
		return
	}
	switch {
	case u.ThreadID == 0:
		r.general(ctx, text)
	case u.ThreadID == r.Topics.Control():
		r.control(ctx, text)
	case r.Sessions.Owns(u.ThreadID):
		if r.Broker.HandlePassword(ctx, u.ThreadID, u.MessageID, u.Text) {
			return
		}
		r.session(ctx, u.ThreadID, u.MessageID, text)
	}
}

// general answers /control in the General topic: every node replies with a
// link to its control topic and recreates the topic if it was deleted.
func (r *Router) general(ctx context.Context, text string) {
	if cmd, _, ok := parseCommand(text); !ok || cmd != "control" {
		return
	}
	id, err := r.Topics.EnsureControl(ctx)
	if err != nil {
		r.warn(ctx, 0, err)
		return
	}
	r.reply(ctx, 0, fmt.Sprintf(`🖥 Управление нодой: <a href="%s">%s</a>`, topics.Link(r.ChatID, id), render.Escape(r.Topics.Node())))
}

func (r *Router) reply(ctx context.Context, thread int, html string) {
	_, _ = r.API.SendMessage(ctx, thread, html, nil, false)
}

func (r *Router) warn(ctx context.Context, thread int, err error) {
	r.reply(ctx, thread, "⚠️ "+render.Escape(err.Error()))
}

func (r *Router) control(ctx context.Context, text string) {
	cmd, args, ok := parseCommand(text)
	r.mu.Lock()
	naming := r.askingName
	r.askingName = false
	r.mu.Unlock()
	if !ok {
		if naming {
			if !r.createProject(ctx, text) {
				r.mu.Lock()
				r.askingName = true // wrong name: ask again
				r.mu.Unlock()
			}
			return
		}
		r.menu(ctx)
		return
	}
	switch cmd {
	case "start", "menu":
		r.menu(ctx)
	case "cancel":
		r.reply(ctx, r.Topics.Control(), "Отменено.")
	case "projects":
		r.listProjects(ctx)
	case "newproject":
		if args == "" {
			r.askProjectName(ctx)
			return
		}
		r.createProject(ctx, args)
	case "new":
		project, profile, task, ok := parseNewArgs(args)
		if !ok {
			r.listProjects(ctx)
			return
		}
		r.newSession(ctx, project, task, profile)
	case "profiles":
		r.listProfiles(ctx)
	case "sessions":
		r.listSessions(ctx)
	case "history":
		r.history(ctx, args)
	case "usage":
		r.report(ctx, r.Topics.Control(), r.Sessions.NodeUsage(ctx, r.Topics.Control()))
	case "approve":
		r.approveMenu(ctx)
	default:
		r.reply(ctx, r.Topics.Control(), helpControl)
	}
}

func (r *Router) newSession(ctx context.Context, project, task, profile string) {
	dir, err := r.Projects.Dir(project)
	if err != nil {
		r.warnMenu(ctx, err)
		return
	}
	thread, err := r.Sessions.NewWithProfile(ctx, project, dir, task, profile)
	if err != nil {
		r.warn(ctx, r.Topics.Control(), err)
		return
	}
	r.reply(ctx, r.Topics.Control(), fmt.Sprintf(`🧵 Сессия создана: <a href="%s">%s</a>`,
		topics.Link(r.ChatID, thread), render.Escape(project)))
}

func (r *Router) listProjects(ctx context.Context) {
	names, err := r.Projects.List()
	if err != nil {
		r.warn(ctx, r.Topics.Control(), err)
		return
	}
	if len(names) == 0 {
		r.reply(ctx, r.Topics.Control(), "Проектов нет. Создай: /newproject &lt;name&gt;")
		return
	}
	var kb telegram.Keyboard
	for _, n := range names {
		if len("new:"+n) <= 64 {
			kb = append(kb, []telegram.Button{{Text: "📁 " + n, Data: "pr:" + n}})
		}
	}
	kb = append(kb, []telegram.Button{{Text: "➕ Новый проект", Data: "m:newproject"}, {Text: "🏠 Меню", Data: "m:menu"}})
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), "📁 <b>Проекты</b> — выбери проект", kb, false)
}

func (r *Router) listSessions(ctx context.Context) {
	rows := r.Sessions.List()
	if len(rows) == 0 {
		r.reply(ctx, r.Topics.Control(), "Открытых сессий нет.")
		return
	}
	lines := []string{"<b>Сессии</b>"}
	for _, s := range rows {
		lines = append(lines, fmt.Sprintf(`%s <a href="%s">%s · %s</a>`, render.StateEmoji(s.State),
			topics.Link(r.ChatID, s.ThreadID), render.Escape(s.Project), render.Escape(s.Title)))
	}
	r.reply(ctx, r.Topics.Control(), strings.Join(lines, "\n"))
}

func (r *Router) callback(ctx context.Context, u telegram.Update) {
	if r.Broker.HandleCallback(ctx, u) {
		return
	}
	if alert, ok := r.Sessions.HandleButton(ctx, u); ok {
		_ = r.API.AnswerCallback(ctx, u.CallbackID, alert)
		return
	}
	if key, ok := strings.CutPrefix(u.CallbackData, "h:"); ok {
		r.attach(ctx, u.CallbackID, key)
		return
	}
	if u.ThreadID == r.Topics.Control() {
		kind, arg, _ := strings.Cut(u.CallbackData, ":")
		if kind == "cl" && r.Group != nil {
			toast := ""
			var err error
			switch arg {
			case "ask":
				toast, err = r.Group.Ask(ctx)
			case "sweep":
				toast = r.Group.SweepAll(ctx)
			case "yes":
				err = r.Group.Confirm(ctx, u.MessageID)
			case "no":
				err = r.Group.Cancel(ctx, u.MessageID)
			}
			if err != nil {
				toast = clip("Ошибка: "+err.Error(), 190) // callback toasts hold 200 characters
			}
			_ = r.API.AnswerCallback(ctx, u.CallbackID, toast)
			return
		}
		switch kind {
		case "new", "pr", "ph", "m", "ap":
			if kind == "new" && !r.claimPress(u.MessageID, u.CallbackData) {
				_ = r.API.AnswerCallback(ctx, u.CallbackID, "Сессия уже создаётся")
				return
			}
			// Answer first: Telegram drops answers that come after a slow action.
			_ = r.API.AnswerCallback(ctx, u.CallbackID, "")
			switch kind {
			case "new":
				r.newSession(ctx, arg, "", "")
			case "pr":
				r.projectMenu(ctx, arg)
			case "ph":
				r.history(ctx, arg)
			case "m":
				r.menuAction(ctx, arg)
			case "ap":
				r.setApprove(ctx, u.MessageID, arg)
			}
			return
		}
	}
	_ = r.API.AnswerCallback(ctx, u.CallbackID, "Неизвестная кнопка")
}

// pressGuard is how long a press of a button is remembered, so that a double
// tap does not repeat its action.
const pressGuard = 10 * time.Second

// claimPress reports whether this press of button data on message msgID is
// the first one within pressGuard.
func (r *Router) claimPress(msgID int, data string) bool {
	now := time.Now()
	key := strconv.Itoa(msgID) + ":" + data
	r.mu.Lock()
	defer r.mu.Unlock()
	for k, at := range r.pressed {
		if now.Sub(at) > pressGuard {
			delete(r.pressed, k)
		}
	}
	if _, dup := r.pressed[key]; dup {
		return false
	}
	if r.pressed == nil {
		r.pressed = map[string]time.Time{}
	}
	r.pressed[key] = now
	return true
}

func (r *Router) session(ctx context.Context, thread, msgID int, text string) {
	if cmd, args, ok := parseCommand(text); ok {
		switch cmd {
		case "stop":
			r.report(ctx, thread, r.Sessions.Stop(ctx, thread))
			return
		case "mode":
			r.report(ctx, thread, r.Sessions.SetMode(ctx, thread, args))
			return
		case "close":
			r.report(ctx, thread, r.Sessions.Close(ctx, thread))
			return
		case "skills":
			r.report(ctx, thread, r.Sessions.Skills(ctx, thread))
			return
		case "agents":
			r.report(ctx, thread, r.Sessions.Agents(ctx, thread))
			return
		case "context":
			r.report(ctx, thread, r.Sessions.Context(ctx, thread))
			return
		case "usage":
			r.report(ctx, thread, r.Sessions.SessionUsage(ctx, thread))
			return
		case "ls":
			r.report(ctx, thread, r.Sessions.ListDir(ctx, thread, args, 0))
			return
		case "file":
			r.report(ctx, thread, r.Sessions.SendFile(ctx, thread, args))
			return
		case "help":
			r.reply(ctx, thread, helpSession)
			return
		}
	}
	if r.Broker.HandleText(ctx, thread, text) {
		return
	}
	r.report(ctx, thread, r.Sessions.MessageFrom(ctx, thread, text, msgID))
}

func (r *Router) report(ctx context.Context, thread int, err error) {
	if err != nil {
		r.warn(ctx, thread, err)
	}
}

var sourceIcon = map[string]string{"claude-desktop": "🖥", "cli": "⌨", "sdk-go": "🤖"}

// history lists recent Claude Code sessions of one project or of all projects.
func (r *Router) history(ctx context.Context, project string) {
	names := []string{project}
	if project == "" {
		var err error
		if names, err = r.Projects.List(); err != nil {
			r.warn(ctx, r.Topics.Control(), err)
			return
		}
	}
	var found []pick
	for _, name := range names {
		picks, err := r.projectHistory(name)
		if err != nil {
			if project != "" {
				r.warn(ctx, r.Topics.Control(), err)
				return
			}
			slog.Warn("history", "project", name, "err", err) // one broken project must not hide the rest
			continue
		}
		found = append(found, picks...)
	}
	if len(found) == 0 {
		r.reply(ctx, r.Topics.Control(), "Сессий не найдено.")
		return
	}
	sort.Slice(found, func(i, j int) bool { return found[i].entry.Updated.After(found[j].entry.Updated) })
	if len(found) > 10 {
		found = found[:10]
	}
	now := time.Now()
	lines := []string{"<b>Сессии Claude Code</b> — нажми номер, чтобы продолжить в Telegram"}
	var kb telegram.Keyboard
	r.mu.Lock()
	if r.picks == nil {
		r.picks = map[string]pick{}
	}
	for i, p := range found {
		r.seq++
		key := strconv.Itoa(r.seq)
		r.picks[key] = p
		icon := sourceIcon[p.entry.Entrypoint]
		if icon == "" {
			icon = "·"
		}
		line := fmt.Sprintf("%d. %s <b>%s</b> · %s · %s", i+1, icon, render.Escape(clip(p.entry.Title, 60)), render.Escape(p.project), ago(now.Sub(p.entry.Updated)))
		if p.entry.Active(now) {
			line += " · 🟢 активна"
		}
		if p.entry.LastPrompt != "" && p.entry.LastPrompt != p.entry.Title {
			line += "\n   ↳ " + render.Escape(clip(p.entry.LastPrompt, 80))
		}
		lines = append(lines, line)
		kb = append(kb, []telegram.Button{{Text: fmt.Sprintf("%d ▶ %s", i+1, clip(p.entry.Title, 30)), Data: "h:" + key}})
	}
	for key := range r.picks { // keep the newest maxPicks: older lists go stale
		if n, _ := strconv.Atoi(key); n <= r.seq-maxPicks {
			delete(r.picks, key)
		}
	}
	r.mu.Unlock()
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), strings.Join(lines, "\n"), kb, false)
}

// projectHistory returns the recent Claude Code sessions of one project.
func (r *Router) projectHistory(name string) ([]pick, error) {
	dir, err := r.Projects.Dir(name)
	if err != nil {
		return nil, err
	}
	entries, err := history.List(history.Dir(r.ClaudeHome, dir), 10)
	if err != nil {
		return nil, err
	}
	out := make([]pick, 0, len(entries))
	for _, e := range entries {
		out = append(out, pick{project: name, dir: dir, entry: e})
	}
	return out, nil
}

// attach opens (or points to) a topic for a session picked in /history.
func (r *Router) attach(ctx context.Context, callbackID, key string) {
	r.mu.Lock()
	p, ok := r.picks[key]
	busy := ok && r.attaching[p.entry.ID]
	if ok && !busy { // one attach per session at a time: a double tap must not open two topics
		if r.attaching == nil {
			r.attaching = map[string]bool{}
		}
		r.attaching[p.entry.ID] = true
	}
	r.mu.Unlock()
	if !ok {
		_ = r.API.AnswerCallback(ctx, callbackID, "Список устарел, повтори /history")
		return
	}
	if busy {
		_ = r.API.AnswerCallback(ctx, callbackID, "Сессия уже подключается")
		return
	}
	defer func() {
		r.mu.Lock()
		delete(r.attaching, p.entry.ID)
		r.mu.Unlock()
	}()
	_ = r.API.AnswerCallback(ctx, callbackID, "")
	if thread, open := r.Sessions.FindByClaudeID(p.entry.ID); open {
		r.reply(ctx, r.Topics.Control(), fmt.Sprintf(`Сессия уже открыта: <a href="%s">%s</a>`, topics.Link(r.ChatID, thread), render.Escape(p.project)))
		return
	}
	fork := p.entry.Active(time.Now())
	thread, err := r.Sessions.Attach(ctx, p.project, p.dir, p.entry.ID, p.entry.Title, fork)
	if err != nil {
		r.warn(ctx, r.Topics.Control(), err)
		return
	}
	text := "🔗 Подключено к сессии «" + render.Escape(clip(p.entry.Title, 100)) + "»"
	if p.entry.LastPrompt != "" {
		text += "\nПоследний запрос: " + render.Escape(clip(p.entry.LastPrompt, 300))
	}
	if fork {
		text += "\n\n🟢 Сессия сейчас активна в другом месте, поэтому здесь продолжится её копия: исходная сессия не изменится."
	}
	text += "\n\nНапиши сообщение, чтобы продолжить."
	r.reply(ctx, thread, text)
	r.reply(ctx, r.Topics.Control(), fmt.Sprintf(`🔗 Сессия подключена: <a href="%s">%s</a>`, topics.Link(r.ChatID, thread), render.Escape(p.project)))
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "только что"
	case d < time.Hour:
		return fmt.Sprintf("%d мин назад", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d ч назад", int(d.Hours()))
	default:
		return fmt.Sprintf("%d дн назад", int(d.Hours()/24))
	}
}

var profileRe = regexp.MustCompile(`(?s)^\s*--profile(?:=|\s+)(\S+)\s*(.*)$`)

// parseNewArgs splits "<project> [--profile name] [task]".
func parseNewArgs(args string) (project, profile, task string, ok bool) {
	m := argsRe.FindStringSubmatch(strings.TrimSpace(args))
	if m == nil {
		return "", "", "", false
	}
	project, rest := m[1], m[2]
	if pm := profileRe.FindStringSubmatch(rest); pm != nil {
		profile, rest = pm[1], pm[2]
	}
	return project, profile, strings.TrimSpace(rest), true
}

func (r *Router) listProfiles(ctx context.Context) {
	names := []string{"full"}
	if r.ProfileNames != nil {
		names = r.ProfileNames()
	}
	r.reply(ctx, r.Topics.Control(), "<b>Профили</b>: "+render.Escape(strings.Join(names, ", "))+
		"\nНовая сессия с профилем: /new &lt;project&gt; --profile &lt;имя&gt; [задача]\nПрофили задаются в profiles.yaml рядом с .env.")
}

// file handles a document or photo the user sent: in a session topic it is
// stored in the project inbox for the agent.
func (r *Router) file(ctx context.Context, u telegram.Update) {
	switch {
	case u.ThreadID == r.Topics.Control():
		r.reply(ctx, u.ThreadID, "Файлы принимаются в теме сессии: там агент сможет их прочитать.")
		return
	case !r.Sessions.Owns(u.ThreadID):
		return
	}
	if u.File.Size > telegram.MaxDownload {
		r.reply(ctx, u.ThreadID, "⚠️ Файл больше 20 МБ — Telegram не даёт ботам скачивать такие. Положи его в проект другим способом.")
		return
	}
	data, err := r.API.DownloadFile(ctx, u.File.ID)
	if err != nil {
		r.warn(ctx, u.ThreadID, err)
		return
	}
	r.report(ctx, u.ThreadID, r.Sessions.ReceiveFile(ctx, u.ThreadID, u.File.Name, data, strings.TrimSpace(u.Text)))
}

// Commands is the "/" menu registered with Telegram. Telegram shows one list
// for the whole group, so session commands are marked.
func Commands() []telegram.Command {
	return []telegram.Command{
		{Name: "menu", Description: "Главное меню ноды"},
		{Name: "projects", Description: "Проекты"},
		{Name: "new", Description: "Новая сессия: /new проект задача"},
		{Name: "history", Description: "Продолжить сессию из терминала или приложения"},
		{Name: "sessions", Description: "Открытые сессии"},
		{Name: "newproject", Description: "Создать проект"},
		{Name: "profiles", Description: "Профили плагинов"},
		{Name: "stop", Description: "В сессии: прервать ход"},
		{Name: "mode", Description: "В сессии: режим разрешений"},
		{Name: "ls", Description: "В сессии: файлы проекта"},
		{Name: "file", Description: "В сессии: прислать файл, /file путь"},
		{Name: "skills", Description: "В сессии: команды агента"},
		{Name: "agents", Description: "В сессии: субагенты"},
		{Name: "context", Description: "В сессии: заполненность контекста"},
		{Name: "usage", Description: "Расход Claude и лимиты подписки"},
		{Name: "approve", Description: "Автоодобрение команд агента"},
		{Name: "close", Description: "В сессии: закрыть сессию"},
		{Name: "control", Description: "В общей теме: найти тему управления"},
		{Name: "help", Description: "Справка"},
	}
}

var menuKeyboard = telegram.Keyboard{
	{{Text: "📁 Проекты", Data: "m:projects"}, {Text: "🕘 История", Data: "m:history"}},
	{{Text: "🧵 Сессии", Data: "m:sessions"}, {Text: "➕ Новый проект", Data: "m:newproject"}},
	{{Text: "🔐 Автоодобрение", Data: "m:approve"}, {Text: "❓ Справка", Data: "m:help"}},
}

func approveView(mode string) (string, telegram.Keyboard) {
	text := "🔐 <b>Автоодобрение команд</b>\nСейчас: <b>" + permissions.ApproveLabel(mode) + "</b>\n\n" +
		"🟢 Всё сам — любые команды без вопросов, включая sudo\n" +
		"🟡 Всё, кроме sudo — на sudo придёт кнопка\n" +
		"🔴 По запросу — кнопка на каждую команду\n\n" +
		"Вопросы агента и команды, которые обращаются к папке tgsync, приходят кнопкой в любом режиме. " +
		"Но в режимах 🟢 и 🟡 агент может запустить любой код от твоего пользователя, так что это не стена: служебные файлы tgsync там защищены не полностью."
	var kb telegram.Keyboard
	for _, m := range permissions.ApproveModes {
		label := permissions.ApproveLabel(m)
		if m == mode {
			label = "• " + label
		}
		kb = append(kb, []telegram.Button{{Text: label, Data: "ap:" + m}})
	}
	return text, kb
}

func (r *Router) approveMenu(ctx context.Context) {
	text, kb := approveView(r.Broker.ApproveMode(ctx))
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), text, kb, false)
}

func (r *Router) setApprove(ctx context.Context, msgID int, mode string) {
	if err := r.Broker.SetApproveMode(ctx, mode); err != nil {
		r.warn(ctx, r.Topics.Control(), err)
		return
	}
	text, kb := approveView(r.Broker.ApproveMode(ctx))
	_ = r.API.EditMessage(ctx, msgID, text, kb)
}

func (r *Router) menu(ctx context.Context) {
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), "🖥 <b>"+render.Escape(r.Topics.Node())+"</b> — что делаем?", menuKeyboard, false)
}

func (r *Router) menuAction(ctx context.Context, action string) {
	switch action {
	case "projects":
		r.listProjects(ctx)
	case "history":
		r.history(ctx, "")
	case "sessions":
		r.listSessions(ctx)
	case "newproject":
		r.askProjectName(ctx)
	case "approve":
		r.approveMenu(ctx)
	case "help":
		r.reply(ctx, r.Topics.Control(), helpControl)
	default:
		r.menu(ctx)
	}
}

func (r *Router) projectMenu(ctx context.Context, name string) {
	if _, err := r.Projects.Dir(name); err != nil {
		r.warnMenu(ctx, err)
		return
	}
	kb := telegram.Keyboard{
		{{Text: "▶ Новая сессия", Data: "new:" + name}, {Text: "🕘 История", Data: "ph:" + name}},
		{{Text: "⬅ Проекты", Data: "m:projects"}},
	}
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), "📁 <b>"+render.Escape(name)+"</b>\nНовая сессия — задачу напишешь в её теме. С задачей сразу: /new "+render.Escape(name)+" задача", kb, false)
}

func (r *Router) askProjectName(ctx context.Context) {
	r.mu.Lock()
	r.askingName = true
	r.mu.Unlock()
	r.reply(ctx, r.Topics.Control(), "➕ Как назвать проект? Отправь имя сообщением: латиница, цифры, «.», «_», «-». Отмена — /cancel.")
}

func (r *Router) createProject(ctx context.Context, name string) bool {
	name = strings.TrimSpace(name)
	dir, err := r.Projects.Create(ctx, name)
	if err != nil {
		r.warnMenu(ctx, fmt.Errorf("%v. Попробуй другое имя или /cancel", err))
		return false
	}
	row := []telegram.Button{{Text: "🏠 Меню", Data: "m:menu"}}
	if data := "new:" + name; len(data) <= 64 { // Telegram rejects longer button data
		row = append([]telegram.Button{{Text: "▶ Новая сессия", Data: data}}, row...)
	}
	kb := telegram.Keyboard{row}
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), "📁 Создан проект <b>"+render.Escape(name)+"</b>\n"+render.Escape(dir), kb, false)
	return true
}

// warnMenu reports an error in the control topic with a way forward.
func (r *Router) warnMenu(ctx context.Context, err error) {
	kb := telegram.Keyboard{{{Text: "📁 Проекты", Data: "m:projects"}, {Text: "🏠 Меню", Data: "m:menu"}}}
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), "⚠️ "+render.Escape(err.Error()), kb, false)
}
