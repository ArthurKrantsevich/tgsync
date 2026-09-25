package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/permissions"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

type env struct {
	m   *Manager
	api *telegram.Fake
	ag  *agent.Fake
	st  *store.Store
	br  *permissions.Broker
}

func newEnv(t *testing.T, maxParallel int) *env {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := telegram.NewFake()
	br := permissions.NewBroker(api, st)
	ag := &agent.Fake{}
	tp := topics.New(api, st, "node1")
	_ = tp.LoadIcons(context.Background())
	m := NewManager(Deps{API: api, Store: st, Topics: tp, Broker: br, Runner: ag,
		MaxParallel: maxParallel, SettingSources: []string{"user"}})
	t.Cleanup(m.Shutdown)
	return &env{m: m, api: api, ag: ag, st: st, br: br}
}

func (e *env) session(t *testing.T, i int) *agent.FakeSession {
	t.Helper()
	var s *agent.FakeSession
	testutil.Eventually(t, fmt.Sprintf("agent session %d", i), func() bool {
		if ss := e.ag.Sessions(); len(ss) > i {
			s = ss[i]
			return true
		}
		return false
	})
	return s
}

// sessionState waits until the session reaches state.
func (e *env) sessionState(t *testing.T, thread int, state string) {
	t.Helper()
	testutil.Eventually(t, "session state "+state, func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		s := e.m.sessions[thread]
		return s != nil && s.row.State == state
	})
}

func (e *env) hasMessage(t *testing.T, thread int, substr string) {
	t.Helper()
	testutil.Eventually(t, "message "+substr, func() bool {
		for _, m := range e.api.Messages(thread) {
			if strings.Contains(m.HTML, substr) {
				return true
			}
		}
		return false
	})
}

func result() agent.Event {
	return agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "success", CostUSD: 0.01}}
}

func TestNewWithTaskRunsTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, err := e.m.New(ctx, "demo", "/w/demo", "fix the bug")
	if err != nil {
		t.Fatal(err)
	}
	s := e.session(t, 0)
	if s.Opts.Cwd != "/w/demo" || s.Opts.ResumeID != "" || s.Opts.PermissionMode != "default" || len(s.Opts.SettingSources) != 1 {
		t.Fatalf("opts: %+v", s.Opts)
	}
	if got := s.Sent(); len(got) != 1 || got[0] != "fix the bug" {
		t.Fatalf("sent: %v", got)
	}
	e.sessionState(t, thread, store.StateRunning)
	if name := e.api.Topic(thread).Name; name != "demo · fix the bug" {
		t.Fatalf("state change must not rename the topic: %q", name)
	}

	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "sid-1"}})
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Edit", ToolInput: map[string]any{"file_path": "/w/demo/a.go"}})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "Done **fixing**"})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "subagent chatter", ParentToolUseID: "t1"})
	s.Emit(result())

	e.hasMessage(t, thread, "Done <b>fixing</b>\n\n<i>✅ Ход завершён")
	e.sessionState(t, thread, store.StateIdle)
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "subagent chatter") {
			t.Fatal("subagent text must not be posted")
		}
		if strings.Contains(m.HTML, "📝 Edit a.go") {
			t.Fatal("status message must be removed at the end of the turn")
		}
	}
	rows, _ := e.st.OpenSessions(ctx)
	if len(rows) != 1 || rows[0].ClaudeSessionID != "sid-1" || rows[0].State != store.StateIdle {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestRemarksGoToStatusOnlyAnswerIsPosted(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "x", Plugins: []string{"p"}}})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "Looking at **the** code"})
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Read", ToolInput: map[string]any{"file_path": "/w/demo/a.go"}})
	e.hasMessage(t, thread, "💬 <i>Looking at the code</i>")
	s.Emit(agent.Event{Kind: agent.EventText, Text: "All good"})
	s.Emit(result())
	e.hasMessage(t, thread, "All good")
	e.sessionState(t, thread, store.StateIdle)
	msgs := e.api.Messages(thread)
	if len(msgs) != 1 { // the answer with the result line
		for _, m := range msgs {
			t.Log(m.HTML)
		}
		t.Fatalf("want 1 message, got %d", len(msgs))
	}
	for _, m := range msgs {
		if strings.Contains(m.HTML, "Looking at") || strings.Contains(m.HTML, "🔌") {
			t.Fatalf("remark or healthy plugin summary posted: %q", m.HTML)
		}
	}
}

func TestReactionWhileProcessing(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	if err := e.m.MessageFrom(ctx, thread, "first", 501); err != nil {
		t.Fatal(err)
	}
	s := e.session(t, 0)
	_ = e.m.MessageFrom(ctx, thread, "second", 502)
	if e.api.Reaction(501) != "👀" || e.api.Reaction(502) != "👀" {
		t.Fatalf("reactions: %q %q", e.api.Reaction(501), e.api.Reaction(502))
	}
	s.Emit(result())
	testutil.Eventually(t, "first reaction removed", func() bool { return e.api.Reaction(501) == "" })
	if e.api.Reaction(502) != "👀" {
		t.Fatal("queued message keeps its reaction while its turn runs")
	}
	testutil.Eventually(t, "second turn", func() bool { return len(s.Sent()) == 2 })
	s.Emit(result())
	testutil.Eventually(t, "second reaction removed", func() bool { return e.api.Reaction(502) == "" })
}

