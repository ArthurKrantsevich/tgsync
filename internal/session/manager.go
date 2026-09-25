// Package session runs agent sessions bound to Telegram topics.
package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/files"
	"github.com/ArthurKrantsevich/tgsync/internal/permissions"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

// ErrUnknownSession means the topic has no session on this node.
var ErrUnknownSession = errors.New("сессия не найдена на этой ноде")

// ErrAlreadyOpen: the Claude session is already open in another topic here.
var ErrAlreadyOpen = errors.New("эта сессия уже открыта в другой теме")

const defaultTitle = "новая сессия"

var modes = map[string]bool{"default": true, "acceptEdits": true, "plan": true}

// Deps are the collaborators of Manager.
type Deps struct {
	API            telegram.API
	Store          *store.Store
	Topics         *topics.Manager
	Broker         *permissions.Broker
	Runner         agent.Runner
	MaxParallel    int
	CLIPath        string
	SettingSources []string
	Protected      []string
	EditInterval   time.Duration                      // minimum time between status message edits
	ProbeInterval  time.Duration                      // how often idle session topics are checked for deletion
	AutoSend       []string                           // globs of changed files sent at the end of a turn
	AgentEnv       func(thread int) map[string]string // extra environment of a session's claude process
	IdleTimeout    time.Duration                      // stop an idle claude process after this long; 0 keeps it
	StallWarn      time.Duration                      // warn when a turn shows no activity this long; 0 disables
	MaxTurn        time.Duration                      // interrupt turns longer than this; 0 disables
	RemindEvery    time.Duration                      // remind about unanswered prompts; 0 disables
	// Profiles resolves a profile name (empty: project or node default) into
	// the setting sources, env and settings of the claude process.
	Profiles       func(name, project string) (string, agent.StartOptions, error)
	ShowHookOutput bool          // post hook messages meant for the user
	Limits         LimitObserver // subscription limit notices; nil disables them
	Now            func() time.Time
}

// Manager owns every session of the node. m.mu is never held during
// Telegram or agent calls.
type Manager struct {
	d Deps

	mu        sync.Mutex
	sessions  map[int]*sess
	order     []int // threads waiting for a turn slot, FIFO
	stopping  bool
	fileSeq   int
	itemSeq   int                // ids of inbox items, for «send now» buttons
	agentSeq  int                // numbers of tracked agents, for panel buttons
	endSeq    int                // order tracked agents finished in
	agentRefs map[int]agentRef   // panel buttons: ag:<n> → agent
	fileRefs  map[string]fileRef // /ls buttons: f:<n> file, l:<n> folder
	attaching map[string]bool    // Claude session ids being attached right now
	turnRefs  map[string]turnRef // turn summary buttons: td/tc/tr/ty/tn:<n>
}

type fileRef struct {
	thread int
	rel    string
	dir    bool // /ls folder button: rel is a folder, offset its page
	offset int
}

// listing is an /ls message and the fileRefs keys of its buttons.
type listing struct {
	msg  int // 0 until the message is sent
	keys []string
}

type sess struct {
	row          store.SessionRow
	agent        agent.Session
	inbox        []*inboxItem
	turnMsg      int // the user's message the current turn answers, marked with a reaction
	inTurn       bool
	queued       bool
	closed       bool
	fork         bool // next process start continues a copy of row.ClaudeSessionID
	files        *files.Tracker
	turnNo       int                  // turns started in this process; turn buttons act on the latest only
	turnBase     string               // git snapshot taken when the current turn started; "" outside git
	rollbackNote string               // put before the next prompt after ↩ rollback
	restoring    bool                 // ↩ rollback in progress: no turn may start
	restored     chan struct{}        // closed when the rollback in progress ends
	idleSince    time.Time            // end of the last turn
	stallFrom    time.Time            // «Ждать» pressed: the next stall warning counts from here
	warned       bool                 // stall warning sent for the current quiet period
	overtime     bool                 // MAX_TURN_DURATION already enforced in this turn
	initShown    bool                 // plugin summary already posted
	commands     []string             // slash commands from the last init, for /skills
	inbox2       []string             // files the user sent without a caption, for the next message
	lastText     string               // last top-level answer of the turn, scanned for file paths
	pending      string               // top-level text not shown yet: the final answer, or a remark before the next tool call
	interrupted  bool                 // the node interrupted this turn (/stop, «send now», time limit)
	snapWarned   bool                 // the user was told that git snapshots fail
	retryAt      time.Time            // MAX_TURN_DURATION: next interrupt attempt after a failed one
	probedAt     time.Time            // last topic probe after an edit found no message
	panel        *agentPanel          // the latest agents panel, nil before the first agent
	agentCalls   map[string]agentCall // Agent tool calls not yet matched to a task, by tool use id
	lastFinished string               // name of the agent that finished last, for continuation turns
	ctxSnap      *agent.ContextInfo   // context at the end of the last turn
	ctxAt        time.Time
	hinted       bool // the compaction hint was shown for the current crossing
	// usageBase holds the running totals the current process reported last:
	// result usage is cumulative per process, rows store the difference.
	usageBase map[string]agent.ModelUsage
	listings  []*listing // /ls messages with live buttons, oldest first
	summaries []string   // turnRefs keys of this session, oldest first
	waits     int
	status    render.Status
	statusMsg int
	lastEdit  time.Time
	dirty     bool
}

func NewManager(d Deps) *Manager {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.MaxParallel < 1 {
		d.MaxParallel = 1
	}
	if d.ProbeInterval <= 0 {
		d.ProbeInterval = 10 * time.Minute
	}
	return &Manager{d: d, sessions: map[int]*sess{}, fileRefs: map[string]fileRef{}, attaching: map[string]bool{}, turnRefs: map[string]turnRef{}, agentRefs: map[int]agentRef{}}
}

// inboxItem is a message waiting for its turn.
type inboxItem struct {
	id      int
	text    string
	msg     int  // the user's Telegram message, 0 if none
	notice  int  // the «queued» notice with the «send now» button, 0 if none
	started bool // its turn began; a late notice is deleted
}

func newSess(row store.SessionRow) *sess {
	return &sess{row: row, files: files.NewTracker(row.Cwd), agentCalls: map[string]agentCall{}}
}

// Restore loads open sessions after a node restart. Sessions that were in a
// turn become interrupted; the next message resumes them.
func (m *Manager) Restore(ctx context.Context) error {
	rows, err := m.d.Store.OpenSessions(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		s := newSess(row)
		m.mu.Lock()
		m.sessions[row.ThreadID] = s
		m.mu.Unlock()
		switch row.State {
		case store.StateIdle, store.StateFailed, store.StateInterrupted:
		default:
			m.setState(ctx, s, store.StateInterrupted)
			m.say(ctx, s, "⏸ Нода перезапустилась во время хода. Напиши сообщение, чтобы продолжить.", false)
		}
	}
	return nil
}

// New creates a topic and a session with the default profile.
func (m *Manager) New(ctx context.Context, project, dir, task string) (int, error) {
	return m.NewWithProfile(ctx, project, dir, task, "")
}

// NewWithProfile creates a topic and a session. A non-empty task starts the
// first turn. An unknown profile is refused before anything is created.
func (m *Manager) NewWithProfile(ctx context.Context, project, dir, task, profile string) (int, error) {
	if m.d.Profiles != nil {
		name, _, err := m.d.Profiles(profile, project)
		if err != nil {
			return 0, err
		}
		profile = name
	}
	title := defaultTitle
	if strings.TrimSpace(task) != "" {
		title = shortTitle(task)
	}
	thread, err := m.d.Topics.CreateSession(ctx, project, title)
	if err != nil {
		return 0, fmt.Errorf("создать topic: %w", err)
	}
	row := store.SessionRow{ThreadID: thread, Project: project, Cwd: dir, Title: title, State: store.StateIdle, Mode: "default", Profile: profile}
	if err := m.d.Store.CreateSession(ctx, &row); err != nil {
		return 0, err
	}
	s := newSess(row)
	m.mu.Lock()
	m.sessions[thread] = s
	m.mu.Unlock()
	if profile != "" && profile != "full" {
		m.say(ctx, s, "🧩 профиль "+render.Escape(profile), true)
	}
	if strings.TrimSpace(task) == "" {
		m.say(ctx, s, "🧵 Сессия готова. Напиши задачу — агент начнёт работу. Подсказки: /help", false)
		return thread, nil
	}
	return thread, m.Message(ctx, thread, task)
}

