package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// trackedAgent is one subagent of a session. Guarded by m.mu.
type trackedAgent struct {
	n          int // manager-wide number used by the panel buttons
	id         string
	toolUseID  string
	name       string
	desc       string
	state      string // running, completed, failed, stopped
	started    time.Time
	took       time.Duration
	toolUses   int
	lastTool   string
	summary    string
	outputFile string
	result     string    // text of the Agent tool result, if it came
	parent     string    // tool use id of the calling agent's Agent call; empty for the main agent
	action     string    // last tool call of the agent, as a status line
	ended      time.Time // when it finished
	endN       int       // finish order: agents may end on the same clock tick (Windows)
}

// agentPanel is the message listing the agents started while it was open.
type agentPanel struct {
	msg      int
	agents   []*trackedAgent
	dirty    bool
	lastEdit time.Time
	view     int  // 0: list; else the number of the agent whose card is open
	posting  bool // the first post is in flight
}

// agentCall is an Agent tool call, kept until its task starts.
type agentCall struct {
	name   string    // subagent type
	parent string    // tool use id of the calling agent's Agent call
	at     time.Time // when it was made
}

// staleCall is how long an Agent call waits for its task before it is forgotten.
const staleCall = 10 * time.Minute

type agentRef struct {
	thread int
	a      *trackedAgent
}

func (p *agentPanel) running() int {
	n := 0
	for _, a := range p.agents {
		if a.state == "running" {
			n++
		}
	}
	return n
}

// runningAgents counts the session's running agents. Callers hold m.mu.
func (m *Manager) runningAgents(s *sess) int {
	if s.panel == nil {
		return 0
	}
	return s.panel.running()
}

// findAgent returns the agent started by a tool call. Callers hold m.mu.
func (m *Manager) findAgent(s *sess, toolUseID string) *trackedAgent {
	if s.panel == nil || toolUseID == "" {
		return nil
	}
	for _, a := range s.panel.agents {
		if a.toolUseID == toolUseID {
			return a
		}
	}
	return nil
}

func (m *Manager) agentByID(s *sess, id string) *trackedAgent {
	if s.panel == nil {
		return nil
	}
	for _, a := range s.panel.agents {
		if a.id == id {
			return a
		}
	}
	return nil
}

// maxListed is how many agents the list shows; the rest collapse into counts.
const maxListed = 12