func TestSendNowJumpsQueueAndInterrupts(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "long task")
	s := e.session(t, 0)
	_ = e.m.MessageFrom(ctx, thread, "later", 601)
	_ = e.m.MessageFrom(ctx, thread, "urgent", 602)
	var notices []telegram.FakeMessage
	testutil.Eventually(t, "two queue notices", func() bool {
		notices = nil
		for _, m := range e.api.Messages(thread) {
			if strings.Contains(m.HTML, "В очереди") {
				notices = append(notices, m)
			}
		}
		return len(notices) == 2
	})
	btn := notices[1].Keyboard[0][0]
	if !strings.Contains(btn.Text, "Отправить сейчас") {
		t.Fatalf("button: %+v", btn)
	}
	alert, handled := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: notices[1].ID, CallbackData: btn.Data})
	if !handled || alert != "" || s.Interrupts() != 1 {
		t.Fatalf("handled=%v alert=%q interrupts=%d", handled, alert, s.Interrupts())
	}
	s.Emit(agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "error_during_execution", IsError: true}})
	e.hasMessage(t, thread, "⏹ Ход прерван")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "с ошибкой") {
			t.Fatal("an interrupt we asked for is not an error")
		}
	}
	testutil.Eventually(t, "urgent sent first", func() bool {
		sent := s.Sent()
		return len(sent) == 2 && sent[1] == "urgent"
	})
	testutil.Eventually(t, "notice removed", func() bool {
		for _, m := range e.api.Messages(thread) {
			if m.ID == notices[1].ID {
				return false
			}
		}
		return true
	})
	if alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: btn.Data}); alert != "Уже отправлено" {
		t.Fatalf("stale button: %q", alert)
	}
}

func TestNewWithoutTaskWaitsForFirstMessage(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	e.hasMessage(t, thread, "Напиши задачу")
	if n := len(e.ag.Sessions()); n != 0 {
		t.Fatalf("no process before the first message, got %d", n)
	}
	if err := e.m.Message(ctx, thread, "add tests\nwith details"); err != nil {
		t.Fatal(err)
	}
	e.session(t, 0)
	testutil.Eventually(t, "title from first message", func() bool {
		return strings.HasSuffix(e.api.Topic(thread).Name, "· add tests")
	})
}

func TestMessageDuringTurnIsQueued(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "first")
	s := e.session(t, 0)
	_ = e.m.Message(ctx, thread, "second")
	e.hasMessage(t, thread, "В очереди")
	if n := len(s.Sent()); n != 1 {
		t.Fatalf("second message sent too early: %v", s.Sent())
	}
	s.Emit(result())
	testutil.Eventually(t, "second turn", func() bool { return len(s.Sent()) == 2 && s.Sent()[1] == "second" })
	if n := len(e.ag.Sessions()); n != 1 {
		t.Fatalf("process must be reused, got %d processes", n)
	}
}

func TestMessageWhileWaitingForPermissionIsQueued(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "deploy")
	s := e.session(t, 0)
	done := make(chan agent.PermissionDecision, 1)
	go func() {
		done <- s.Ask(agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": "make deploy"}})
	}()
	e.sessionState(t, thread, store.StateWaiting)
	if e.br.HandleText(ctx, thread, "why?") {
		t.Fatal("plain text must not answer a permission request")
	}
	_ = e.m.Message(ctx, thread, "why?")
	e.hasMessage(t, thread, "В очереди")
	btn, _ := e.api.Button(thread, "✅")
	e.br.HandleCallback(ctx, telegram.Update{UserID: 1, ThreadID: thread, CallbackID: "cb", CallbackData: btn.Data})
	if d := <-done; !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	e.sessionState(t, thread, store.StateRunning)
	s.Emit(result())
	testutil.Eventually(t, "queued text sent", func() bool { return len(s.Sent()) == 2 && s.Sent()[1] == "why?" })
}

func TestParallelLimit(t *testing.T) {
	e, ctx := newEnv(t, 1), context.Background()
	_, _ = e.m.New(ctx, "demo", "/w/demo", "a")
	second, _ := e.m.New(ctx, "other", "/w/other", "b")
	first := e.session(t, 0)
	e.sessionState(t, second, store.StateQueued)
	if n := len(e.ag.Sessions()); n != 1 {
		t.Fatalf("limit 1 exceeded: %d processes", n)
	}
	first.Emit(result())
	if s := e.session(t, 1); s.Opts.Cwd != "/w/other" {
		t.Fatalf("wrong session started: %+v", s.Opts)
	}
	e.sessionState(t, second, store.StateRunning)
}

func TestOneTurnPerProject(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	_, _ = e.m.New(ctx, "demo", "/w/demo", "a")
	second, _ := e.m.New(ctx, "demo", "/w/demo", "b")
	first := e.session(t, 0)
	e.sessionState(t, second, store.StateQueued)
	first.Emit(result())
	e.session(t, 1)
}

