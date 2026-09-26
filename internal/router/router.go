// Package router maps Telegram updates to node actions.
package router

import (
	"context"
	"errors"
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
	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
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
	askingName bool                 // "New project" button: the next text is the name
}

// maxPicks is how many /history buttons stay valid; older ones say the list
// is stale.
const maxPicks = 50

// pick is a session offered by /history.
type pick struct {
	project, dir string
	entry        history.Entry
}

// helpSession is the /help text of a session topic.
func helpSession() string { return i18n.T("router.help.session") }

// helpControl is the help text of the control topic.
func helpControl() string { return i18n.T("router.help.control") }

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
	r.reply(ctx, 0, i18n.T("router.control_link", topics.Link(r.ChatID, id), render.Escape(r.Topics.Node())))
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
		r.reply(ctx, r.Topics.Control(), i18n.T("router.cancelled"))
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
		r.reply(ctx, r.Topics.Control(), helpControl())
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
	r.reply(ctx, r.Topics.Control(), i18n.T("router.session_created",
		topics.Link(r.ChatID, thread), render.Escape(project)))
}

func (r *Router) listProjects(ctx context.Context) {
	names, err := r.Projects.List()
	if err != nil {
		r.warn(ctx, r.Topics.Control(), err)
		return
	}
	if len(names) == 0 {
		r.reply(ctx, r.Topics.Control(), i18n.T("router.no_projects"))
		return
	}
	var kb telegram.Keyboard
	for _, n := range names {
		if len("new:"+n) <= 64 {
			kb = append(kb, []telegram.Button{{Text: "📁 " + n, Data: "pr:" + n}})
		}
	}
	kb = append(kb, []telegram.Button{{Text: i18n.T("router.btn.new_project"), Data: "m:newproject"}, {Text: i18n.T("router.btn.menu"), Data: "m:menu"}})
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), i18n.T("router.projects_title"), kb, false)
}

func (r *Router) listSessions(ctx context.Context) {
	rows := r.Sessions.List()
	if len(rows) == 0 {
		r.reply(ctx, r.Topics.Control(), i18n.T("router.no_sessions"))
		return
	}
	lines := []string{i18n.T("router.sessions_title")}
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
				toast = clip(i18n.T("router.error_toast", err.Error()), 190) // callback toasts hold 200 characters
			}
			_ = r.API.AnswerCallback(ctx, u.CallbackID, toast)
			return
		}
		switch kind {
		case "new", "pr", "ph", "m", "ap":
			if kind == "new" && !r.claimPress(u.MessageID, u.CallbackData) {
				_ = r.API.AnswerCallback(ctx, u.CallbackID, i18n.T("router.session_creating"))
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
	_ = r.API.AnswerCallback(ctx, u.CallbackID, i18n.T("router.unknown_button"))
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
			r.reply(ctx, thread, helpSession())
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
		r.reply(ctx, r.Topics.Control(), i18n.T("router.history.none"))
		return
	}
	sort.Slice(found, func(i, j int) bool { return found[i].entry.Updated.After(found[j].entry.Updated) })
	if len(found) > 10 {
		found = found[:10]
	}
	now := time.Now()
	lines := []string{i18n.T("router.history.title")}
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
			line += i18n.T("router.history.active")
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
		_ = r.API.AnswerCallback(ctx, callbackID, i18n.T("router.history.stale"))
		return
	}
	if busy {
		_ = r.API.AnswerCallback(ctx, callbackID, i18n.T("router.history.busy"))
		return
	}
	defer func() {
		r.mu.Lock()
		delete(r.attaching, p.entry.ID)
		r.mu.Unlock()
	}()
	_ = r.API.AnswerCallback(ctx, callbackID, "")
	if thread, open := r.Sessions.FindByClaudeID(p.entry.ID); open {
		r.reply(ctx, r.Topics.Control(), i18n.T("router.history.open", topics.Link(r.ChatID, thread), render.Escape(p.project)))
		return
	}
	fork := p.entry.Active(time.Now())
	thread, err := r.Sessions.Attach(ctx, p.project, p.dir, p.entry.ID, p.entry.Title, fork)
	if err != nil {
		r.warn(ctx, r.Topics.Control(), err)
		return
	}
	text := i18n.T("router.attach.title", render.Escape(clip(p.entry.Title, 100)))
	if p.entry.LastPrompt != "" {
		text += i18n.T("router.attach.last", render.Escape(clip(p.entry.LastPrompt, 300)))
	}
	if fork {
		text += i18n.T("router.attach.fork")
	}
	text += i18n.T("router.attach.continue")
	r.reply(ctx, thread, text)
	r.reply(ctx, r.Topics.Control(), i18n.T("router.attach.link", topics.Link(r.ChatID, thread), render.Escape(p.project)))
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
		return i18n.T("router.ago.now")
	case d < time.Hour:
		n := int(d.Minutes())
		return i18n.N("router.ago.n.minutes", n, n)
	case d < 24*time.Hour:
		n := int(d.Hours())
		return i18n.N("router.ago.n.hours", n, n)
	default:
		n := int(d.Hours() / 24)
		return i18n.N("router.ago.n.days", n, n)
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
	r.reply(ctx, r.Topics.Control(), i18n.T("router.profiles", render.Escape(strings.Join(names, ", "))))
}

