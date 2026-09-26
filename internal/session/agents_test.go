package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

func taskStarted(id, toolUse, typ, desc string) agent.Event {
	return agent.Event{Kind: agent.EventTaskStarted, Task: &agent.TaskInfo{ID: id, ToolUseID: toolUse, Type: typ, Description: desc}}
}

func (e *env) panel(t *testing.T, thread int, substr string) telegram.FakeMessage {
	t.Helper()
	var found telegram.FakeMessage
	testutil.Eventually(t, "panel with "+substr, func() bool {
		for _, m := range e.api.Messages(thread) {
			if strings.HasPrefix(m.HTML, "🤖 <b>Агенты</b>") && strings.Contains(m.HTML, substr) {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

// card waits for the agent card (the panel message in card view) containing substr.
func (e *env) card(t *testing.T, thread int, substr string) telegram.FakeMessage {
	t.Helper()
	var found telegram.FakeMessage
	testutil.Eventually(t, "card with "+substr, func() bool {
		for _, m := range e.api.Messages(thread) {
			if strings.Contains(m.HTML, "Вызвал: ") && strings.Contains(m.HTML, substr) {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

// press presses the first button of msg whose text starts with prefix.
func (e *env) press(t *testing.T, thread int, msg telegram.FakeMessage, prefix string) string {
	t.Helper()
	for _, row := range msg.Keyboard {
		for _, b := range row {
			if strings.HasPrefix(b.Text, prefix) {
				return e.m.HandleAgentButton(context.Background(), telegram.Update{ThreadID: thread, CallbackData: b.Data})
			}
		}
	}
	t.Fatalf("no button %q in %+v", prefix, msg.Keyboard)
	return ""
}

func startAgent(s *agent.FakeSession, parent, toolUse, task, name, desc string) {
	s.Emit(agent.Event{Kind: agent.EventToolUse, ParentToolUseID: parent, ToolUseID: toolUse, ToolName: "Agent",
		ToolInput: map[string]any{"subagent_type": name, "description": desc}})
	s.Emit(taskStarted(task, toolUse, "local_agent", desc))
}

func TestAgentsTreeAndCard(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "Explore", "ищет sudo")
	startAgent(s, "tu1", "tu2", "t2", "go-reviewer", "ревью")
	p := e.panel(t, thread, "▶ <b>Explore</b> — ищет sudo\n   ↳ ▶ <b>go-reviewer</b> — ревью")
	if strings.Contains(p.HTML, "инстр.") {
		t.Fatalf("list must not show counters: %q", p.HTML)
	}
	if len(p.Keyboard) != 1 || len(p.Keyboard[0]) != 2 || p.Keyboard[0][1].Text != "▶ go-reviewer" {
		t.Fatalf("list keyboard: %+v", p.Keyboard)
	}
	if alert := e.press(t, thread, p, "▶ go-reviewer"); alert != "" {
		t.Fatalf("open card: %q", alert)
	}
	e.card(t, thread, "Вызвал: Explore")
	s.Emit(agent.Event{Kind: agent.EventToolUse, ParentToolUseID: "tu2", ToolUseID: "tu3", ToolName: "Read",
		ToolInput: map[string]any{"file_path": "/w/demo/a.go"}})
	c := e.card(t, thread, "Сейчас: 📖 Read a.go")
	if c.ID != p.ID {
		t.Fatal("the card replaces the panel in the same message")
	}
	if alert := e.press(t, thread, c, "⬅ Назад"); alert != "" {
		t.Fatalf("back: %q", alert)
	}
	e.panel(t, thread, "↳ ▶ <b>go-reviewer</b>")
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed", Summary: "found it"}})
	p = e.panel(t, thread, "✅ <b>Explore</b>")
	e.press(t, thread, p, "✅ Explore")
	e.card(t, thread, "<i>found it</i>")
}

func TestShellTaskHasNoPanel(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(taskStarted("t9", "", "local_bash", "npm run dev"))
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "Агенты") {
			t.Fatalf("shell task opened a panel: %q", m.HTML)
		}
	}
}

func TestLastAssistantText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.jsonl")
	lines := []string{
		`{"type":"user","message":{"content":"task"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"first answer"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"final "},{"type":"text","text":"report"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"x"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := lastAssistantText(path); err != nil || got != "final \n\nreport" {
		t.Fatalf("got %q, %v", got, err)
	}
	cut := `t":"cut"}]}}` + "\n" + lines[1] + "\n"
	if got, _ := lastAssistantTextFrom(strings.NewReader(cut), true); got != "first answer" {
		t.Fatalf("cut tail: %q", got)
	}
	if _, err := lastAssistantText(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file must fail")
	}
}

func TestCardStopAndOutput(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "Explore", "one")
	startAgent(s, "", "tu2", "t2", "Plan", "two")
	p := e.panel(t, thread, "<b>Plan</b>")
	e.press(t, thread, p, "▶ Explore")
	c := e.card(t, thread, "<b>Explore</b>")
	if alert := e.press(t, thread, c, "⏹ Остановить"); alert != "Останавливаю" {
		t.Fatalf("stop alert: %q", alert)
	}
	if got := s.Stopped(); len(got) != 1 || got[0] != "t1" || s.Interrupts() != 0 {
		t.Fatalf("stopped %v, interrupts %d", got, s.Interrupts())
	}
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "stopped"}})
	s.Emit(agent.Event{Kind: agent.EventToolResult, ToolUseID: "tu1", Text: "explore report"})
	path := filepath.Join(t.TempDir(), "t2.output")
	_ = os.WriteFile(path, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"plan report"}]}}`+"\n"), 0o600)
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t2", Status: "completed", OutputFile: path}})
	testutil.Eventually(t, "tool result stored", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		a := e.m.findAgent(e.m.sessions[thread], "tu1")
		return a != nil && a.result != "" && e.m.runningAgents(e.m.sessions[thread]) == 0
	})
	c = e.card(t, thread, "Остановлен за")
	if alert := e.press(t, thread, c, "📄 Результат"); alert != "" {
		t.Fatalf("output alert: %q", alert)
	}
	e.press(t, thread, c, "⬅ Назад")
	p = e.panel(t, thread, "✅ <b>Plan</b>")
	e.press(t, thread, p, "✅ Plan")
	c = e.card(t, thread, "Готово за")
	if alert := e.press(t, thread, c, "📄 Результат"); alert != "" {
		t.Fatalf("output alert: %q", alert)
	}
	var texts []string
	for _, d := range e.api.Documents(thread) {
		texts = append(texts, d.Name+"="+string(d.Data))
	}
	joined := strings.Join(texts, "|")
	if len(texts) != 2 || !strings.Contains(joined, "agent-Explore-") || !strings.Contains(joined, "explore report") ||
		!strings.Contains(joined, "plan report") {
		t.Fatalf("documents: %v", texts)
	}
	if alert := e.m.HandleAgentButton(ctx, telegram.Update{ThreadID: thread + 1, CallbackData: p.Keyboard[0][0].Data}); alert != "Кнопка устарела" {
		t.Fatalf("foreign topic: %q", alert)
	}
}

func TestAgentsCommandReposts(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	if err := e.m.Agents(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "Агентов в этой сессии ещё не было")
	s := e.session(t, 0)
	s.Emit(taskStarted("t1", "", "local_agent", "one"))
	old := e.panel(t, thread, "one")
	if err := e.m.Agents(ctx, thread); err != nil {
		t.Fatal(err)
	}
	msgs := e.api.Messages(thread)
	last := msgs[len(msgs)-1]
	if last.ID == old.ID || !strings.Contains(last.HTML, "one") {
		t.Fatalf("panel not reposted at the bottom: %+v", last)
	}
	for _, m := range msgs {
		if m.ID == old.ID {
			t.Fatal("old panel must be deleted")
		}
	}
}

// fakeClock is a settable clock safe to read from the manager's goroutines.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Add(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }

func TestIdleProcessKeptWhileAgentRuns(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	clock := &fakeClock{now: time.Now()}
	e.m.d.Now = clock.Now
	e.m.d.IdleTimeout = time.Minute
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(taskStarted("t1", "", "local_agent", "background"))
	e.panel(t, thread, "background")
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	clock.Add(2 * time.Minute)
	e.m.tick(ctx)
	if s.Closed() {
		t.Fatal("process with a running agent must not be stopped for idling")
	}
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed"}})
	e.panel(t, thread, "✅")
	testutil.Eventually(t, "idle stop after the agent", func() bool { e.m.tick(ctx); return s.Closed() })
}

func TestContinuationTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolUseID: "tu1", ToolName: "Agent", ToolInput: map[string]any{"subagent_type": "Explore"}})
	s.Emit(taskStarted("t1", "tu1", "local_agent", "bg"))
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed"}})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "Explore finished: all good"})
	e.hasMessage(t, thread, "продолжение после агента Explore")
	s.Emit(result())
	e.hasMessage(t, thread, "Explore finished: all good\n\n<i>✅ Ход завершён")
}

func TestSubagentEventsDoNotStartTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(taskStarted("t1", "tu1", "local_agent", "bg"))
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	s.Emit(agent.Event{Kind: agent.EventToolUse, ParentToolUseID: "tu1", ToolName: "Read", ToolInput: map[string]any{"file_path": "/w/demo/a"}})
	s.Emit(agent.Event{Kind: agent.EventText, ParentToolUseID: "tu1", Text: "sub text"})
	s.Emit(agent.Event{Kind: agent.EventTaskProgress, Task: &agent.TaskInfo{ID: "t1", ToolUses: 1}})
	e.press(t, thread, e.panel(t, thread, "bg"), "▶")
	e.card(t, thread, "1 инстр.")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "продолжение") {
			t.Fatal("subagent events must not start a continuation turn")
		}
	}
}

func TestStopMentionsRunningAgents(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(taskStarted("t1", "", "local_agent", "bg"))
	e.panel(t, thread, "bg")
	if err := e.m.Stop(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "Агенты ещё работают: 1")
}

func TestAgentButtonsAfterClose(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(taskStarted("t1", "", "local_agent", "bg"))
	p := e.panel(t, thread, "bg")
	if err := e.m.Close(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if got := s.Stopped(); len(got) != 1 || got[0] != "t1" {
		t.Fatalf("close must stop running agents, stopped %v", got)
	}
	for _, m := range e.api.Messages(thread) {
		if m.ID == p.ID && len(m.Keyboard) != 0 {
			t.Fatal("panel buttons must be removed on close")
		}
	}
	if alert := e.m.HandleAgentButton(ctx, telegram.Update{ThreadID: thread, CallbackData: p.Keyboard[0][0].Data}); alert != "Кнопка устарела" {
		t.Fatalf("button after close: %q", alert)
	}
}

func TestBackgroundAgentOutputPrefersTranscript(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolUseID: "tu1", ToolName: "Agent", ToolInput: map[string]any{"subagent_type": "Review"}})
	s.Emit(taskStarted("t1", "tu1", "local_agent", "bg"))
	s.Emit(agent.Event{Kind: agent.EventToolResult, ToolUseID: "tu1", Text: "Async agent launched successfully."})
	path := filepath.Join(t.TempDir(), "t1.output")
	_ = os.WriteFile(path, []byte(`{"type":"assistant","message":{"content":[{"type":"text","text":"review report"}]}}`+"\n"), 0o600)
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed", OutputFile: path}})
	p := e.panel(t, thread, "✅ <b>Review</b>")
	e.press(t, thread, p, "✅ Review")
	c := e.card(t, thread, "<b>Review</b>")
	if alert := e.press(t, thread, c, "📄 Результат"); alert != "" {
		t.Fatalf("alert: %q", alert)
	}
	if docs := e.api.Documents(thread); len(docs) != 1 || string(docs[0].Data) != "review report" {
		t.Fatalf("documents: %+v", docs)
	}
}

func TestProcessExitStopsAgents(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(taskStarted("t1", "", "local_agent", "bg"))
	e.panel(t, thread, "▶")
	_ = s.Close()
	e.panel(t, thread, "⏹ <b>local_agent</b>")
	e.m.mu.Lock()
	n := e.m.runningAgents(e.m.sessions[thread])
	e.m.mu.Unlock()
	if n != 0 {
		t.Fatalf("running agents after process exit: %d", n)
	}
}

func TestPanelCollapsesToTwelve(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	desc := strings.Repeat("длинное описание ", 10)
	for i := 0; i < 20; i++ {
		startAgent(s, "", fmt.Sprintf("tu%d", i), fmt.Sprintf("t%d", i), fmt.Sprintf("agent%d", i), desc)
	}
	p := e.panel(t, thread, "… ещё 8 работают")
	buttons := 0
	for _, row := range p.Keyboard {
		buttons += len(row)
	}
	if buttons != 12 || len(p.HTML) > 4096 {
		t.Fatalf("buttons %d, text %d bytes", buttons, len(p.HTML))
	}
	for i := 0; i < 20; i++ {
		s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: fmt.Sprintf("t%d", i), Status: "completed"}})
	}
	p = e.panel(t, thread, "… ещё 8 готовых")
	if !strings.Contains(p.HTML, "<b>agent19</b>") || strings.Contains(p.HTML, "<b>agent0</b>") {
		t.Fatalf("the most recently finished stay listed:\n%s", p.HTML)
	}
}

func TestAgentsListOrphanBecomesRoot(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu0", "t0", "Caller", "starts many")
	for i := 1; i <= 12; i++ {
		startAgent(s, "", fmt.Sprintf("tu%d", i), fmt.Sprintf("t%d", i), fmt.Sprintf("a%d", i), "x")
	}
	startAgent(s, "tu0", "tuK", "tK", "Kid", "nested")
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t0", Status: "completed"}})
	for i := 1; i <= 11; i++ {
		s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: fmt.Sprintf("t%d", i), Status: "completed"}})
	}
	// Running: a12, Kid. Finished: Caller (oldest), a1..a11; Caller and a1 collapse.
	p := e.panel(t, thread, "… ещё 2 готовых")
	if !strings.Contains(p.HTML, "\n▶ <b>Kid</b> — nested") {
		t.Fatalf("an agent whose caller is collapsed is a root:\n%s", p.HTML)
	}
}

func TestNewPanelRetiresOld(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "First", "one")
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed"}})
	old := e.panel(t, thread, "✅ <b>First</b>")
	startAgent(s, "", "tu2", "t2", "Second", "two")
	e.panel(t, thread, "▶ <b>Second</b>")
	testutil.Eventually(t, "old panel retired", func() bool {
		for _, m := range e.api.Messages(thread) {
			if m.ID == old.ID {
				return len(m.Keyboard) == 0
			}
		}
		return false
	})
	if alert := e.m.HandleAgentButton(ctx, telegram.Update{ThreadID: thread, CallbackData: old.Keyboard[0][0].Data}); alert != "Кнопка устарела" {
		t.Fatalf("retired button: %q", alert)
	}
	e.m.mu.Lock()
	refs, names := len(e.m.agentRefs), len(e.m.sessions[thread].agentCalls)
	e.m.mu.Unlock()
	if refs != 1 || names != 0 { // a call is dropped once its task starts
		t.Fatalf("retired agents must be forgotten: refs %d, names %d", refs, names)
	}
}

func TestAgentsNoDoublePost(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	api := &blockingAPI{Fake: e.api, hit: make(chan struct{}), release: make(chan struct{}), armed: true}
	e.m.d.API = api
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "Explore", "one") // its first post blocks
	<-api.hit
	if err := e.m.Agents(ctx, thread); err != nil { // while the post is in flight
		t.Fatal(err)
	}
	startAgent(s, "", "tu2", "t2", "Plan", "two") // a change during the post
	close(api.release)
	e.panel(t, thread, "<b>Plan</b>")
	n := 0
	for _, m := range e.api.Messages(thread) {
		if strings.HasPrefix(m.HTML, "🤖 <b>Агенты</b>") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want one panel, got %d", n)
	}
}

func TestContinuationLabelLastFinished(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "Alpha", "a")
	startAgent(s, "", "tu2", "t2", "Beta", "b")
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t2", Status: "completed"}})
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed"}})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "both done"})
	e.hasMessage(t, thread, "продолжение после агента Alpha")
}

func TestIdleEmptyResultIgnored(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	before := len(e.api.Messages(thread))
	s.Emit(result()) // stray result while idle
	// A task event is processed after it and starts no turn: a barrier.
	s.Emit(taskStarted("t1", "", "local_agent", "barrier"))
	e.panel(t, thread, "barrier")
	msgs := e.api.Messages(thread)
	if len(msgs) != before+1 { // only the panel
		for _, m := range msgs {
			t.Log(m.HTML)
		}
		t.Fatalf("a stray idle result must post nothing: %d → %d", before, len(msgs))
	}
}

func TestIdleErrorResultIsShown(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	s.Emit(agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "error_during_execution", IsError: true, Text: "API Error: overloaded"}})
	e.hasMessage(t, thread, "API Error: overloaded")
}

// blockingAPI holds the first agents-panel post after arm until release.
type blockingAPI struct {
	*telegram.Fake
	mu      sync.Mutex
	armed   bool
	hit     chan struct{}
	release chan struct{}
}

func (b *blockingAPI) SendMessage(ctx context.Context, thread int, html string, kb telegram.Keyboard, silent bool) (int, error) {
	b.mu.Lock()
	hold := b.armed && strings.HasPrefix(html, "🤖 <b>Агенты</b>")
	if hold {
		b.armed = false
	}
	b.mu.Unlock()
	if hold {
		close(b.hit)
		<-b.release
	}
	return b.Fake.SendMessage(ctx, thread, html, kb, silent)
}

func TestPanelRetiredDuringPostLosesButtons(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	api := &blockingAPI{Fake: e.api, hit: make(chan struct{}), release: make(chan struct{})}
	e.m.d.API = api
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "First", "one")
	e.panel(t, thread, "First")
	api.mu.Lock()
	api.armed = true
	api.mu.Unlock()
	done := make(chan struct{})
	go func() { _ = e.m.Agents(ctx, thread); close(done) }() // repost blocks in SendMessage
	<-api.hit
	s.Emit(agent.Event{Kind: agent.EventTaskDone, Task: &agent.TaskInfo{ID: "t1", Status: "completed"}})
	startAgent(s, "", "tu2", "t2", "Second", "two")
	e.panel(t, thread, "▶ <b>Second</b>")
	close(api.release)
	<-done
	testutil.Eventually(t, "reposted retired panel has no buttons", func() bool {
		for _, m := range e.api.Messages(thread) {
			if strings.Contains(m.HTML, "<b>First</b>") && !strings.Contains(m.HTML, "Second") {
				return len(m.Keyboard) == 0
			}
		}
		return false
	})
}

func TestProcessExitSetsEnded(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "Gone", "bg")
	e.panel(t, thread, "Gone")
	_ = s.Close()
	e.panel(t, thread, "⏹ <b>Gone</b>")
	e.m.mu.Lock()
	ss := e.m.sessions[thread]
	a := e.m.findAgent(ss, "tu1")
	ended, last := a.ended, ss.lastFinished
	e.m.mu.Unlock()
	if ended.IsZero() || last != "Gone" {
		t.Fatalf("ended %v, lastFinished %q", ended, last)
	}
}

// countingAPI counts edits per message.
type countingAPI struct {
	*telegram.Fake
	mu    sync.Mutex
	edits map[int]int
}

func (c *countingAPI) EditMessage(ctx context.Context, id int, html string, kb telegram.Keyboard) error {
	c.mu.Lock()
	c.edits[id]++
	c.mu.Unlock()
	return c.Fake.EditMessage(ctx, id, html, kb)
}

func (c *countingAPI) count(id int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.edits[id]
}

func TestListViewNotReedited(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	api := &countingAPI{Fake: e.api, edits: map[int]int{}}
	e.m.d.API = api
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	startAgent(s, "", "tu1", "t1", "Explore", "one")
	startAgent(s, "", "tu2", "t2", "Plan", "two")
	p := e.panel(t, thread, "<b>Plan</b>")
	before := api.count(p.ID)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ParentToolUseID: "tu1", ToolUseID: "x1", ToolName: "Read", ToolInput: map[string]any{"file_path": "/w/demo/a"}})
	s.Emit(agent.Event{Kind: agent.EventTaskProgress, Task: &agent.TaskInfo{ID: "t1", ToolUses: 3}})
	s.Emit(taskStarted("t9", "", "local_bash", "sync")) // barrier: ignored task event
	testutil.Eventually(t, "events handled", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		a := e.m.findAgent(e.m.sessions[thread], "tu1")
		return a != nil && a.toolUses == 3
	})
	e.m.refreshPanels(ctx)
	if got := api.count(p.ID); got != before {
		t.Fatalf("the list view does not change: %d extra edits", got-before)
	}
	e.press(t, thread, p, "▶ Explore")
	opened := api.count(p.ID)
	e.m.refreshPanels(ctx)
	if api.count(p.ID) != opened+1 {
		t.Fatal("an open card of a running agent is refreshed")
	}
	s.Emit(agent.Event{Kind: agent.EventToolUse, ParentToolUseID: "tu2", ToolUseID: "x2", ToolName: "Read", ToolInput: map[string]any{"file_path": "/w/demo/b"}})
	s.Emit(agent.Event{Kind: agent.EventToolUse, ParentToolUseID: "tu1", ToolUseID: "x3", ToolName: "Read", ToolInput: map[string]any{"file_path": "/w/demo/c"}})
	e.card(t, thread, "Сейчас: 📖 Read c")
	if got := api.count(p.ID); got != opened+2 {
		t.Fatalf("only the carded agent's action edits the card: %d edits", got-opened)
	}
}

func TestStaleAgentCallsPruned(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	clock := &fakeClock{now: time.Now()}
	e.m.d.Now = clock.Now
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolUseID: "old", ToolName: "Agent", ToolInput: map[string]any{"subagent_type": "Lost"}})
	s.Emit(taskStarted("t9", "", "local_bash", "sync"))
	testutil.Eventually(t, "call recorded", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		_, ok := e.m.sessions[thread].agentCalls["old"]
		return ok
	})
	clock.Add(11 * time.Minute)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolUseID: "pend", ToolName: "Agent", ToolInput: map[string]any{"subagent_type": "Soon"}})
	startAgent(s, "", "tu1", "t1", "Fresh", "x")
	e.panel(t, thread, "Fresh")
	e.m.mu.Lock()
	_, stale := e.m.sessions[thread].agentCalls["old"]
	_, fresh := e.m.sessions[thread].agentCalls["pend"]
	e.m.mu.Unlock()
	if stale || !fresh {
		t.Fatalf("stale kept %v, fresh kept %v", stale, fresh)
	}
}

func TestContinuationIsANewTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	e.sessionState(t, thread, "running") // the start snapshot is taken
	write(t, dir, "a.go", "b\n")
	agentTurn(s, dir, "a.go")
	e.hasMessage(t, thread, "Изменено за ход: 1")
	s.Emit(agent.Event{Kind: agent.EventText, Text: "bg done"})
	e.hasMessage(t, thread, "продолжение после агента")
	write(t, dir, "b.go", "new\n")
	agentTurn(s, dir, "b.go")
	var refs []turnRef
	testutil.Eventually(t, "two summaries", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		refs = refs[:0]
		for _, r := range e.m.turnRefs {
			refs = append(refs, r)
		}
		return len(refs) == 2
	})
	if refs[0].turn == refs[1].turn {
		t.Fatalf("the continuation shares turn number %d: both summaries count as latest", refs[0].turn)
	}
	if refs[0].base == refs[1].base {
		t.Fatal("the continuation must diff against its own start, not the previous turn's")
	}
}

func TestContinuationWaitsForRollback(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	ss := e.m.lookup(thread)
	e.m.mu.Lock()
	ss.beginRestore()
	e.m.mu.Unlock()
	s.Emit(agent.Event{Kind: agent.EventText, Text: "bg done"})
	time.Sleep(100 * time.Millisecond)
	e.m.mu.Lock()
	inTurn, no := ss.inTurn, ss.turnNo
	e.m.mu.Unlock()
	if inTurn || no != 1 {
		t.Fatalf("a continuation started during a rollback: inTurn=%v turn=%d", inTurn, no)
	}
	e.m.mu.Lock()
	ss.endRestore()
	e.m.mu.Unlock()
	e.hasMessage(t, thread, "продолжение после агента")
	s.Emit(result())
	e.hasMessage(t, thread, "bg done")
	e.m.mu.Lock()
	no = ss.turnNo
	e.m.mu.Unlock()
	if no != 2 {
		t.Fatalf("turn after the rollback: %d", no)
	}
}