func TestProcessExitMarksFailedAndResumes(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "sid-1"}})
	_ = s.Close()
	e.hasMessage(t, thread, "завершился во время хода")
	e.sessionState(t, thread, store.StateFailed)
	_ = e.m.Message(ctx, thread, "again")
	if r := e.session(t, 1); r.Opts.ResumeID != "sid-1" {
		t.Fatalf("resume id: %q", r.Opts.ResumeID)
	}
}

func TestStartErrorFailsTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.ag.StartErr = fmt.Errorf("claude not found")
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	e.hasMessage(t, thread, "Не удалось запустить claude")
	e.sessionState(t, thread, store.StateFailed)
	e.ag.StartErr = nil
	_ = e.m.Message(ctx, thread, "retry")
	s := e.session(t, 0)
	if sent := s.Sent(); len(sent) == 0 || sent[0] != "a" {
		t.Fatalf("the failed message must be sent first: %v", sent)
	}
}

func TestStopSetModeClose(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	if err := e.m.Stop(ctx, thread); err != nil || s.Interrupts() != 1 {
		t.Fatalf("stop: err=%v interrupts=%d", err, s.Interrupts())
	}
	if err := e.m.SetMode(ctx, thread, "yolo"); err == nil {
		t.Fatal("unknown mode must fail")
	}
	if err := e.m.SetMode(ctx, thread, "plan"); err != nil || s.Mode() != "plan" {
		t.Fatalf("mode: err=%v mode=%q", err, s.Mode())
	}
	_ = e.st.AddUsage(ctx, store.UsageRow{At: time.Now(), ThreadID: thread, Project: "demo", Model: "m"})
	if err := e.m.Close(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if !s.Closed() || e.m.Owns(thread) || !e.api.Topic(thread).Closed {
		t.Fatalf("closed=%v owns=%v topic=%+v", s.Closed(), e.m.Owns(thread), e.api.Topic(thread))
	}
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "завершился во время хода") {
			t.Fatal("closing is not a crash")
		}
	}
	if err := e.m.Message(ctx, thread, "x"); err != ErrUnknownSession {
		t.Fatalf("closed session: %v", err)
	}
}

func TestRestoreMarksInterrupted(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	row := store.SessionRow{ThreadID: 77, Project: "demo", Cwd: "/w/demo", Title: "x", State: store.StateRunning, ClaudeSessionID: "sid-9"}
	if err := e.st.CreateSession(ctx, &row); err != nil {
		t.Fatal(err)
	}
	idle := store.SessionRow{ThreadID: 78, Project: "demo", Cwd: "/w/demo", Title: "y", State: store.StateIdle}
	_ = e.st.CreateSession(ctx, &idle)
	if err := e.m.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if !e.m.Owns(77) || !e.m.Owns(78) {
		t.Fatal("restored sessions must be owned")
	}
	e.hasMessage(t, 77, "Нода перезапустилась")
	if len(e.api.Messages(78)) != 0 {
		t.Fatal("idle session needs no restart notice")
	}
	_ = e.m.Message(ctx, 77, "continue")
	if s := e.session(t, 0); s.Opts.ResumeID != "sid-9" {
		t.Fatalf("resume id: %q", s.Opts.ResumeID)
	}
}

func TestTurnEndWithdrawsOpenRequests(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	done := make(chan agent.PermissionDecision, 1)
	go func() {
		done <- s.Ask(agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": "make deploy"}})
	}()
	e.sessionState(t, thread, store.StateWaiting)
	_ = e.m.Stop(ctx, thread)
	s.Emit(result())
	select {
	case d := <-done:
		if d.Allow {
			t.Fatalf("decision: %+v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request still open after the turn ended")
	}
}

func TestCloseIgnoresLateResult(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	e.session(t, 0)
	_ = e.st.AddUsage(ctx, store.UsageRow{At: time.Now(), ThreadID: thread, Project: "demo", Model: "m"})
	e.m.mu.Lock()
	s := e.m.sessions[thread]
	e.m.mu.Unlock()
	if err := e.m.Close(ctx, thread); err != nil {
		t.Fatal(err)
	}
	before := len(e.api.Messages(thread))
	e.m.handleEvent(s, result())
	e.m.handleEvent(s, agent.Event{Kind: agent.EventText, Text: "late"})
	if n := len(e.api.Messages(thread)); n != before {
		t.Fatalf("closed topic got %d new messages", n-before)
	}
	if rows, _ := e.st.OpenSessions(ctx); len(rows) != 0 {
		t.Fatalf("closed session reopened in store: %+v", rows)
	}
	if got := e.api.Topic(thread); got.Name != "demo · a" || !got.Closed || got.Icon != "i-ok" {
		t.Fatalf("topic after close: %+v", got)
	}
}

func TestCloseEmptySessionRemovesTopic(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	if err := e.m.Close(ctx, thread); err != nil {
		t.Fatal(err)
	}
	if e.api.Topic(thread).ID != 0 {
		t.Fatal("empty session topic must be deleted")
	}
	if rows, _ := e.st.Cleanable(ctx, time.Now().Add(time.Hour)); len(rows) != 0 {
		t.Fatalf("removed topic still cleanable: %+v", rows)
	}
}

func TestCloseSessionWithHistoryKeepsTopic(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "sid-1"}})
	testutil.Eventually(t, "session id stored", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		return e.m.sessions[thread].row.ClaudeSessionID == "sid-1"
	})
	if err := e.m.Close(ctx, thread); err != nil { // no usage rows: the turn never finished
		t.Fatal(err)
	}
	if got := e.api.Topic(thread); got.ID == 0 || !got.Closed || got.Icon != "i-ok" {
		t.Fatalf("topic with history must be closed, not deleted: %+v", got)
	}
}