// Attach opens a topic for an existing Claude Code session, for example one
// started in a terminal. With fork the first turn continues a copy, so a
// session that is still running elsewhere is left untouched.
func (m *Manager) Attach(ctx context.Context, project, dir, sessionID, title string, fork bool) (int, error) {
	if title = shortTitle(title); title == "" {
		title = defaultTitle
	}
	if !fork {
		// Two topics resuming one transcript would interleave it: the id is
		// claimed before the slow topic creation.
		m.mu.Lock()
		busy := m.attaching[sessionID]
		for _, s := range m.sessions {
			busy = busy || s.row.ClaudeSessionID == sessionID
		}
		if !busy {
			m.attaching[sessionID] = true
		}
		m.mu.Unlock()
		if busy {
			return 0, ErrAlreadyOpen
		}
		defer func() {
			m.mu.Lock()
			delete(m.attaching, sessionID)
			m.mu.Unlock()
		}()
	}
	thread, err := m.d.Topics.CreateSession(ctx, project, title)
	if err != nil {
		return 0, fmt.Errorf("создать topic: %w", err)
	}
	row := store.SessionRow{ThreadID: thread, Project: project, Cwd: dir, Title: title,
		ClaudeSessionID: sessionID, State: store.StateIdle, Mode: "default"}
	if err := m.d.Store.CreateSession(ctx, &row); err != nil {
		return 0, err
	}
	s := newSess(row)
	s.fork = fork
	m.mu.Lock()
	m.sessions[thread] = s
	m.mu.Unlock()
	return thread, nil
}

// FindByClaudeID returns the topic of an open session with this Claude session id.
func (m *Manager) FindByClaudeID(id string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for thread, s := range m.sessions {
		if s.row.ClaudeSessionID == id {
			return thread, true
		}
	}
	return 0, false
}

// Message delivers user text to the session, now or after the current turn.
func (m *Manager) Message(ctx context.Context, thread int, text string) error {
	return m.MessageFrom(ctx, thread, text, 0)
}

// processingReaction marks the user's message while its turn is queued or running.
const processingReaction = "👀"

// MessageFrom is Message for the user's Telegram message msgID: it carries a
// reaction until the turn answering it ends.
func (m *Manager) MessageFrom(ctx context.Context, thread int, text string, msgID int) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	// React before queueing: a late reaction could land after the turn
	// already ended and removed it.
	if msgID != 0 {
		m.react(ctx, s, msgID, processingReaction)
	}
	m.mu.Lock()
	if m.sessions[thread] != s {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	item := text
	if len(s.inbox2) > 0 {
		item = "Пользователь прислал файлы (прочитай, если нужно для задачи):\n- " + strings.Join(s.inbox2, "\n- ") + "\n\n" + text
		s.inbox2 = nil
	}
	m.itemSeq++
	it := &inboxItem{id: m.itemSeq, text: item, msg: msgID}
	s.inbox = append(s.inbox, it)
	renamed := s.row.Title == defaultTitle
	if renamed {
		s.row.Title = shortTitle(text)
	}
	row := s.row
	busy := s.inTurn || s.queued
	if !s.inTurn {
		m.enqueue(thread)
	}
	m.mu.Unlock()
	if renamed {
		m.save(ctx, row)
		if err := m.d.Topics.Rename(ctx, thread, row.Project, row.Title); err != nil {
			m.telegramFailed(s, "rename topic", err)
		}
	}
	if busy {
		m.queueNotice(ctx, s, it)
		return nil
	}
	m.schedule(ctx)
	return nil
}

// enqueue adds a thread to the slot queue. Callers hold m.mu.
func (m *Manager) enqueue(thread int) {
	for _, t := range m.order {
		if t == thread {
			return
		}
	}
	m.order = append(m.order, thread)
}

// schedule starts turns while there are free slots.
func (m *Manager) schedule(ctx context.Context) {
	for {
		m.mu.Lock()
		run, queued := m.pick()
		m.mu.Unlock()
		for _, s := range queued {
			m.setState(ctx, s, store.StateQueued)
		}
		if run == nil {
			return
		}
		if m.safely("startTurn", run, func() { m.startTurn(ctx, run) }) {
			m.safely("startTurn recovery", run, func() {
				m.say(ctx, run, internalError+" Напиши сообщение, чтобы продолжить.", false)
				m.failTurn(ctx, run)
			})
		}
	}
}

// pick takes the first session that may start a turn now. When none can,
// it returns the sessions that just became queued. Callers hold m.mu.
func (m *Manager) pick() (*sess, []*sess) {
	active := 0
	busy := map[string]bool{}
	for _, s := range m.sessions {
		if s.inTurn {
			active++
			busy[s.row.Project] = true
		}
	}
	kept := m.order[:0]
	for i, th := range m.order {
		s := m.sessions[th]
		if s != nil && s.restoring && len(s.inbox) > 0 {
			kept = append(kept, th) // starts once the rollback is done
			continue
		}
		if s == nil || s.inTurn || len(s.inbox) == 0 {
			continue
		}
		if active < m.d.MaxParallel && !busy[s.row.Project] {
			s.inTurn, s.queued = true, false
			m.order = append(kept, m.order[i+1:]...)
			return s, nil
		}
		kept = append(kept, th)
	}
	m.order = kept
	var queued []*sess
	for _, th := range kept {
		if s := m.sessions[th]; !s.queued {
			s.queued = true
			queued = append(queued, s)
		}
	}
	return nil, queued
}

func (m *Manager) startTurn(ctx context.Context, s *sess) {
	m.mu.Lock()
	// schedule released m.mu after pick: Close or topicGone may have emptied
	// the inbox meanwhile.
	if s.closed || len(s.inbox) == 0 {
		s.inTurn = false
		m.mu.Unlock()
		return
	}
	it := s.inbox[0]
	s.inbox = s.inbox[1:]
	it.started = true
	text, notice := it.text, it.notice
	s.turnMsg = it.msg
	a := s.agent
	row := s.row
	m.mu.Unlock()
	if notice != 0 {
		_ = m.d.API.DeleteMessage(ctx, notice)
	}

	if a == nil {
		m.setState(ctx, s, store.StateStarting)
		prof := agent.StartOptions{SettingSources: m.d.SettingSources}
		if m.d.Profiles != nil {
			var err error
			if _, prof, err = m.d.Profiles(row.Profile, row.Project); err != nil {
				m.abortTurn(ctx, s, it, "❌ "+render.Escape(err.Error()))
				return
			}
		}
		env := map[string]string{}
		for k, v := range prof.Env {
			env[k] = v
		}
		for k, v := range m.agentEnv(row.ThreadID) {
			env[k] = v
		}
		prompt := telegramPrompt
		if env["SUDO_ASKPASS"] != "" {
			prompt += " " + sudoPrompt
		}
		var err error
		a, err = m.d.Runner.Start(ctx, agent.StartOptions{
			Cwd: row.Cwd, ResumeID: row.ClaudeSessionID, Fork: s.fork, PermissionMode: row.Mode,
			SettingSources: prof.SettingSources, Settings: prof.Settings, HookEvents: m.d.ShowHookOutput,
			CLIPath: m.d.CLIPath,
			CanUseTool: m.d.Broker.CanUseTool(permissions.SessionInfo{
				ThreadID: row.ThreadID, Project: row.Project, ProjectDir: row.Cwd, Protected: m.d.Protected,
				OnWait: func(waiting bool) { m.safely("onWait", s, func() { m.onWait(s, waiting) }) },
			}),
			Env:                env,
			AppendSystemPrompt: prompt,
			Tools:              []agent.Tool{m.sendFileTool(s)},
		})
		if err != nil {
			m.abortTurn(ctx, s, it, "❌ Не удалось запустить claude: "+render.Escape(err.Error()))
			return
		}
		m.mu.Lock()
		if s.closed {
			// Closed while claude was starting: Close saw no process to stop.
			m.mu.Unlock()
			_ = a.Close()
			return
		}
		s.agent, s.fork, s.usageBase = a, false, nil
		m.mu.Unlock()
		go m.readEvents(s, a)
	}

	base := m.snapshot(ctx, s, "turn snapshot")
	now := m.d.Now()
	m.mu.Lock()
	s.turnNo++
	prevBase := s.turnBase
	s.turnBase = base
	note := s.rollbackNote
	s.status = render.Status{State: store.StateRunning, Started: now, LastEvent: now}
	s.lastEdit, s.dirty, s.waits = now, false, 0
	s.warned, s.overtime, s.stallFrom, s.retryAt = false, false, time.Time{}, time.Time{}
	s.lastText, s.pending, s.interrupted = "", "", false
	status := s.status
	m.mu.Unlock()
	msgID, _ := m.d.API.SendMessage(ctx, row.ThreadID, render.StatusText(status, now), stopKeyboard(row.ThreadID), true)
	m.mu.Lock()
	s.statusMsg = msgID
	m.mu.Unlock()
	m.setState(ctx, s, store.StateRunning)
	if note != "" {
		text = note + "\n\n" + text
	}
	m.mu.Lock()
	closed := s.closed
	m.mu.Unlock()
	if closed { // Close stopped the process while the status was being posted
		if msgID != 0 {
			_ = m.d.API.DeleteMessage(ctx, msgID)
		}
		return
	}
	if err := a.Send(ctx, text); err != nil {
		m.mu.Lock() // no turn ran: the previous summary stays the latest
		s.turnNo--
		s.turnBase = prevBase
		m.mu.Unlock()
		m.abortTurn(ctx, s, it, "❌ Не удалось отправить сообщение агенту: "+render.Escape(err.Error()))
		return
	}
	if note != "" {
		m.mu.Lock()
		if s.rollbackNote == note {
			s.rollbackNote = ""
		}
		m.mu.Unlock()
	}
}