// file handles a document or photo the user sent: in a session topic it is
// stored in the project inbox for the agent.
func (r *Router) file(ctx context.Context, u telegram.Update) {
	switch {
	case u.ThreadID == r.Topics.Control():
		r.reply(ctx, u.ThreadID, i18n.T("router.file.control"))
		return
	case !r.Sessions.Owns(u.ThreadID):
		return
	}
	if u.File.Size > telegram.MaxDownload {
		r.reply(ctx, u.ThreadID, i18n.T("router.file.too_big"))
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
		{Name: "menu", Description: i18n.T("router.cmd.menu")},
		{Name: "projects", Description: i18n.T("router.cmd.projects")},
		{Name: "new", Description: i18n.T("router.cmd.new")},
		{Name: "history", Description: i18n.T("router.cmd.history")},
		{Name: "sessions", Description: i18n.T("router.cmd.sessions")},
		{Name: "newproject", Description: i18n.T("router.cmd.newproject")},
		{Name: "profiles", Description: i18n.T("router.cmd.profiles")},
		{Name: "stop", Description: i18n.T("router.cmd.stop")},
		{Name: "mode", Description: i18n.T("router.cmd.mode")},
		{Name: "ls", Description: i18n.T("router.cmd.ls")},
		{Name: "file", Description: i18n.T("router.cmd.file")},
		{Name: "skills", Description: i18n.T("router.cmd.skills")},
		{Name: "agents", Description: i18n.T("router.cmd.agents")},
		{Name: "context", Description: i18n.T("router.cmd.context")},
		{Name: "usage", Description: i18n.T("router.cmd.usage")},
		{Name: "approve", Description: i18n.T("router.cmd.approve")},
		{Name: "close", Description: i18n.T("router.cmd.close")},
		{Name: "control", Description: i18n.T("router.cmd.control")},
		{Name: "help", Description: i18n.T("router.cmd.help")},
	}
}

func menuKeyboard() telegram.Keyboard {
	return telegram.Keyboard{
		{{Text: i18n.T("router.btn.projects"), Data: "m:projects"}, {Text: i18n.T("router.btn.history"), Data: "m:history"}},
		{{Text: i18n.T("router.btn.sessions"), Data: "m:sessions"}, {Text: i18n.T("router.btn.new_project"), Data: "m:newproject"}},
		{{Text: i18n.T("router.btn.approve"), Data: "m:approve"}, {Text: i18n.T("router.btn.help"), Data: "m:help"}},
	}
}

func approveView(mode string) (string, telegram.Keyboard) {
	text := i18n.T("router.approve", permissions.ApproveLabel(mode))
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
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), i18n.T("router.menu", render.Escape(r.Topics.Node())), menuKeyboard(), false)
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
		r.reply(ctx, r.Topics.Control(), helpControl())
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
		{{Text: i18n.T("router.btn.new_session"), Data: "new:" + name}, {Text: i18n.T("router.btn.history"), Data: "ph:" + name}},
		{{Text: i18n.T("router.btn.back_projects"), Data: "m:projects"}},
	}
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), i18n.T("router.project_menu", render.Escape(name), render.Escape(name)), kb, false)
}

func (r *Router) askProjectName(ctx context.Context) {
	r.mu.Lock()
	r.askingName = true
	r.mu.Unlock()
	r.reply(ctx, r.Topics.Control(), i18n.T("router.ask_project_name"))
}

func (r *Router) createProject(ctx context.Context, name string) bool {
	name = strings.TrimSpace(name)
	dir, err := r.Projects.Create(ctx, name)
	if err != nil {
		r.warnMenu(ctx, errors.New(i18n.T("router.bad_project_name", err)))
		return false
	}
	row := []telegram.Button{{Text: i18n.T("router.btn.menu"), Data: "m:menu"}}
	if data := "new:" + name; len(data) <= 64 { // Telegram rejects longer button data
		row = append([]telegram.Button{{Text: i18n.T("router.btn.new_session"), Data: data}}, row...)
	}
	kb := telegram.Keyboard{row}
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), i18n.T("router.project_created", render.Escape(name), render.Escape(dir)), kb, false)
	return true
}

// warnMenu reports an error in the control topic with a way forward.
func (r *Router) warnMenu(ctx context.Context, err error) {
	kb := telegram.Keyboard{{{Text: i18n.T("router.btn.projects"), Data: "m:projects"}, {Text: i18n.T("router.btn.menu"), Data: "m:menu"}}}
	_, _ = r.API.SendMessage(ctx, r.Topics.Control(), "⚠️ "+render.Escape(err.Error()), kb, false)
}