func TestFailedIconAndRecovery(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	_ = s.Close() // process dies mid-turn
	e.sessionState(t, thread, store.StateFailed)
	testutil.Eventually(t, "failed icon", func() bool { return e.api.Topic(thread).Icon == "i-fail" })
	if e.api.Topic(thread).Closed {
		t.Fatal("failed topic must stay open")
	}
	_ = e.m.Message(ctx, thread, "again")
	e.session(t, 1)
	testutil.Eventually(t, "active icon back", func() bool { return e.api.Topic(thread).Icon == "i-active" })
}

func TestUserDeletedTopicNotCleanable(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	gone, _ := e.m.New(ctx, "demo", "/w/demo", "")
	e.api.DeleteTopic(gone)
	e.m.probeTopics(ctx)
	if rows, _ := e.st.Cleanable(ctx, time.Now().Add(time.Hour)); len(rows) != 0 {
		t.Fatalf("user-deleted topic offered for cleanup: %+v", rows)
	}
}

func TestTopicRemovedDropsSession(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	e.m.TopicRemoved(thread)
	if e.m.Owns(thread) {
		t.Fatal("session still loaded")
	}
	e.m.TopicRemoved(12345) // unknown thread: no panic
}

func TestCrashRequeuesWaitingMessages(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "sid-1"}})
	_ = e.m.Message(ctx, thread, "queued")
	_ = s.Close()
	r := e.session(t, 1)
	if r.Opts.ResumeID != "sid-1" {
		t.Fatalf("resume id: %q", r.Opts.ResumeID)
	}
	testutil.Eventually(t, "queued message sent after crash", func() bool {
		sent := r.Sent()
		return len(sent) == 1 && sent[0] == "queued"
	})
}

func TestDeletedTopicClosesSession(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	e.api.DeleteTopic(thread)
	s.Emit(agent.Event{Kind: agent.EventText, Text: "hello"})
	s.Emit(result())
	testutil.Eventually(t, "session dropped", func() bool { return !e.m.Owns(thread) && s.Closed() })
	if rows, _ := e.st.OpenSessions(ctx); len(rows) != 0 {
		t.Fatalf("store still has open sessions: %+v", rows)
	}
}

func TestProbeClosesIdleSessionWithDeletedTopic(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	gone, _ := e.m.New(ctx, "demo", "/w/demo", "")
	kept, _ := e.m.New(ctx, "other", "/w/other", "")
	e.api.DeleteTopic(gone)
	e.m.probeTopics(ctx)
	if e.m.Owns(gone) || !e.m.Owns(kept) {
		t.Fatalf("owns gone=%v kept=%v", e.m.Owns(gone), e.m.Owns(kept))
	}
}

func TestAttachResumesExistingSession(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, err := e.m.Attach(ctx, "demo", "/w/demo", "sid-x", "Fix login", false)
	if err != nil {
		t.Fatal(err)
	}
	if name := e.api.Topic(thread).Name; name != "demo · Fix login" {
		t.Fatalf("topic: %q", name)
	}
	if got, ok := e.m.FindByClaudeID("sid-x"); !ok || got != thread {
		t.Fatalf("find: %d %v", got, ok)
	}
	if len(e.ag.Sessions()) != 0 {
		t.Fatal("no process before the first message")
	}
	_ = e.m.Message(ctx, thread, "continue")
	s := e.session(t, 0)
	if s.Opts.ResumeID != "sid-x" || s.Opts.Fork {
		t.Fatalf("opts: %+v", s.Opts)
	}
}

func TestAttachForkAppliesOnlyToFirstStart(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.Attach(ctx, "demo", "/w/demo", "sid-live", "", true)
	_ = e.m.Message(ctx, thread, "go")
	s := e.session(t, 0)
	if s.Opts.ResumeID != "sid-live" || !s.Opts.Fork {
		t.Fatalf("first start: %+v", s.Opts)
	}
	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "sid-copy"}})
	_ = s.Close()
	e.sessionState(t, thread, store.StateFailed)
	_ = e.m.Message(ctx, thread, "again")
	if r := e.session(t, 1); r.Opts.ResumeID != "sid-copy" || r.Opts.Fork {
		t.Fatalf("second start: %+v", r.Opts)
	}
	if _, ok := e.m.FindByClaudeID("sid-live"); ok {
		t.Fatal("after fork the topic belongs to the copy, not the original")
	}
}