// abortTurn ends a turn that never reached the agent. The message goes back
// to the front of the inbox and is sent with the next one. It is only called
// from inside schedule, so it does not reschedule.
func (m *Manager) abortTurn(ctx context.Context, s *sess, it *inboxItem, html string) {
	m.mu.Lock()
	s.inTurn = false
	it.started, it.notice = false, 0
	s.inbox = append([]*inboxItem{it}, s.inbox...)
	statusMsg := s.statusMsg
	s.statusMsg = 0
	m.mu.Unlock()
	if statusMsg != 0 {
		_ = m.d.API.DeleteMessage(ctx, statusMsg)
	}
	m.endReaction(ctx, s)
	m.say(ctx, s, html+"\nСообщение сохранено и уйдёт вместе со следующим.", false)
	m.setState(ctx, s, store.StateFailed)
}

func (m *Manager) readEvents(s *sess, a agent.Session) {
	defer func() {
		r := recover()
		if r == nil {
			return
		}
		m.crashed("readEvents", s, r)
		_ = a.Close()
		m.mu.Lock()
		current := s.agent == a || s.agent == nil
		if s.agent == a {
			s.agent = nil
		}
		m.mu.Unlock()
		if current { // the turn belongs to this process: it is gone now
			ctx := context.Background()
			m.safely("readEvents recovery", s, func() {
				m.say(ctx, s, internalError, false)
				m.failTurn(ctx, s)
				m.schedule(ctx)
			})
		}
	}()
	for ev := range a.Events() {
		if m.safely("handleEvent", s, func() { m.handleEvent(s, ev) }) {
			m.safely("handleEvent recovery", s, func() { m.eventPanicked(s, ev) })
		}
	}
	ctx := context.Background()
	_ = a.Close()
	m.mu.Lock()
	if s.agent != nil && s.agent != a {
		// An old process (stopped for idling) exited after a new one took
		// over; the current turn belongs to the new process.
		m.mu.Unlock()
		return
	}
	m.mu.Unlock()
	m.d.Broker.CancelThread(ctx, s.row.ThreadID)
	m.agentsGone(ctx, s)
	m.mu.Lock()
	if s.agent == a {
		s.agent = nil
	}
	wasInTurn := s.inTurn
	s.inTurn, s.waits = false, 0
	skip := m.stopping || s.closed
	st, msgID := s.status, s.statusMsg
	s.statusMsg, s.dirty = 0, false
	if !skip && len(s.inbox) > 0 {
		m.enqueue(s.row.ThreadID)
	}
	m.mu.Unlock()
	if skip {
		return
	}
	if wasInTurn {
		if msgID != 0 {
			st.State = store.StateFailed
			m.edit(ctx, s, msgID, render.StatusText(st, m.d.Now()))
		}
		m.endReaction(ctx, s)
		m.sayPending(ctx, s)
		m.say(ctx, s, "❌ Процесс claude завершился во время хода. Напиши сообщение, чтобы продолжить.", false)
		m.setState(ctx, s, store.StateFailed)
	}
	m.schedule(ctx)
}

func (m *Manager) handleEvent(s *sess, ev agent.Event) {
	m.mu.Lock()
	closed := s.closed
	m.mu.Unlock()
	if closed {
		return
	}
	ctx := context.Background()
	now := m.d.Now()
	m.mu.Lock()
	s.warned = false
	m.mu.Unlock()
	// A top-level event outside a turn: a finished background agent woke
	// the agent for a continuation turn. A result with nothing to say is
	// noise; an error result is still shown.
	switch ev.Kind {
	case agent.EventText, agent.EventToolUse, agent.EventResult:
		m.mu.Lock()
		idle := !s.inTurn
		m.mu.Unlock()
		if idle && ev.ParentToolUseID == "" {
			if ev.Kind == agent.EventResult && !ev.Result.IsError && strings.TrimSpace(ev.Result.Text) == "" {
				return
			}
			m.beginContinuation(ctx, s)
		}
	}
	switch ev.Kind {
	case agent.EventInit:
		m.mu.Lock()
		changed := ev.Init.SessionID != "" && ev.Init.SessionID != s.row.ClaudeSessionID
		if changed {
			s.row.ClaudeSessionID = ev.Init.SessionID
		}
		row := s.row
		s.commands = ev.Init.SlashCommands
		first := !s.initShown
		s.initShown = true
		m.mu.Unlock()
		if changed {
			m.save(ctx, row)
		}
		if text := initSummary(ev.Init); first && text != "" {
			m.say(ctx, s, text, true)
		}
	case agent.EventToolUse:
		m.mu.Lock()
		s.files.Note(ev.ToolName, ev.ToolInput)
		if ev.ToolName == "Agent" || ev.ToolName == "Task" {
			name, _ := ev.ToolInput["subagent_type"].(string)
			if name == "" {
				name = "agent"
			}
			s.agentCalls[ev.ToolUseID] = agentCall{name: name, parent: ev.ParentToolUseID, at: now}
			if a := m.findAgent(s, ev.ToolUseID); a != nil {
				a.name = name
			}
		}

		line := render.ToolLine(ev.ToolName, ev.ToolInput, s.row.Cwd)
		s.status.Step++
		if ev.ParentToolUseID != "" {
			s.status.Sub = line
		} else {
			s.status.Current, s.status.Sub = line, ""
			if s.pending != "" {
				s.status.Note, s.pending = render.NoteLine(s.pending), ""
			}
		}
		s.status.LastEvent = now
		s.dirty = true
		m.mu.Unlock()
		m.maybeFlush(ctx, s, false)
		if ev.ParentToolUseID != "" {
			m.noteAction(ctx, s, ev.ParentToolUseID, line)
		}
	case agent.EventToolResult:
		m.mu.Lock()
		s.status.LastEvent = now
		if a := m.findAgent(s, ev.ToolUseID); a != nil && ev.Text != "" {
			a.result = ev.Text
		}
		m.mu.Unlock()
	case agent.EventText:
		m.mu.Lock()
		s.status.LastEvent = now
		m.mu.Unlock()
		if ev.ParentToolUseID != "" {
			return
		}
		// Only the last text of a turn is posted; earlier ones become the
		// status note when the next tool call starts.
		m.mu.Lock()
		s.lastText = ev.Text
		if s.pending != "" {
			s.pending += "\n\n"
		}
		s.pending += ev.Text
		m.mu.Unlock()
	case agent.EventTaskStarted, agent.EventTaskProgress, agent.EventTaskDone:
		m.onTask(ctx, s, ev)
	case agent.EventRateLimit:
		if m.d.Limits != nil && ev.Limit != nil {
			m.d.Limits.Observe(ctx, s.row.ThreadID, *ev.Limit)
		}
	case agent.EventCompacted:
		m.compacted(ctx, s, ev.Text, ev.Tokens)
	case agent.EventResult:
		m.finishTurn(ctx, s, *ev.Result)
	case agent.EventError:
		m.say(ctx, s, "⚠️ "+render.Escape(ev.Err.Error()), false)
	case agent.EventHook:
		if m.d.ShowHookOutput {
			m.say(ctx, s, "<i>🪝 "+render.Escape(ev.Text)+"</i>", true)
		}
	}
}