// listing picks the listed agents (running first, then the most recently
// finished, 12 at most) and orders them as a tree under their callers.
// Callers hold m.mu.
func (m *Manager) listing(p *agentPanel) ([]render.AgentEntry, []*trackedAgent, render.Hidden) {
	var running, finished []*trackedAgent
	for _, a := range p.agents {
		if a.state == "running" {
			running = append(running, a)
		} else {
			finished = append(finished, a)
		}
	}
	var hidden render.Hidden
	if len(running) > maxListed {
		hidden.Running = len(running) - maxListed
		running = running[:maxListed]
	}
	sort.SliceStable(finished, func(i, j int) bool { return finished[i].endN > finished[j].endN })
	if room := maxListed - len(running); len(finished) > room {
		hidden.Done = len(finished) - room
		finished = finished[:room]
	}
	listed := map[*trackedAgent]bool{}
	for _, a := range running {
		listed[a] = true
	}
	for _, a := range finished {
		listed[a] = true
	}
	byToolUse := map[string]*trackedAgent{}
	for _, a := range p.agents {
		if listed[a] && a.toolUseID != "" {
			byToolUse[a.toolUseID] = a
		}
	}
	children := map[*trackedAgent][]*trackedAgent{}
	var roots []*trackedAgent
	for _, a := range p.agents { // start order
		if !listed[a] {
			continue
		}
		if caller := byToolUse[a.parent]; a.parent != "" && caller != nil && caller != a {
			children[caller] = append(children[caller], a)
		} else {
			roots = append(roots, a)
		}
	}
	var entries []render.AgentEntry
	var order []*trackedAgent
	var walk func(a *trackedAgent, depth int)
	walk = func(a *trackedAgent, depth int) {
		entries = append(entries, render.AgentEntry{Name: a.name, Description: a.desc, State: a.state, Depth: depth})
		order = append(order, a)
		for _, c := range children[a] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	return entries, order, hidden
}

// listView is the compact list with a button per listed agent. Callers hold m.mu.
func (m *Manager) listView(p *agentPanel) (string, telegram.Keyboard) {
	entries, order, hidden := m.listing(p)
	var kb telegram.Keyboard
	var row []telegram.Button
	for _, a := range order {
		row = append(row, telegram.Button{Text: render.AgentEmoji(a.state) + " " + cutLabel(a.name),
			Data: "ag:" + strconv.Itoa(a.n) + ":c"})
		if len(row) == 3 {
			kb, row = append(kb, row), nil
		}
	}
	if len(row) > 0 {
		kb = append(kb, row)
	}
	return render.AgentsList(entries, hidden), kb
}

// panelView renders the panel in its current view. Callers hold m.mu.
func (m *Manager) panelView(p *agentPanel, now time.Time) (string, telegram.Keyboard) {
	var a *trackedAgent
	for _, x := range p.agents {
		if x.n == p.view {
			a = x
		}
	}
	if p.view == 0 || a == nil {
		p.view = 0
		return m.listView(p)
	}
	caller := ""
	if a.parent != "" {
		caller = "другой субагент"
		for _, x := range p.agents {
			if x.toolUseID == a.parent {
				caller = x.name
			}
		}
	}
	action := a.action
	if action == "" && a.lastTool != "" {
		action = "🔧 " + a.lastTool
	}
	text := render.AgentCard(render.AgentCardInfo{Name: a.name, Description: a.desc, State: a.state, Caller: caller,
		Action: action, Summary: a.summary, Started: a.started, Took: a.took, ToolUses: a.toolUses}, now)
	key := "ag:" + strconv.Itoa(a.n) + ":"
	row := []telegram.Button{{Text: "⬅ Назад", Data: key + "b"}}
	if a.state == "running" {
		row = append(row, telegram.Button{Text: "⏹ Остановить", Data: key + "s"})
	} else {
		row = append(row, telegram.Button{Text: "📄 Результат", Data: key + "o"})
	}
	return text, telegram.Keyboard{row}
}

// noteAction records a subagent's tool call as its current action.
func (m *Manager) noteAction(ctx context.Context, s *sess, parentToolUseID, line string) {
	m.mu.Lock()
	a := m.findAgent(s, parentToolUseID)
	if a == nil || a.state != "running" {
		m.mu.Unlock()
		return
	}
	a.action = line
	shown := s.panel.view == a.n // only the agent's card shows it
	s.panel.dirty = s.panel.dirty || shown
	m.mu.Unlock()
	if shown {
		m.flushAgents(ctx, s, false)
	}
}

// onTask applies a task lifecycle event to the session's agents panel.
func (m *Manager) onTask(ctx context.Context, s *sess, ev agent.Event) {
	t := ev.Task
	now := m.d.Now()
	m.mu.Lock()
	switch ev.Kind {
	case agent.EventTaskStarted:
		if !t.IsAgent() {
			m.mu.Unlock()
			return
		}
		var retired *agentPanel
		if s.panel == nil || s.panel.running() == 0 {
			retired, s.panel = s.panel, &agentPanel{}
		}
		if retired != nil {
			for _, x := range retired.agents {
				delete(m.agentRefs, x.n)
			}
		}
		call, known := s.agentCalls[t.ToolUseID]
		delete(s.agentCalls, t.ToolUseID)
		if retired != nil || s.panel.agents == nil {
			for id, c := range s.agentCalls {
				if now.Sub(c.at) > staleCall {
					delete(s.agentCalls, id)
				}
			}
		}
		name := call.name
		if !known || name == "" {
			name = t.Type
		}
		m.agentSeq++
		a := &trackedAgent{n: m.agentSeq, id: t.ID, toolUseID: t.ToolUseID, name: name, desc: t.Description,
			state: "running", started: now, parent: call.parent}
		s.panel.agents = append(s.panel.agents, a)
		m.agentRefs[a.n] = agentRef{thread: s.row.ThreadID, a: a}
		if retired != nil && retired.msg != 0 {
			retired.view = 0
			text, _ := m.listView(retired)
			msgID := retired.msg
			// Runs when onTask returns, after the lock is released.
			defer func() { _ = m.d.API.EditMessage(ctx, msgID, text, nil) }()
		}
	case agent.EventTaskProgress:
		a := m.agentByID(s, t.ID)
		if a == nil {
			m.mu.Unlock()
			return
		}
		a.toolUses, a.lastTool = t.ToolUses, t.LastTool
		if s.panel.view != a.n { // the list does not show counters
			m.mu.Unlock()
			return
		}
	case agent.EventTaskDone:
		a := m.agentByID(s, t.ID)
		if a == nil || a.state != "running" {
			m.mu.Unlock()
			return
		}
		a.state, a.took = t.Status, now.Sub(a.started)
		m.endSeq++
		a.ended, a.endN = now, m.endSeq
		s.lastFinished = a.name
		if t.Summary != "" {
			a.summary = t.Summary
		}
		if t.OutputFile != "" {
			a.outputFile = t.OutputFile
		}
		if t.ToolUses > 0 {
			a.toolUses = t.ToolUses
		}
	}
	s.panel.dirty = true
	force := ev.Kind != agent.EventTaskProgress
	m.mu.Unlock()
	m.flushAgents(ctx, s, force)
}

func cutLabel(s string) string {
	if r := []rune(s); len(r) > 24 {
		return string(r[:23]) + "…"
	}
	return s
}

// flushAgents posts the panel, or edits it when it changed and the edit
// interval has passed (force skips the wait). One post at a time: changes
// made while the first post is in flight are flushed right after it.
func (m *Manager) flushAgents(ctx context.Context, s *sess, force bool) {
	now := m.d.Now()
	m.mu.Lock()
	p := s.panel
	if p == nil || !p.dirty || p.posting || (!force && p.msg != 0 && now.Sub(p.lastEdit) < m.d.EditInterval) {
		m.mu.Unlock()
		return
	}
	p.dirty, p.lastEdit = false, now
	text, kb := m.panelView(p, now)
	msgID := p.msg
	if msgID == 0 {
		p.posting = true
	}
	m.mu.Unlock()
	if msgID != 0 {
		if err := m.d.API.EditMessage(ctx, msgID, text, kb); err != nil {
			m.telegramFailed(s, "edit agents panel", err)
		}
		return
	}
	id, err := m.d.API.SendMessage(ctx, s.row.ThreadID, text, kb, true)
	m.mu.Lock()
	p.posting = false
	if err == nil {
		p.msg = id
	} else {
		p.dirty = true
	}
	again := err == nil && p.dirty
	retired := err == nil && s.panel != p
	var final string
	if retired { // replaced while this post was in flight: no live buttons
		p.view = 0
		final, _ = m.listView(p)
	}
	m.mu.Unlock()
	if err != nil {
		m.telegramFailed(s, "send agents panel", err)
		return
	}
	if retired {
		_ = m.d.API.EditMessage(ctx, id, final, nil)
		return
	}
	if again {
		m.flushAgents(ctx, s, true)
	}
}

// maxTranscriptTail is how much of an agent transcript is read for its answer.
const maxTranscriptTail = 8 << 20

// lastAssistantText returns the last assistant text of an agent transcript
// (JSONL), reading at most maxTranscriptTail bytes from its end.
func lastAssistantText(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	cut := false
	if st, err := f.Stat(); err == nil && st.Size() > maxTranscriptTail {
		if _, err := f.Seek(st.Size()-maxTranscriptTail, io.SeekStart); err != nil {
			return "", err
		}
		cut = true
	}
	return lastAssistantTextFrom(f, cut)
}

// lastAssistantTextFrom scans JSONL; with cut the first line is partial and skipped.
func lastAssistantTextFrom(r io.Reader, cut bool) (string, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), maxTranscriptTail)
	last := ""
	for first := true; sc.Scan(); first = false {
		if first && cut {
			continue
		}
		var line struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "assistant" {
			continue
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(line.Message.Content, &blocks) != nil {
			continue
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		if len(parts) > 0 {
			last = strings.Join(parts, "\n\n")
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if last == "" {
		return "", errors.New("в журнале агента нет ответа")
	}
	return last, nil
}

// HandleAgentButton handles the panel buttons (ag:<n>:c card, b back,
// s stop, o output) and returns the callback alert.
func (m *Manager) HandleAgentButton(ctx context.Context, u telegram.Update) string {
	parts := strings.Split(u.CallbackData, ":")
	if len(parts) != 3 {
		return "Кнопка устарела"
	}
	n, err := strconv.Atoi(parts[1])
	m.mu.Lock()
	ref, ok := m.agentRefs[n]
	var s *sess
	var a agent.Session
	var st trackedAgent
	if ok {
		if s = m.sessions[ref.thread]; s != nil {
			a, st = s.agent, *ref.a
		}
	}
	m.mu.Unlock()
	if err != nil || !ok || s == nil || ref.thread != u.ThreadID {
		return "Кнопка устарела"
	}
	switch parts[2] {
	case "c", "b":
		m.mu.Lock()
		if s.panel != nil {
			s.panel.view, s.panel.dirty = 0, true
			if parts[2] == "c" {
				s.panel.view = n
			}
		}
		m.mu.Unlock()
		m.flushAgents(ctx, s, true)
		return ""
	case "s":
		if st.state != "running" {
			return "Агент уже завершён"
		}
		if a == nil {
			return "Процесс агента уже остановлен"
		}
		if err := a.StopTask(ctx, st.id); err != nil {
			return alertText(err)
		}
		return "Останавливаю"
	case "o":
		// The transcript first: a background agent's tool result is only
		// the launch notice.
		var text string
		if st.outputFile != "" {
			text, _ = lastAssistantText(st.outputFile)
		}
		if text == "" {
			text = st.result
		}
		if strings.TrimSpace(text) == "" {
			return "Результат недоступен"
		}
		name := fmt.Sprintf("agent-%s-%d.md", fileSafe.ReplaceAllString(st.name, "_"), st.n)
		caption := "📄 " + render.Escape(st.name) + " · " + render.Escape(cutLabel(st.desc))
		if _, err := m.d.API.SendDocument(ctx, u.ThreadID, name, []byte(text), caption, true); err != nil {
			return alertText(err)
		}
		return ""
	}
	return "Кнопка устарела"
}

var fileSafe = regexp.MustCompile(`[^A-Za-z0-9_-]+`)

// Agents reposts the agents panel at the bottom of the topic.
func (m *Manager) Agents(ctx context.Context, thread int) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	m.mu.Lock()
	p := s.panel
	old := 0
	if p != nil {
		if p.posting {
			m.mu.Unlock()
			return nil // the panel is being posted right now
		}
		old, p.msg, p.dirty, p.view = p.msg, 0, true, 0
	}
	m.mu.Unlock()
	if p == nil {
		m.say(ctx, s, "🤖 Агентов в этой сессии ещё не было.", true)
		return nil
	}
	if old != 0 {
		_ = m.d.API.DeleteMessage(ctx, old)
	}
	m.flushAgents(ctx, s, true)
	return nil
}

// beginContinuation starts a turn that no user message started: a finished
// background agent woke the agent. It shows a status message like any turn.
// It takes no MAX_PARALLEL slot: the CLI is already running the turn.
// Like any turn it gets a number and a start snapshot for its summary.
func (m *Manager) beginContinuation(ctx context.Context, s *sess) {
	// A rollback in progress ends first, so the start snapshot sees the
	// restored files. The CLI runs the turn anyway; only its events wait.
	for {
		m.mu.Lock()
		if s.inTurn || s.closed {
			m.mu.Unlock()
			return
		}
		done := s.restored
		if !s.restoring || done == nil {
			break // m.mu stays held
		}
		m.mu.Unlock()
		<-done
	}
	name := s.lastFinished
	if name == "" {
		name = "в фоне"
	}
	// The turn is claimed before the snapshot: the turn buttons refuse a
	// rollback from now on.
	now := m.d.Now()
	s.inTurn = true
	s.turnNo++
	s.turnBase = ""
	s.status = render.Status{State: store.StateRunning, Started: now, LastEvent: now,
		Note: "🤖 продолжение после агента " + name}
	s.lastEdit, s.dirty, s.waits = now, false, 0
	s.warned, s.overtime, s.stallFrom, s.retryAt = false, false, time.Time{}, time.Time{}
	s.lastText, s.pending, s.interrupted = "", "", false
	status := s.status
	m.mu.Unlock()
	base := m.snapshot(ctx, s, "turn snapshot")
	m.mu.Lock()
	s.turnBase = base
	m.mu.Unlock()
	msgID, _ := m.d.API.SendMessage(ctx, s.row.ThreadID, render.StatusText(status, now), stopKeyboard(s.row.ThreadID), true)
	m.mu.Lock()
	s.statusMsg = msgID
	m.mu.Unlock()
	m.setState(ctx, s, store.StateRunning)
}

// stopAgents stops the running agents of a closing session and removes the
// panel buttons, which could only act on a dead process now.
func (m *Manager) stopAgents(ctx context.Context, s *sess, a agent.Session) {
	m.mu.Lock()
	var ids []string
	msgID, text := 0, ""
	if p := s.panel; p != nil {
		for _, x := range p.agents {
			if x.state == "running" {
				ids = append(ids, x.id)
			}
		}
		p.view = 0
		msgID = p.msg
		text, _ = m.listView(p)
	}
	for n, ref := range m.agentRefs {
		if ref.thread == s.row.ThreadID {
			delete(m.agentRefs, n)
		}
	}
	m.mu.Unlock()
	if a != nil {
		for _, id := range ids {
			_ = a.StopTask(ctx, id)
		}
	}
	if msgID != 0 {
		_ = m.d.API.EditMessage(ctx, msgID, text, nil)
	}
}

// agentsGone marks the running agents stopped when their claude process
// exits: nothing will report their end any more.
func (m *Manager) agentsGone(ctx context.Context, s *sess) {
	now := m.d.Now()
	m.mu.Lock()
	changed := false
	// A closed session's panel was finalized by stopAgents.
	if s.panel != nil && !s.closed {
		for _, a := range s.panel.agents {
			if a.state == "running" {
				m.endSeq++
				a.state, a.took, a.ended, a.endN, changed = "stopped", now.Sub(a.started), now, m.endSeq, true
				s.lastFinished = a.name
			}
		}
		s.panel.dirty = s.panel.dirty || changed
	}
	m.mu.Unlock()
	if changed {
		m.flushAgents(ctx, s, true)
	}
}

// refreshPanels re-renders open cards of running agents, whose elapsed time
// moves; the list shows nothing that changes with time. Called by the ticker.
func (m *Manager) refreshPanels(ctx context.Context) {
	m.mu.Lock()
	var due []*sess
	for _, s := range m.sessions {
		p := s.panel
		if p == nil || p.msg == 0 || p.view == 0 { // the first post belongs to onTask
			continue
		}
		for _, a := range p.agents {
			if a.n == p.view && a.state == "running" {
				p.dirty = true
				due = append(due, s)
			}
		}
	}
	m.mu.Unlock()
	for _, s := range due {
		m.flushAgents(ctx, s, false)
	}
}