func project(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func sendFileTool(t *testing.T, s *agent.FakeSession) agent.Tool {
	t.Helper()
	for _, tool := range s.Opts.Tools {
		if tool.Name == "send_file" {
			return tool
		}
	}
	t.Fatalf("send_file tool missing: %+v", s.Opts.Tools)
	return agent.Tool{}
}

func TestSendFileTool(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"docs/spec.md": "# Spec\n\nDetails here."})
	thread, _ := e.m.New(ctx, "demo", dir, "write the spec")
	s := e.session(t, 0)
	if !strings.Contains(s.Opts.AppendSystemPrompt, "send_file") {
		t.Fatalf("system prompt: %q", s.Opts.AppendSystemPrompt)
	}
	tool := sendFileTool(t, s)
	out, err := tool.Handler(map[string]any{"path": "docs/spec.md", "caption": "please check"})
	if err != nil || out == "" {
		t.Fatalf("handler: %q %v", out, err)
	}
	docs := e.api.Documents(thread)
	if len(docs) != 1 || docs[0].Name != "spec.md" || !strings.Contains(docs[0].Caption, "please check") || string(docs[0].Data) != "# Spec\n\nDetails here." {
		t.Fatalf("docs: %+v", docs)
	}
	if out, _ := tool.Handler(map[string]any{"path": "docs/spec.md"}); !strings.Contains(out, "уже") {
		t.Fatalf("same version must not be sent twice: %q", out)
	}
	if _, err := tool.Handler(map[string]any{"path": "/etc/hosts"}); err == nil {
		t.Fatal("files outside the project must be refused")
	}
	if n := len(e.api.Documents(thread)); n != 1 {
		t.Fatalf("documents: %d", n)
	}
}

func TestTurnEndDeliversFiles(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.AutoSend = []string{"**/*.pdf"}
	dir := project(t, map[string]string{"docs/plan.md": "# Plan", "main.go": "package main", "out/report.pdf": "%PDF"})
	thread, _ := e.m.New(ctx, "demo", dir, "plan it")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(dir, "docs/plan.md")}})
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Edit", ToolInput: map[string]any{"file_path": filepath.Join(dir, "main.go")}})
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(dir, "out/report.pdf")}})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "Plan written to `docs/plan.md`, please review."})
	s.Emit(result())
	e.hasMessage(t, thread, "Изменено за ход")
	names := map[string]bool{}
	testutil.Eventually(t, "documents delivered", func() bool {
		names = map[string]bool{}
		for _, d := range e.api.Documents(thread) {
			names[d.Name] = true
		}
		return names["plan.md"] && names["report.pdf"]
	})
	if !names["plan.md"] || !names["report.pdf"] || names["main.go"] {
		t.Fatalf("auto-sent documents: %v", names)
	}
	before := len(e.api.Messages(thread))
	_ = e.m.Message(ctx, thread, "next")
	s.Emit(result())
	e.hasMessage(t, thread, "шагов: 0")
	for _, m := range e.api.Messages(thread)[before:] {
		if strings.Contains(m.HTML, "Изменено за ход") {
			t.Fatal("files of the previous turn must not be listed again")
		}
	}
}

func TestSendFileCommandAndDiff(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"a.go": "old\n"})
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, out)
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("new\n"), 0o644)
	thread, _ := e.m.New(ctx, "demo", dir, "")
	if err := e.m.SendFile(ctx, thread, "a.go"); err != nil {
		t.Fatal(err)
	}
	if err := e.m.SendFile(ctx, thread, "a.go"); err != nil {
		t.Fatal("/file must resend on request")
	}
	if n := len(e.api.Documents(thread)); n != 2 {
		t.Fatalf("documents: %d", n)
	}
	if err := e.m.SendFile(ctx, thread, "../etc/passwd"); err == nil {
		t.Fatal("escape must be refused")
	}
	_ = e.m.Message(ctx, thread, "edit")
	s := e.session(t, 0)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("agent\n"), 0o644) // the turn's own change
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Edit", ToolInput: map[string]any{"file_path": filepath.Join(dir, "a.go")}})
	s.Emit(result())
	press(t, e, thread, "🔀 Diff")
	testutil.Eventually(t, "diff sent", func() bool {
		for _, d := range e.api.Documents(thread) {
			if strings.HasPrefix(d.Name, "turn-") && strings.Contains(string(d.Data), "-new") && strings.Contains(string(d.Data), "+agent") {
				return true
			}
		}
		return false
	})
}

func TestMentionsSendOnlyDocumentsAndAreCapped(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	fs := map[string]string{"a.go": "package a", "b.go": "package b"}
	var text []string
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("docs/n%d.md", i)
		fs[name] = "# N"
		text = append(text, name)
	}
	dir := project(t, fs)
	thread, _ := e.m.New(ctx, "demo", dir, "go")
	s := e.session(t, 0)
	for name := range fs {
		s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(dir, name)}})
	}
	s.Emit(agent.Event{Kind: agent.EventText, Text: "Changed a.go, b.go and " + strings.Join(text, ", ")})
	s.Emit(result())
	e.hasMessage(t, thread, "Изменено за ход")
	testutil.Eventually(t, "capped delivery", func() bool { return len(e.api.Documents(thread)) == 5 })
	for _, d := range e.api.Documents(thread) {
		if strings.HasSuffix(d.Name, ".go") {
			t.Fatalf("source files must not be auto-sent: %s", d.Name)
		}
	}
}