func (m *Manager) finishTurn(ctx context.Context, s *sess, r agent.ResultInfo) {
	m.d.Broker.CancelThread(ctx, s.row.ThreadID)
	now := m.d.Now()
	m.mu.Lock()
	s.inTurn, s.waits = false, 0
	s.idleSince = now
	s.status.State = store.StateIdle
	st, msgID := s.status, s.statusMsg
	s.statusMsg, s.dirty = 0, false
	// Everything of this turn is taken together with freeing the slot: the
	// next turn may start during the Telegram calls below and reset it.
	pending, interrupted, text := s.pending, s.interrupted, s.lastText
	s.pending, s.interrupted, s.lastText = "", false, ""
	turnMsg := s.turnMsg
	s.turnMsg = 0
	turn := turnInfo{no: s.turnNo, base: s.turnBase}
	if len(s.inbox) > 0 {
		m.enqueue(s.row.ThreadID)
	}
	m.mu.Unlock()
	// Before anything slow: a turn started meanwhile sets its own state.
	m.setState(ctx, s, store.StateIdle)
	if msgID != 0 {
		// The result line repeats its figures; the status message is noise now.
		if err := m.d.API.DeleteMessage(ctx, msgID); err != nil {
			m.edit(ctx, s, msgID, render.StatusText(st, now))
		}
	}
	if turnMsg != 0 {
		m.react(ctx, s, turnMsg, "")
	}
	chunks := render.Chunks(pending)
	result := render.ResultText(r, st.Step, now.Sub(st.Started))
	if interrupted && r.IsError {
		result = render.InterruptedText(st.Step, now.Sub(st.Started))
	}
	m.recordUsage(ctx, s, r, now)
	// Collect this turn's files before the next turn can start, deliver after
	// the slot is free: uploads may take a while.
	changed := s.files.Changed()
	s.files.ResetTurn()
	if turn.base != "" && len(changed) > 0 {
		// The end state is taken now: the next turn or the user may change
		// the files before the summary and its buttons are used.
		turn.end = m.snapshot(ctx, s, "turn end snapshot")
	}

	// The answer and the result line go out as one message when they fit;
	// room is kept for the context figure added later.
	merged := false
	if n := len(chunks); n > 0 && len(chunks[n-1])+len(result)+40 <= maxMessage {
		merged = true
	}
	lastID, lastBase := 0, ""
	for i, chunk := range chunks {
		last := i == len(chunks)-1
		var kb telegram.Keyboard
		html := chunk
		if last && merged {
			kb, lastBase = m.resultKeyboard(s), chunk+"\n\n"
			html = lastBase + "<i>" + result + "</i>"
		}
		id, err := m.d.API.SendMessage(ctx, s.row.ThreadID, html, kb, !last || !merged)
		if err != nil {
			m.telegramFailed(s, "send answer", err)
		}
		if last && merged {
			lastID = id
		}
	}
	if !merged {
		id, err := m.d.API.SendMessage(ctx, s.row.ThreadID, result, m.resultKeyboard(s), false)
		if err != nil {
			m.telegramFailed(s, "send result", err)
		}
		lastID = id
	}
	m.schedule(ctx)
	go m.deliverFiles(ctx, s, text, changed, turn)

	// The context figure is added to the result line afterwards, so the
	// answer never waits for the query.
	info := m.snapshotContext(ctx, s)
	if info == nil {
		return
	}
	line := result + fmt.Sprintf(" · 🧠 %d%%", int(info.Percent+0.5))
	html := line
	if merged {
		html = lastBase + "<i>" + line + "</i>"
	}
	if lastID != 0 {
		if err := m.d.API.EditMessage(ctx, lastID, html, m.resultKeyboard(s)); err != nil {
			m.telegramFailed(s, "edit result", err)
		}
	}
	m.contextHint(ctx, s, info)
}

// onWait tracks permission and question waits. Parallel tool calls may wait at once.
func (m *Manager) onWait(s *sess, waiting bool) {
	ctx := context.Background()
	m.mu.Lock()
	if !s.inTurn {
		m.mu.Unlock()
		return
	}
	if waiting {
		s.waits++
	} else if s.waits > 0 {
		s.waits--
	}
	state := store.StateRunning
	if s.waits > 0 {
		state = store.StateWaiting
	}
	s.status.State = state
	s.dirty = true
	m.mu.Unlock()
	m.setState(ctx, s, state)
	m.maybeFlush(ctx, s, true)
}

func (m *Manager) setState(ctx context.Context, s *sess, state string) {
	m.mu.Lock()
	if s.row.State == state || (s.closed && state != store.StateClosed) {
		m.mu.Unlock()
		return
	}
	prev := s.row.State
	s.row.State = state
	row := s.row
	m.mu.Unlock()
	m.save(ctx, row)
	switch {
	case state == store.StateClosed:
		// Empty: no finished turn and no agent process ever started (a
		// started one leaves a Claude session with history in the topic).
		u, err := m.d.Store.SessionUsage(ctx, row.ThreadID)
		empty := err == nil && u.Turns == 0 && row.ClaudeSessionID == ""
		if err := m.d.Topics.Finish(ctx, row.ThreadID, empty); err != nil {
			m.telegramFailed(s, "finish topic", err)
		}
	case state == store.StateFailed:
		if err := m.d.Topics.SetIcon(ctx, row.ThreadID, topics.IconFailed); err != nil {
			m.telegramFailed(s, "failed icon", err)
		}
	case prev == store.StateFailed:
		if err := m.d.Topics.SetIcon(ctx, row.ThreadID, topics.IconActive); err != nil {
			m.telegramFailed(s, "active icon", err)
		}
	}
}

func (m *Manager) save(ctx context.Context, row store.SessionRow) {
	if err := m.d.Store.UpdateSession(ctx, &row); err != nil {
		slog.Warn("save session", "thread", row.ThreadID, "err", err)
	}
}

func (m *Manager) say(ctx context.Context, s *sess, html string, silent bool) {
	if _, err := m.d.API.SendMessage(ctx, s.row.ThreadID, html, nil, silent); err != nil {
		m.telegramFailed(s, "send message", err)
	}
}

// maxMessage is Telegram's limit for a message text.
const maxMessage = 4096

// sayPending posts the text the agent wrote after its last tool call.
func (m *Manager) sayPending(ctx context.Context, s *sess) {
	m.mu.Lock()
	text := s.pending
	s.pending = ""
	m.mu.Unlock()
	for _, chunk := range render.Chunks(text) {
		m.say(ctx, s, chunk, true)
	}
}

// react sets a reaction on a user's message. Failures are only logged:
// the reaction is a hint, not part of the answer.
func (m *Manager) react(ctx context.Context, s *sess, msgID int, emoji string) {
	if err := m.d.API.SetReaction(ctx, msgID, emoji); err != nil {
		slog.Debug("set reaction", "thread", s.row.ThreadID, "msg", msgID, "err", err)
	}
}

// endReaction removes the processing reaction of the current turn's message.
func (m *Manager) endReaction(ctx context.Context, s *sess) {
	m.mu.Lock()
	msgID := s.turnMsg
	s.turnMsg = 0
	m.mu.Unlock()
	if msgID != 0 {
		m.react(ctx, s, msgID, "")
	}
}

func (m *Manager) edit(ctx context.Context, s *sess, msgID int, html string) {
	if err := m.d.API.EditMessage(ctx, msgID, html, nil); err != nil {
		m.telegramFailed(s, "edit message", err)
	}
}

// telegramFailed logs a failed call and closes the session when the reason
// is that the user deleted its topic.
func (m *Manager) telegramFailed(s *sess, what string, err error) {
	if errors.Is(err, telegram.ErrTopicGone) {
		m.topicGone(s)
		return
	}
	slog.Warn(what, "thread", s.row.ThreadID, "err", err)
	if errors.Is(err, telegram.ErrMessageGone) {
		m.probeTopic(s)
	}
}

// probeInterval limits topic probes after failed edits: a deleted status
// message fails every refresh.
const probeInterval = time.Minute

// probeTopic closes the session if its topic is gone. An edit in a deleted
// topic may fail as "message not found", which also means the user deleted
// just that message, so the topic itself is asked.
func (m *Manager) probeTopic(s *sess) {
	now := m.d.Now()
	m.mu.Lock()
	skip := s.closed || (!s.probedAt.IsZero() && now.Sub(s.probedAt) < probeInterval)
	if !skip {
		s.probedAt = now
	}
	row := s.row
	m.mu.Unlock()
	if skip {
		return
	}
	if ok, err := m.d.Topics.Alive(context.Background(), row.ThreadID, row.Project, row.Title); err == nil && !ok {
		m.topicGone(s)
	}
}

// topicGone closes a session whose topic was deleted: the process stops,
// open requests are withdrawn and the store marks it closed.
func (m *Manager) topicGone(s *sess) {
	ctx := context.Background()
	m.mu.Lock()
	if s.closed {
		m.mu.Unlock()
		return
	}
	s.closed = true
	delete(m.sessions, s.row.ThreadID)
	m.dropFileRefs(s.row.ThreadID)
	a := s.agent
	s.agent, s.inTurn, s.inbox = nil, false, nil
	s.row.State = store.StateClosed
	row := s.row
	m.mu.Unlock()
	slog.Info("topic deleted, session closed", "thread", row.ThreadID, "project", row.Project)
	m.stopAgents(ctx, s, a)
	if a != nil {
		_ = a.Close()
	}
	m.d.Broker.CancelThread(ctx, row.ThreadID)
	m.save(ctx, row)
	if err := m.d.Store.MarkTopicDeleted(ctx, row.ThreadID); err != nil {
		slog.Warn("mark topic deleted", "thread", row.ThreadID, "err", err)
	}
	go m.safely("schedule", nil, func() { m.schedule(ctx) })
}

// TopicRemoved drops a session whose topic the cleanup deleted.
func (m *Manager) TopicRemoved(thread int) {
	m.mu.Lock()
	s := m.sessions[thread]
	m.mu.Unlock()
	if s != nil {
		m.topicGone(s)
	}
}

// probeTopics closes idle sessions whose topics were deleted. Sessions in a
// turn find out on their next message.
func (m *Manager) probeTopics(ctx context.Context) {
	m.mu.Lock()
	var idle []*sess
	for _, s := range m.sessions {
		if !s.inTurn {
			idle = append(idle, s)
		}
	}
	m.mu.Unlock()
	for _, s := range idle {
		m.mu.Lock()
		row := s.row
		m.mu.Unlock()
		if ok, err := m.d.Topics.Alive(ctx, row.ThreadID, row.Project, row.Title); err == nil && !ok {
			m.topicGone(s)
		}
	}
}

func (m *Manager) maybeFlush(ctx context.Context, s *sess, force bool) {
	m.mu.Lock()
	now := m.d.Now()
	if s.statusMsg == 0 || !s.dirty || (!force && now.Sub(s.lastEdit) < m.d.EditInterval) {
		m.mu.Unlock()
		return
	}
	s.dirty, s.lastEdit = false, now
	text, msgID := render.StatusText(s.status, now), s.statusMsg
	m.mu.Unlock()
	if err := m.d.API.EditMessage(ctx, msgID, text, stopKeyboard(s.row.ThreadID)); err != nil {
		m.telegramFailed(s, "edit status", err)
	}
}

// Run refreshes status messages of running turns until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	interval := m.d.EditInterval
	if interval < time.Second {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	probe := time.NewTicker(m.d.ProbeInterval)
	defer probe.Stop()
	m.safely("probeTopics", nil, func() { m.probeTopics(ctx) })
	for {
		select {
		case <-ctx.Done():
			return
		case <-probe.C:
			m.safely("probeTopics", nil, func() { m.probeTopics(ctx) })
		case <-t.C:
			m.runTick(ctx)
		}
	}
}

// runTick is one beat of Run: time limits, status messages, agent panels.
// A panic in one step does not stop the others or the loop.
func (m *Manager) runTick(ctx context.Context) {
	m.safely("tick", nil, func() { m.tick(ctx) })
	m.mu.Lock()
	var live []*sess
	for _, s := range m.sessions {
		if s.inTurn && s.statusMsg != 0 {
			s.dirty = true
			live = append(live, s)
		}
	}
	m.mu.Unlock()
	for _, s := range live {
		m.safely("status refresh", s, func() { m.maybeFlush(ctx, s, false) })
	}
	m.safely("agent panels", nil, func() { m.refreshPanels(ctx) })
}

// interrupt stops the running turn and withdraws its open prompts. The CLI
// ends such a turn with an error result, which finishTurn reports as an
// interruption rather than a failure.
func (m *Manager) interrupt(ctx context.Context, s *sess, a agent.Session) error {
	m.mu.Lock()
	s.interrupted = true
	m.mu.Unlock()
	if err := a.Interrupt(ctx); err != nil {
		m.mu.Lock()
		s.interrupted = false
		m.mu.Unlock()
		return err
	}
	m.d.Broker.CancelThread(ctx, s.row.ThreadID)
	return nil
}

// Stop interrupts the current turn.
func (m *Manager) Stop(ctx context.Context, thread int) error {
	m.mu.Lock()
	s := m.sessions[thread]
	var a agent.Session
	busy := false
	if s != nil {
		a, busy = s.agent, s.inTurn
	}
	m.mu.Unlock()
	if s == nil {
		return ErrUnknownSession
	}
	if a == nil || !busy {
		m.say(ctx, s, "Сейчас ничего не выполняется.", true)
		return nil
	}
	if err := m.interrupt(ctx, s, a); err != nil {
		return err
	}
	m.say(ctx, s, "⏹ Прерываю ход…", true)
	m.mu.Lock()
	n := m.runningAgents(s)
	m.mu.Unlock()
	if n > 0 {
		m.say(ctx, s, fmt.Sprintf("🤖 Агенты ещё работают: %d. Остановить можно в панели (/agents).", n), true)
	}
	return nil
}

// SetMode changes the permission mode of the session.
func (m *Manager) SetMode(ctx context.Context, thread int, mode string) error {
	if !modes[mode] {
		return errors.New("режим: default, acceptEdits или plan")
	}
	m.mu.Lock()
	s := m.sessions[thread]
	if s == nil {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	a := s.agent
	m.mu.Unlock()
	// The live process first: a mode it refused must not be stored.
	if a != nil {
		if err := a.SetPermissionMode(ctx, mode); err != nil {
			return err
		}
	}
	m.mu.Lock()
	s.row.Mode = mode
	row := s.row
	m.mu.Unlock()
	m.save(ctx, row)
	m.say(ctx, s, "🔧 Режим разрешений: <b>"+mode+"</b>", true)
	return nil
}

// Close ends the session and closes its topic.
func (m *Manager) Close(ctx context.Context, thread int) error {
	m.mu.Lock()
	s := m.sessions[thread]
	if s == nil {
		m.mu.Unlock()
		return ErrUnknownSession
	}
	delete(m.sessions, thread)
	m.dropFileRefs(thread)
	a := s.agent
	marked, notices := []int{s.turnMsg}, []int{}
	for _, it := range s.inbox {
		marked, notices = append(marked, it.msg), append(notices, it.notice)
	}
	s.agent, s.inTurn, s.inbox, s.turnMsg, s.closed = nil, false, nil, 0, true
	m.mu.Unlock()
	for _, id := range marked {
		if id != 0 {
			m.react(ctx, s, id, "")
		}
	}
	for _, id := range notices {
		if id != 0 {
			_ = m.d.API.DeleteMessage(ctx, id)
		}
	}
	m.stopAgents(ctx, s, a)
	if a != nil {
		_ = a.Close()
	}
	m.d.Broker.CancelThread(ctx, thread)
	m.say(ctx, s, "✅ Сессия закрыта.", true)
	m.setState(ctx, s, store.StateClosed)
	m.schedule(ctx)
	return nil
}

// dropFileRefs forgets the file buttons of a closed session. Callers hold m.mu.
func (m *Manager) dropFileRefs(thread int) {
	for k, r := range m.fileRefs {
		if r.thread == thread {
			delete(m.fileRefs, k)
		}
	}
	for k, r := range m.turnRefs {
		if r.thread == thread {
			delete(m.turnRefs, k)
		}
	}
}

// lookup returns the session of a topic, nil if there is none.
func (m *Manager) lookup(thread int) *sess {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[thread]
}

// Owns reports whether the topic belongs to a session of this node.
func (m *Manager) Owns(thread int) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.sessions[thread]
	return ok
}

// List returns the open sessions, oldest first.
func (m *Manager) List() []store.SessionRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]store.SessionRow, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Shutdown stops all claude processes and keeps session states, so the next
// Restore marks running turns as interrupted.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.stopping = true
	var agents []agent.Session
	for _, s := range m.sessions {
		if s.agent != nil {
			agents = append(agents, s.agent)
		}
	}
	m.mu.Unlock()
	for _, a := range agents {
		_ = a.Close()
	}
}

func shortTitle(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if r := []rune(line); len(r) > 40 {
		return string(r[:39]) + "…"
	}
	return line
}

const telegramPrompt = "You are driven from Telegram. When the user should read a file you wrote " +
	"(a spec, plan, report or similar document), call the mcp__tgsync__send_file tool with its path " +
	"and a short caption instead of only mentioning the path. " +
	"The user reads your replies on a phone and only your last message of a turn is posted in full: " +
	"make it short and lead with the outcome. Skip recaps of the steps you took, " +
	"and keep remarks between tool calls to one line or leave them out. " +
	"The user approves commands in Telegram and sees the Bash description next to the command: " +
	"write it in the user's language and say briefly why you run the command and what it gives you."

// sudoPrompt tells the agent that sudo works through Telegram approval;
// without it the agent probes with sudo -n and gives up.
const sudoPrompt = "sudo is available: run sudo commands as usual. The user approves each one in Telegram " +
	"and the password is supplied through SUDO_ASKPASS. Do not test access with sudo -n " +
	"and do not ask the user to run sudo commands or edit sudoers."