func TestAgentEnvPerSession(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.AgentEnv = func(thread int) map[string]string { return map[string]string{"TGSYNC_THREAD": fmt.Sprint(thread)} }
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	if s := e.session(t, 0); s.Opts.Env["TGSYNC_THREAD"] != fmt.Sprint(thread) {
		t.Fatalf("env: %v", s.Opts.Env)
	}
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }

func withClock(e *env) *clock {
	c := &clock{t: time.Now()}
	e.m.d.Now = c.now
	return c
}

func TestIdleTimeoutStopsProcessAndResumes(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.IdleTimeout = time.Hour
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "sid-1"}})
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	c.add(30 * time.Minute)
	e.m.tick(ctx)
	if s.Closed() {
		t.Fatal("process closed too early")
	}
	c.add(31 * time.Minute)
	e.m.tick(ctx)
	testutil.Eventually(t, "idle process stopped", s.Closed)
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "завершился во время хода") {
			t.Fatal("idle stop is not a crash")
		}
	}
	_ = e.m.Message(ctx, thread, "back")
	if r := e.session(t, 1); r.Opts.ResumeID != "sid-1" {
		t.Fatalf("resume: %q", r.Opts.ResumeID)
	}
}

func TestStallWarning(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.StallWarn = 20 * time.Minute
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	c.add(21 * time.Minute)
	e.m.tick(ctx)
	e.hasMessage(t, thread, "Нет активности")
	n := len(e.api.Messages(thread))
	e.m.tick(ctx)
	if len(e.api.Messages(thread)) != n {
		t.Fatal("warning must not repeat on every tick")
	}
	btn, ok := e.api.Button(thread, "⏳")
	if !ok {
		t.Fatal("wait button missing")
	}
	if _, handled := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: btn.Data}); !handled {
		t.Fatal("wait not handled")
	}
	c.add(19 * time.Minute)
	e.m.tick(ctx)
	if len(e.api.Messages(thread)) != n {
		t.Fatal("«Ждать» must postpone the next warning")
	}
	stop, _ := e.api.Button(thread, "⏹")
	e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: stop.Data})
	if s.Interrupts() != 1 {
		t.Fatalf("stop button: interrupts=%d", s.Interrupts())
	}
}

func TestNoStallWarningWhileWaitingForUser(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.StallWarn = time.Minute
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	go s.Ask(agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": "make x"}})
	testutil.Eventually(t, "session waits for the user", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		return e.m.sessions[thread].waits > 0
	})
	c.add(time.Hour)
	e.m.tick(ctx)
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "Нет активности") {
			t.Fatal("waiting for the user is not a stall")
		}
	}
}

func TestMaxTurnDuration(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.MaxTurn = time.Hour
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	c.add(61 * time.Minute)
	e.m.tick(ctx)
	e.m.tick(ctx)
	e.hasMessage(t, thread, "MAX_TURN_DURATION")
	if s.Interrupts() != 1 {
		t.Fatalf("interrupts: %d", s.Interrupts())
	}
}

// TestMaxTurnInterruptFailureBacksOff: an interrupt that keeps failing is
// retried once a minute, with one warning per attempt, not on every tick.
func TestMaxTurnInterruptFailureBacksOff(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.MaxTurn = time.Hour
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.SetInterruptErr(fmt.Errorf("control channel closed"))
	warnings := func() int {
		n := 0
		for _, m := range e.api.Messages(thread) {
			if strings.Contains(m.HTML, "прервать его не удалось") {
				n++
			}
		}
		return n
	}
	c.add(61 * time.Minute)
	e.m.tick(ctx)
	testutil.Eventually(t, "first warning", func() bool { return warnings() == 1 })
	for i := 0; i < 5; i++ { // ticks come every second
		c.add(time.Second)
		e.m.tick(ctx)
	}
	time.Sleep(50 * time.Millisecond) // notifyTimers runs in its own goroutine
	if n, w := s.Interrupts(), warnings(); n != 1 || w != 1 {
		t.Fatalf("within a minute: interrupts=%d warnings=%d, want 1 and 1", n, w)
	}
	c.add(time.Minute)
	e.m.tick(ctx)
	testutil.Eventually(t, "retry after a minute", func() bool { return s.Interrupts() == 2 && warnings() == 2 })
	s.SetInterruptErr(nil)
	c.add(time.Minute)
	e.m.tick(ctx)
	e.hasMessage(t, thread, "прерываю")
}