func (m *Manager) sendFileTool(s *sess) agent.Tool {
	return agent.Tool{
		Name:        "send_file",
		Description: "Send a file from the project to the user in Telegram so they can read it. Use it for documents the user should review.",
		Schema: map[string]any{"type": "object", "properties": map[string]any{
			"path":    map[string]any{"type": "string", "description": "file path, relative to the project or absolute inside it"},
			"caption": map[string]any{"type": "string", "description": "short note shown with the file"},
		}, "required": []any{"path"}},
		Handler: func(args map[string]any) (out string, err error) {
			// Called on the agent library's goroutine: a panic here would
			// end the node.
			defer func() {
				if r := recover(); r != nil {
					m.crashed("send_file", s, r)
					out, err = "", errors.New("tgsync: внутренняя ошибка при отправке файла")
				}
			}()
			p, _ := args["path"].(string)
			caption, _ := args["caption"].(string)
			sent, err := m.sendFile(context.Background(), s, p, caption, false)
			switch {
			case err != nil:
				return "", err
			case !sent:
				return "Эта версия файла уже была отправлена пользователю.", nil
			}
			return "Файл отправлен пользователю.", nil
		},
	}
}

// sendFile uploads a project file to the session topic. Without force a
// version that was already sent is skipped and sent reports false.
func (m *Manager) sendFile(ctx context.Context, s *sess, p, caption string, force bool) (bool, error) {
	rel, data, err := files.Read(s.row.Cwd, p, m.d.Protected)
	if err != nil {
		return false, err
	}
	h := sha256.Sum256(data)
	sum := hex.EncodeToString(h[:])
	if !force && s.files.WasSent(rel, sum) {
		return false, nil
	}
	text := "📄 <code>" + render.Escape(rel) + "</code>"
	if caption = strings.TrimSpace(caption); caption != "" {
		text += "\n" + render.Escape(caption)
	}
	if _, err := m.d.API.SendDocument(ctx, s.row.ThreadID, filepath.Base(rel), data, text, false); err != nil {
		m.telegramFailed(s, "send document", err)
		return false, err
	}
	s.files.MarkSent(rel, sum)
	return true, nil
}

// maxAutoSend caps files sent without a request per turn; the rest stay
// reachable through the buttons.
const maxAutoSend = 5

// deliverFiles runs after a turn: it sends documents the agent pointed to
// and files matching AUTO_SEND_GLOBS, then lists the files changed in the turn.
func (m *Manager) deliverFiles(ctx context.Context, s *sess, text string, changed []string, turn turnInfo) {
	defer func() {
		if r := recover(); r != nil {
			m.crashed("deliverFiles", s, r)
			m.safely("deliverFiles recovery", s, func() { m.say(ctx, s, internalError+" Сводка хода не собрана.", true) })
		}
	}()
	seen := map[string]bool{}
	var send []string
	add := func(rel string) {
		if !seen[rel] && len(send) < maxAutoSend {
			seen[rel] = true
			send = append(send, rel)
		}
	}
	for _, rel := range s.files.Mentioned(text) {
		if files.IsDocument(rel) {
			add(rel)
		}
	}
	for _, rel := range changed {
		if files.Match(m.d.AutoSend, rel) {
			add(rel)
		}
	}
	for _, rel := range send {
		if _, err := m.sendFile(ctx, s, rel, "", false); err != nil && !errors.Is(err, telegram.ErrTopicGone) {
			m.say(ctx, s, "⚠️ "+render.Escape(err.Error()), true)
		}
	}
	if len(changed) == 0 {
		return
	}
	const maxListed = 10
	stats := m.turnStats(ctx, s, turn, changed)
	header := fmt.Sprintf("📎 Изменено за ход: %d", len(changed))
	if stats != nil {
		add, del := 0, 0
		for _, st := range stats {
			add, del = add+st.Added, del+st.Deleted
		}
		header += fmt.Sprintf(" · +%d −%d", add, del)
	}
	lines := []string{header}
	var kb telegram.Keyboard
	for i, rel := range changed {
		if i == maxListed {
			lines = append(lines, fmt.Sprintf("…и ещё %d", len(changed)-maxListed))
			break
		}
		mark := ""
		if s.files.IsSent(rel) {
			mark = " ✓"
		}
		lines = append(lines, "• <code>"+render.Escape(rel)+"</code>"+statText(stats, rel)+mark)
	}
	turnKey := ""
	m.mu.Lock()
	if stats != nil {
		m.fileSeq++
		turnKey = strconv.Itoa(m.fileSeq)
		kb = append(kb, turnRow(turnKey, true))
	}
	m.mu.Unlock()
	html := strings.Join(lines, "\n")
	id, err := m.d.API.SendMessage(ctx, s.row.ThreadID, html, kb, true)
	if err != nil {
		m.telegramFailed(s, "send file list", err)
		return
	}
	if turnKey != "" {
		m.mu.Lock()
		m.turnRefs[turnKey] = turnRef{thread: s.row.ThreadID, turn: turn.no, base: turn.base, end: turn.end,
			files: changed, msg: id, html: html, kb: kb}
		s.summaries = append(s.summaries, turnKey)
		for len(s.summaries) > maxTurnSummaries {
			delete(m.turnRefs, s.summaries[0])
			s.summaries = s.summaries[1:]
		}
		m.mu.Unlock()
	}
}

// HandleFileButton handles f:<n> (send file) buttons of the /ls browser.
// It returns an alert for the user and whether the data was a file button.
func (m *Manager) HandleFileButton(ctx context.Context, u telegram.Update) (string, bool) {
	kind, key, ok := strings.Cut(u.CallbackData, ":")
	if !ok || kind != "f" {
		return "", false
	}
	m.mu.Lock()
	ref, known := m.fileRefs[key]
	s := m.sessions[ref.thread]
	m.mu.Unlock()
	if !known || s == nil || ref.thread != u.ThreadID {
		return "Кнопка устарела", true
	}
	if _, err := m.sendFile(ctx, s, ref.rel, "", true); err != nil {
		return alertText(err), true
	}
	return "", true
}

// alertText fits an error into a callback alert (Telegram limit: 200 characters).
func alertText(err error) string {
	if r := []rune(err.Error()); len(r) > 190 {
		return string(r[:189]) + "…"
	}
	return err.Error()
}

// SendFile sends a project file on request (/file), even if it was sent before.
func (m *Manager) SendFile(ctx context.Context, thread int, p string) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	if strings.TrimSpace(p) == "" {
		return errors.New("укажи путь: /file docs/plan.md")
	}
	_, err := m.sendFile(ctx, s, strings.TrimSpace(p), "", true)
	return err
}

func (m *Manager) agentEnv(thread int) map[string]string {
	if m.d.AgentEnv == nil {
		return nil
	}
	return m.d.AgentEnv(thread)
}

// tick enforces the time limits: it stops idle processes, warns about
// quiet turns, interrupts overlong turns and reminds about open prompts.
func (m *Manager) tick(ctx context.Context) {
	now := m.d.Now()
	var idle []agent.Session
	var stalled []stall
	var overtime []*sess
	m.mu.Lock()
	for _, s := range m.sessions {
		if !s.inTurn && s.agent != nil && m.d.IdleTimeout > 0 && !s.idleSince.IsZero() &&
			now.Sub(s.idleSince) >= m.d.IdleTimeout && m.runningAgents(s) == 0 {
			idle = append(idle, s.agent)
			s.agent = nil
			continue
		}
		if !s.inTurn {
			continue
		}
		base := s.status.LastEvent
		if s.stallFrom.After(base) {
			base = s.stallFrom
		}
		if m.d.StallWarn > 0 && s.waits == 0 && !s.warned && now.Sub(base) >= m.d.StallWarn {
			s.warned = true
			stalled = append(stalled, stall{s, now.Sub(s.status.LastEvent)})
		}
		if m.d.MaxTurn > 0 && !s.overtime && now.Sub(s.status.Started) >= m.d.MaxTurn && !now.Before(s.retryAt) {
			s.overtime = true
			// Claimed for a minute: a failed interrupt is retried (and
			// reported) once a minute, not on every tick.
			s.retryAt = now.Add(interruptRetry)
			overtime = append(overtime, s)
		}
	}
	m.mu.Unlock()
	for _, a := range idle {
		_ = a.Close() // the next message resumes the session
	}
	// Sending may wait for Telegram; keep the ticker free.
	go m.notifyTimers(ctx, now, stalled, overtime)
}

// interruptRetry is how often MAX_TURN_DURATION retries a failed interrupt.
const interruptRetry = time.Minute