func TestProfileAppliedToProcess(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.Profiles = func(name, project string) (string, agent.StartOptions, error) {
		if name == "bad" {
			return "", agent.StartOptions{}, fmt.Errorf("профиль bad не найден")
		}
		return "lean", agent.StartOptions{SettingSources: []string{"user"}, Env: map[string]string{"X": "1"}, Settings: `{"a":1}`}, nil
	}
	e.m.d.AgentEnv = func(int) map[string]string { return map[string]string{"TGSYNC_THREAD": "1"} }
	if _, err := e.m.NewWithProfile(ctx, "demo", "/w/demo", "go", "bad"); err == nil {
		t.Fatal("unknown profile must be refused before a topic is created")
	}
	thread, err := e.m.NewWithProfile(ctx, "demo", "/w/demo", "go", "")
	if err != nil {
		t.Fatal(err)
	}
	s := e.session(t, 0)
	if s.Opts.Settings != `{"a":1}` || s.Opts.Env["X"] != "1" || s.Opts.Env["TGSYNC_THREAD"] != "1" || len(s.Opts.SettingSources) != 1 {
		t.Fatalf("opts: %+v", s.Opts)
	}
	rows, _ := e.st.OpenSessions(ctx)
	if rows[0].Profile != "lean" {
		t.Fatalf("profile not stored: %+v", rows[0])
	}
	e.hasMessage(t, thread, "профиль lean")
}

func TestInitSummaryAndSkills(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.ShowHookOutput = true
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	if err := e.m.Skills(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "после первого ответа")
	s := e.session(t, 0)
	init := agent.Event{Kind: agent.EventInit, Init: &agent.InitInfo{SessionID: "x", Plugins: []string{"superpowers", "caveman"},
		MCPServers:    []agent.MCPServer{{Name: "context7", Status: "needs-auth"}, {Name: "tgsync", Status: "connected"}},
		SlashCommands: []string{"compact", "code-review"}}}
	s.Emit(init)
	s.Emit(init)
	e.hasMessage(t, thread, "context7 ⚠ needs-auth")
	n := 0
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "🔌") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("plugin summary must be shown once, got %d", n)
	}
	s.Emit(agent.Event{Kind: agent.EventHook, Text: "hook says hi"})
	e.hasMessage(t, thread, "hook says hi")
	_ = e.m.Skills(ctx, thread)
	e.hasMessage(t, thread, "/code-review")
}

// slowSession is an agent session whose events channel is closed only when
// the test says so, to reproduce a late exit of an old process.
type slowSession struct {
	*agent.FakeSession
	events chan agent.Event
}

func (s *slowSession) Events() <-chan agent.Event { return s.events }
func (s *slowSession) Close() error               { return nil }

type slowRunner struct {
	agent.Fake
	mu    sync.Mutex
	slows []*slowSession
}

func (r *slowRunner) Start(ctx context.Context, o agent.StartOptions) (agent.Session, error) {
	a, _ := r.Fake.Start(ctx, o)
	s := &slowSession{FakeSession: a.(*agent.FakeSession), events: make(chan agent.Event, 10)}
	r.mu.Lock()
	r.slows = append(r.slows, s)
	r.mu.Unlock()
	return s, nil
}

func TestLateExitOfIdleProcessDoesNotBreakNewTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	run := &slowRunner{}
	e.m.d.Runner = run
	e.m.d.IdleTimeout = time.Hour
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	run.mu.Lock()
	old := run.slows[0]
	run.mu.Unlock()
	old.events <- result()
	e.hasMessage(t, thread, "Ход завершён")
	c.add(2 * time.Hour)
	e.m.tick(ctx)
	_ = e.m.Message(ctx, thread, "next")
	testutil.Eventually(t, "new process", func() bool { run.mu.Lock(); defer run.mu.Unlock(); return len(run.slows) == 2 })
	close(old.events)
	time.Sleep(100 * time.Millisecond)
	e.m.mu.Lock()
	inTurn := e.m.sessions[thread].inTurn
	e.m.mu.Unlock()
	if !inTurn {
		t.Fatal("late exit of the old process ended the new turn")
	}
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "завершился во время хода") {
			t.Fatal("late exit of an idle process is not a crash")
		}
	}
}

func TestMaxTurnWithdrawsPrompts(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.MaxTurn = time.Hour
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	done := make(chan agent.PermissionDecision, 1)
	go func() {
		done <- s.Ask(agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": "make x"}})
	}()
	e.hasMessage(t, thread, "Запрос разрешения")
	c.add(2 * time.Hour)
	e.m.tick(ctx)
	select {
	case d := <-done:
		if d.Allow {
			t.Fatal("prompt must be withdrawn, not allowed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("prompt still open after MAX_TURN_DURATION")
	}
}

func TestReceiveFile(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"main.go": "package main"})
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	thread, _ := e.m.New(ctx, "demo", dir, "")
	if err := e.m.ReceiveFile(ctx, thread, "screen.png", []byte("PNG"), ""); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "передам со следующим сообщением")
	if n := len(e.ag.Sessions()); n != 0 {
		t.Fatal("a file without a caption must not start a turn")
	}
	_ = e.m.ReceiveFile(ctx, thread, "../../log.txt", []byte("LOG"), "")
	_ = e.m.Message(ctx, thread, "почини ошибку на скриншоте")
	s := e.session(t, 0)
	sent := s.Sent()[0]
	if !strings.Contains(sent, ".tgsync/inbox/") || !strings.Contains(sent, "screen.png") || !strings.Contains(sent, "log.txt") || !strings.HasSuffix(sent, "почини ошибку на скриншоте") {
		t.Fatalf("message to agent: %q", sent)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, ".tgsync", "inbox", "*screen.png"))
	if len(matches) != 1 {
		t.Fatalf("saved files: %v", matches)
	}
	if data, _ := os.ReadFile(matches[0]); string(data) != "PNG" {
		t.Fatalf("content: %q", data)
	}
	if ex, _ := os.ReadFile(filepath.Join(dir, ".git", "info", "exclude")); !strings.Contains(string(ex), ".tgsync/") {
		t.Fatalf("inbox must be excluded from git: %q", ex)
	}
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	if err := e.m.ReceiveFile(ctx, thread, "spec.pdf", []byte("PDF"), "прочитай и кратко перескажи"); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "caption starts a turn", func() bool {
		sent := s.Sent()
		return len(sent) == 2 && strings.Contains(sent[1], "spec.pdf") && strings.HasSuffix(sent[1], "прочитай и кратко перескажи")
	})
}