// stall is a turn without activity; quiet is measured under m.mu.
type stall struct {
	s     *sess
	quiet time.Duration
}

func (m *Manager) notifyTimers(ctx context.Context, now time.Time, stalled []stall, overtime []*sess) {
	defer func() {
		if r := recover(); r != nil {
			m.crashed("notifyTimers", nil, r)
		}
	}()
	for _, st := range stalled {
		key := strconv.Itoa(st.s.row.ThreadID)
		kb := telegram.Keyboard{{{Text: "⏳ Ждать", Data: "w:" + key}, {Text: "⏹ Остановить", Data: "x:" + key}}}
		text := fmt.Sprintf("⏳ Нет активности %s. Агент может ждать долгую команду.", render.Duration(st.quiet))
		if _, err := m.d.API.SendMessage(ctx, st.s.row.ThreadID, text, kb, false); err != nil {
			m.telegramFailed(st.s, "stall warning", err)
		}
	}
	for _, s := range overtime {
		m.mu.Lock()
		a := s.agent
		m.mu.Unlock()
		if a != nil {
			if err := m.interrupt(ctx, s, a); err != nil {
				m.mu.Lock()
				s.overtime = false // tick tries again once retryAt has passed
				m.mu.Unlock()
				m.say(ctx, s, fmt.Sprintf("⚠️ Ход идёт дольше MAX_TURN_DURATION (%s), но прервать его не удалось: %s. Попробую снова через минуту.",
					render.Duration(m.d.MaxTurn), render.Escape(err.Error())), false)
				continue
			}
		}
		m.say(ctx, s, fmt.Sprintf("⏱ Ход идёт дольше MAX_TURN_DURATION (%s), прерываю.", render.Duration(m.d.MaxTurn)), false)
	}
	m.d.Broker.Remind(ctx, now, m.d.RemindEvery)
}

// HandleButton handles the session buttons: files (f:, d:) and the stall
// warning (w: wait, x: stop). It reports whether the data was one of them.
func (m *Manager) HandleButton(ctx context.Context, u telegram.Update) (string, bool) {
	kind, key, ok := strings.Cut(u.CallbackData, ":")
	if !ok {
		return "", false
	}
	switch kind {
	case "f":
		return m.HandleFileButton(ctx, u)
	case "l":
		m.mu.Lock()
		ref, known := m.fileRefs[key]
		s := m.sessions[ref.thread]
		m.mu.Unlock()
		if !known || !ref.dir || s == nil || ref.thread != u.ThreadID {
			return "Кнопка устарела", true
		}
		if err := m.showDir(ctx, s, ref.rel, ref.offset, u.MessageID); err != nil {
			return alertText(err), true
		}
		return "", true
	case "td", "tc", "tr", "ty", "tn":
		return m.turnButton(ctx, u, kind, key), true
	case "md", "cl", "cn", "cy", "ls", "mo":
		// The key is always a session's thread ID; a non-numeric key means
		// the prefix belongs to another feature (e.g. the group's "cl:ask"
		// cleanup button) and is left for the caller to handle.
		thread, err := strconv.Atoi(key)
		if err != nil {
			return "", false
		}
		if thread != u.ThreadID {
			return "Кнопка устарела", true
		}
		return m.sessionAction(ctx, kind, thread, u.MessageID), true
	case "ms":
		ts, mode, _ := strings.Cut(key, ":")
		thread, err := strconv.Atoi(ts)
		if err != nil || thread != u.ThreadID {
			return "Кнопка устарела", true
		}
		if err := m.SetMode(ctx, thread, mode); err != nil {
			return alertText(err), true
		}
		_ = m.d.API.EditMessage(ctx, u.MessageID, "⚙ Режим разрешений: <b>"+render.Escape(mode)+"</b>", nil)
		return "", true
	case "qn":
		item, err := strconv.Atoi(key)
		if err != nil {
			return "Кнопка устарела", true
		}
		return m.sendNow(ctx, u.ThreadID, item, u.MessageID), true
	case "ag":
		return m.HandleAgentButton(ctx, u), true
	case "cx":
		thread, err := strconv.Atoi(key)
		if err != nil || thread != u.ThreadID {
			return "Кнопка устарела", true
		}
		alert, err := m.compact(ctx, thread)
		if err != nil {
			return alertText(err), true
		}
		return alert, true
	case "w", "x":
		thread, err := strconv.Atoi(key)
		if err != nil || thread != u.ThreadID {
			return "Кнопка устарела", true
		}
		if kind == "x" {
			if err := m.Stop(ctx, thread); err != nil {
				return alertText(err), true
			}
			return "", true
		}
		m.mu.Lock()
		if s := m.sessions[thread]; s != nil {
			s.stallFrom, s.warned = m.d.Now(), false
		}
		m.mu.Unlock()
		return "Жду дальше", true
	}
	return "", false
}

// initSummary reports MCP servers that failed to connect, "" if all are fine.
func initSummary(in *agent.InitInfo) string {
	var bad []string
	for _, s := range in.MCPServers {
		if s.Status != "connected" && s.Status != "pending" {
			bad = append(bad, render.Escape(s.Name)+" ⚠ "+render.Escape(s.Status))
		}
	}
	if len(bad) == 0 {
		return ""
	}
	return "🔌 MCP: " + strings.Join(bad, ", ") + "\nАвторизуй их в интерактивном claude: в сессиях бота OAuth не пройти."
}

// Skills posts the slash commands the agent offers (from the last init).
func (m *Manager) Skills(ctx context.Context, thread int) error {
	m.mu.Lock()
	s := m.sessions[thread]
	var cmds []string
	if s != nil {
		cmds = append(cmds, s.commands...)
	}
	m.mu.Unlock()
	if s == nil {
		return ErrUnknownSession
	}
	if len(cmds) == 0 {
		m.say(ctx, s, "Список команд появится после первого ответа агента.", true)
		return nil
	}
	sort.Strings(cmds)
	lines := make([]string, len(cmds))
	for i, c := range cmds {
		lines[i] = "/" + c
	}
	for _, chunk := range render.Chunks(fmt.Sprintf("Команд агента: %d. Любую можно отправить сообщением.\n", len(cmds)) + strings.Join(lines, "\n")) {
		m.say(ctx, s, chunk, true)
	}
	return nil
}

// inboxDir is where files sent by the user are stored, inside the project.
const inboxDir = ".tgsync/inbox"

// ReceiveFile stores a file the user sent into the project inbox. With a
// caption it goes to the agent right away; without one it is attached to the
// user's next message.
func (m *Manager) ReceiveFile(ctx context.Context, thread int, name string, data []byte, caption string) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	dir := filepath.Join(s.row.Cwd, filepath.FromSlash(inboxDir))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fname := m.d.Now().Format("20060102-150405") + "-" + files.SafeName(name)
	if err := os.WriteFile(filepath.Join(dir, fname), data, 0o600); err != nil {
		return err
	}
	excludeFromGit(s.row.Cwd)
	entry := fmt.Sprintf("%s/%s (%s)", inboxDir, fname, files.Size(int64(len(data))))
	m.mu.Lock()
	s.inbox2 = append(s.inbox2, entry)
	m.mu.Unlock()
	if strings.TrimSpace(caption) == "" {
		m.say(ctx, s, "📎 Получен <code>"+render.Escape(inboxDir+"/"+fname)+"</code>, передам со следующим сообщением.", true)
		return nil
	}
	return m.Message(ctx, thread, caption)
}

// excludeFromGit keeps the inbox out of commits via .git/info/exclude.
func excludeFromGit(projectDir string) {
	gitDir := filepath.Join(projectDir, ".git")
	if st, err := os.Stat(gitDir); err != nil || !st.IsDir() {
		return
	}
	path := filepath.Join(gitDir, "info", "exclude")
	old, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(old), "\n") {
		if strings.TrimSpace(line) == ".tgsync/" {
			return
		}
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if len(old) > 0 && !strings.HasSuffix(string(old), "\n") {
		_, _ = f.WriteString("\n")
	}
	_, _ = f.WriteString(".tgsync/\n")
}

// hiddenDirs are folders /ls does not show.
var hiddenDirs = map[string]bool{".git": true, "node_modules": true, ".venv": true, "__pycache__": true}

const lsPage = 30

// maxListings and maxTurnSummaries cap the buttons kept per session: older
// /ls messages and turn summaries answer «Кнопка устарела».
const (
	maxListings      = 5
	maxTurnSummaries = 5
)

// ListDir posts a browsable listing of a project folder (/ls).
func (m *Manager) ListDir(ctx context.Context, thread int, rel string, offset int) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	return m.showDir(ctx, s, rel, offset, 0)
}