func TestListDir(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	fs := map[string]string{"README.md": "# R", "docs/a.md": "a", "node_modules/x/y.js": "y", ".git/HEAD": "ref", ".env": "SECRET"}
	for i := 0; i < 35; i++ {
		fs[fmt.Sprintf("many/f%02d.txt", i)] = "x"
	}
	dir := project(t, fs)
	e.m.d.Protected = []string{filepath.Join(dir, ".env")}
	thread, _ := e.m.New(ctx, "demo", dir, "")
	if err := e.m.ListDir(ctx, thread, "", 0); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.api.Button(thread, "📁 docs"); !ok {
		t.Fatal("docs folder button missing")
	}
	for _, hidden := range []string{"📁 node_modules", "📁 .git", "📄 .env"} {
		if _, ok := e.api.Button(thread, hidden); ok {
			t.Fatalf("%s must be hidden", hidden)
		}
	}
	msgs := e.api.Messages(thread)
	listing := msgs[len(msgs)-1]
	btn, _ := e.api.Button(thread, "📁 many")
	e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: listing.ID, CallbackData: btn.Data})
	testutil.Eventually(t, "folder opened in place", func() bool {
		for _, m := range e.api.Messages(thread) {
			if m.ID == listing.ID && strings.Contains(m.HTML, "many") {
				return true
			}
		}
		return false
	})
	if _, ok := e.api.Button(thread, "⬆"); !ok {
		t.Fatal("up button missing")
	}
	more, ok := e.api.Button(thread, "➡")
	if !ok {
		t.Fatal("paging button missing for 35 entries")
	}
	e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: listing.ID, CallbackData: more.Data})
	testutil.Eventually(t, "second page", func() bool { _, ok := e.api.Button(thread, "📄 f34.txt"); return ok })
	f, _ := e.api.Button(thread, "📄 f34.txt")
	e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: listing.ID, CallbackData: f.Data})
	testutil.Eventually(t, "file sent", func() bool { return len(e.api.Documents(thread)) == 1 })
	if err := e.m.ListDir(ctx, thread, "../..", 0); err == nil {
		t.Fatal("escape must be refused")
	}
}

func press(t *testing.T, e *env, thread int, prefix string) {
	t.Helper()
	var btn telegram.Button
	testutil.Eventually(t, "button "+prefix, func() bool { var ok bool; btn, ok = e.api.Button(thread, prefix); return ok })
	msgID := 0
	for _, m := range e.api.Messages(thread) {
		for _, row := range m.Keyboard {
			for _, b := range row {
				if b == btn {
					msgID = m.ID
				}
			}
		}
	}
	if _, ok := e.m.HandleButton(context.Background(), telegram.Update{ThreadID: thread, MessageID: msgID, CallbackData: btn.Data}); !ok {
		t.Fatalf("button %s not handled", prefix)
	}
}

func TestSessionButtons(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", t.TempDir(), "go")
	s := e.session(t, 0)
	press(t, e, thread, "⏹")
	if s.Interrupts() != 1 {
		t.Fatalf("stop button: %d", s.Interrupts())
	}
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "последнее событие") && m.Keyboard != nil {
			t.Fatal("finished status must lose its stop button")
		}
	}
	press(t, e, thread, "⋯")
	press(t, e, thread, "⚙ Режим")
	press(t, e, thread, "plan")
	testutil.Eventually(t, "mode set", func() bool { return s.Mode() == "plan" })
	press(t, e, thread, "📂 Файлы")
	e.hasMessage(t, thread, "📂 <code>/</code>")
	press(t, e, thread, "✖ Закрыть")
	if e.api.Topic(thread).Closed {
		t.Fatal("close must ask for confirmation first")
	}
	press(t, e, thread, "Отмена")
	if !e.m.Owns(thread) {
		t.Fatal("cancel must keep the session")
	}
	press(t, e, thread, "✖ Закрыть")
	press(t, e, thread, "Да, закрыть")
	testutil.Eventually(t, "closed", func() bool { return !e.m.Owns(thread) })
}