// showDir renders a folder page; with editMsg it updates that message in place.
func (m *Manager) showDir(ctx context.Context, s *sess, rel string, offset, editMsg int) error {
	abs, rel, err := files.ResolveDir(s.row.Cwd, rel)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	protected := map[string]bool{}
	for _, p := range m.d.Protected {
		protected[filepath.Clean(p)] = true
	}
	var dirs, regular []os.DirEntry
	for _, e := range entries {
		switch {
		case protected[filepath.Join(abs, e.Name())]:
		case e.IsDir() && hiddenDirs[e.Name()]:
		case e.IsDir():
			dirs = append(dirs, e)
		default:
			regular = append(regular, e)
		}
	}
	all := append(dirs, regular...)
	if offset < 0 || offset >= len(all) {
		offset = 0
	}
	end := offset + lsPage
	if end > len(all) {
		end = len(all)
	}
	text := fmt.Sprintf("📂 <code>%s</code> · %d", render.Escape("/"+filepath.ToSlash(rel)), len(all))
	if len(all) > lsPage {
		text += fmt.Sprintf(" · %d–%d", offset+1, end)
	}
	if len(all) == 0 {
		text += "\nПапка пуста."
	}
	var kb telegram.Keyboard
	m.mu.Lock()
	page := &listing{msg: editMsg}
	ref := func(r fileRef) string {
		m.fileSeq++
		key := strconv.Itoa(m.fileSeq)
		m.fileRefs[key] = r
		page.keys = append(page.keys, key)
		return key
	}
	for _, e := range all[offset:end] {
		child := filepath.Join(rel, e.Name())
		if e.IsDir() {
			kb = append(kb, []telegram.Button{{Text: "📁 " + e.Name(), Data: "l:" + ref(fileRef{thread: s.row.ThreadID, rel: child, dir: true})}})
			continue
		}
		label := "📄 " + e.Name()
		if info, err := e.Info(); err == nil {
			label += " · " + files.Size(info.Size())
		}
		kb = append(kb, []telegram.Button{{Text: label, Data: "f:" + ref(fileRef{thread: s.row.ThreadID, rel: child})}})
	}
	var nav []telegram.Button
	if rel != "" {
		parent := filepath.Dir(rel)
		if parent == "." {
			parent = ""
		}
		nav = append(nav, telegram.Button{Text: "⬆ ..", Data: "l:" + ref(fileRef{thread: s.row.ThreadID, rel: parent, dir: true})})
	}
	if end < len(all) {
		nav = append(nav, telegram.Button{Text: "➡ ещё", Data: "l:" + ref(fileRef{thread: s.row.ThreadID, rel: rel, dir: true, offset: end})})
	}
	m.keepListing(s, page)
	m.mu.Unlock()
	if len(nav) > 0 {
		kb = append(kb, nav)
	}
	if editMsg != 0 {
		return m.d.API.EditMessage(ctx, editMsg, text, kb)
	}
	id, err := m.d.API.SendMessage(ctx, s.row.ThreadID, text, kb, true)
	m.mu.Lock()
	page.msg = id
	m.mu.Unlock()
	return err
}

// keepListing records the buttons of an /ls page. A page shown in place
// of another retires that page's buttons; beyond maxListings messages the
// oldest lose theirs. Callers hold m.mu.
func (m *Manager) keepListing(s *sess, page *listing) {
	kept := s.listings[:0]
	for _, l := range s.listings {
		if page.msg != 0 && l.msg == page.msg {
			m.dropKeys(l.keys)
			continue
		}
		kept = append(kept, l)
	}
	kept = append(kept, page)
	for len(kept) > maxListings {
		m.dropKeys(kept[0].keys)
		kept = kept[1:]
	}
	s.listings = kept
}

// dropKeys forgets file buttons. Callers hold m.mu.
func (m *Manager) dropKeys(keys []string) {
	for _, k := range keys {
		delete(m.fileRefs, k)
	}
}

// queueNotice tells that a message waits for the current turn and offers to
// send it right away. The notice is deleted when the message's turn starts.
func (m *Manager) queueNotice(ctx context.Context, s *sess, it *inboxItem) {
	kb := telegram.Keyboard{{{Text: "⚡ Отправить сейчас", Data: "qn:" + strconv.Itoa(it.id)}}}
	id, err := m.d.API.SendMessage(ctx, s.row.ThreadID, "📥 В очереди, уйдёт следующим ходом.", kb, true)
	if err != nil {
		m.telegramFailed(s, "send queue notice", err)
		return
	}
	m.mu.Lock()
	it.notice = id
	started := it.started
	m.mu.Unlock()
	if started {
		_ = m.d.API.DeleteMessage(ctx, id)
	}
}

// sendNow moves a queued message to the front and interrupts the running
// turn, so the message goes out as soon as the agent stops.
func (m *Manager) sendNow(ctx context.Context, thread, item, notice int) string {
	m.mu.Lock()
	s := m.sessions[thread]
	var it *inboxItem
	if s != nil {
		for i, x := range s.inbox {
			if x.id == item {
				it = x
				s.inbox = append([]*inboxItem{x}, append(s.inbox[:i:i], s.inbox[i+1:]...)...)
				break
			}
		}
	}
	var a agent.Session
	busy := false
	if it != nil {
		a, busy = s.agent, s.inTurn
	}
	m.mu.Unlock()
	if it == nil {
		return "Уже отправлено"
	}
	if a == nil || !busy {
		_ = m.d.API.EditMessage(ctx, notice, "📥 Уйдёт первым.", nil)
		return ""
	}
	if err := m.interrupt(ctx, s, a); err != nil {
		return alertText(err)
	}
	_ = m.d.API.EditMessage(ctx, notice, "⚡ Прерываю ход, отправляю…", nil)
	return ""
}

func stopKeyboard(thread int) telegram.Keyboard {
	return telegram.Keyboard{{{Text: "⏹ Остановить", Data: "x:" + strconv.Itoa(thread)}}}
}

// resultKeyboard is the panel under a finished turn.
func (m *Manager) resultKeyboard(s *sess) telegram.Keyboard {
	t := strconv.Itoa(s.row.ThreadID)
	return telegram.Keyboard{{{Text: "⋯", Data: "mo:" + t}}}
}

// panelKeyboard is the result panel after ⋯ is pressed.
func panelKeyboard(thread int) telegram.Keyboard {
	t := strconv.Itoa(thread)
	return telegram.Keyboard{{
		{Text: "📂 Файлы", Data: "ls:" + t},
		{Text: "⚙ Режим", Data: "md:" + t},
		{Text: "✖ Закрыть", Data: "cl:" + t},
	}}
}

var modeHelp = []struct{ mode, text string }{
	{"default", "default — опасные действия спрашивают разрешение"},
	{"acceptEdits", "acceptEdits — правки файлов без вопросов"},
	{"plan", "plan — только план, без изменений"},
}

// sessionAction handles the panel buttons: mode picker and close with confirmation.
func (m *Manager) sessionAction(ctx context.Context, kind string, thread, msgID int) string {
	m.mu.Lock()
	s := m.sessions[thread]
	mode := ""
	if s != nil {
		mode = s.row.Mode
	}
	m.mu.Unlock()
	if s == nil {
		return "Сессия уже закрыта"
	}
	t := strconv.Itoa(thread)
	switch kind {
	case "mo":
		if err := m.d.API.EditKeyboard(ctx, msgID, panelKeyboard(thread)); err != nil {
			return alertText(err)
		}
	case "ls":
		if err := m.showDir(ctx, s, "", 0, 0); err != nil {
			return alertText(err)
		}
	case "md":
		var kb telegram.Keyboard
		for _, h := range modeHelp {
			kb = append(kb, []telegram.Button{{Text: h.text, Data: "ms:" + t + ":" + h.mode}})
		}
		_, _ = m.d.API.SendMessage(ctx, thread, "⚙ Режим разрешений сейчас: <b>"+render.Escape(mode)+"</b>. Выбери новый:", kb, true)
	case "cl":
		kb := telegram.Keyboard{{{Text: "Да, закрыть", Data: "cy:" + t}, {Text: "Отмена", Data: "cn:" + t}}}
		_, _ = m.d.API.SendMessage(ctx, thread, "Закрыть сессию? Процесс остановится, тема закроется. Продолжить её потом можно через /history.", kb, true)
	case "cn":
		_ = m.d.API.EditMessage(ctx, msgID, "Сессия остаётся открытой.", nil)
	case "cy":
		_ = m.d.API.EditMessage(ctx, msgID, "Закрываю сессию.", nil)
		if err := m.Close(ctx, thread); err != nil {
			return alertText(err)
		}
	}
	return ""
}
