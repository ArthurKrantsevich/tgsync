package session

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

// A panic in a session goroutine used to take the whole node down; only the
// Telegram update handlers recovered. These tests panic in each goroutine
// and check that the node lives on with the session in a usable state.

// panicLimits panics on every rate limit event.
type panicLimits struct{}

func (panicLimits) Observe(context.Context, int, agent.RateLimit) { panic("limits boom") }
func (panicLimits) Windows() []store.RateLimitRow                 { return nil }

// panicAPI panics when a sent or edited message contains marker.
type panicAPI struct {
	*telegram.Fake
	mu     sync.Mutex
	marker string
}

func (p *panicAPI) arm(marker string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.marker = marker
}

func (p *panicAPI) check(html string) {
	p.mu.Lock()
	m := p.marker
	p.mu.Unlock()
	if m != "" && strings.Contains(html, m) {
		panic("telegram boom: " + m)
	}
}

func (p *panicAPI) SendMessage(ctx context.Context, thread int, html string, kb telegram.Keyboard, silent bool) (int, error) {
	p.check(html)
	return p.Fake.SendMessage(ctx, thread, html, kb, silent)
}

func (p *panicAPI) EditMessage(ctx context.Context, msgID int, html string, kb telegram.Keyboard) error {
	p.check(html)
	return p.Fake.EditMessage(ctx, msgID, html, kb)
}

func withPanicAPI(e *env) *panicAPI {
	p := &panicAPI{Fake: e.api}
	e.m.d.API = p
	return p
}

func TestPanicInEventInterruptsTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.Limits = panicLimits{}
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventRateLimit, Limit: &agent.RateLimit{Window: "five_hour", Status: "allowed"}})
	e.hasMessage(t, thread, "Внутренняя ошибка tgsync")
	testutil.Eventually(t, "turn interrupted", func() bool { return s.Interrupts() == 1 })
	// The CLI ends the interrupted turn as usual; the events keep flowing.
	s.Emit(agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "error_during_execution", IsError: true}})
	e.hasMessage(t, thread, "⏹ Ход прерван")
	e.sessionState(t, thread, store.StateIdle)
	_ = e.m.Message(ctx, thread, "again")
	testutil.Eventually(t, "next turn", func() bool { return len(s.Sent()) == 2 })
}

func TestPanicWhileFinishingTurnStartsQueuedTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	p := withPanicAPI(e)
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	_ = e.m.Message(ctx, thread, "queued")
	e.hasMessage(t, thread, "В очереди")
	p.arm("BOOM")
	s.Emit(agent.Event{Kind: agent.EventText, Text: "BOOM"})
	s.Emit(result())
	testutil.Eventually(t, "queued turn started", func() bool { return len(s.Sent()) == 2 })
	p.arm("")
	s.Emit(result())
	e.sessionState(t, thread, store.StateIdle)
}

func TestPanicInFileSummaryKeepsNode(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	p := withPanicAPI(e)
	dir := project(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	p.arm("Изменено за ход")
	agentTurn(s, dir, "a.go")
	e.hasMessage(t, thread, "Ход завершён")
	e.hasMessage(t, thread, "Внутренняя ошибка tgsync")
	p.arm("")
	_ = e.m.Message(ctx, thread, "again")
	agentTurn(s, dir, "a.go")
	e.hasMessage(t, thread, "Изменено за ход")
}

func TestPanicInTickKeepsNode(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	p := withPanicAPI(e)
	c := withClock(e)
	e.m.d.StallWarn = time.Minute
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Read", ToolInput: map[string]any{"file_path": "/w/demo/a.go"}})
	testutil.Eventually(t, "status shows the tool", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		return e.m.sessions[thread].status.Step == 1
	})
	p.arm("Read") // the status refresh of the tick loop
	c.add(2 * time.Minute)
	e.m.runTick(ctx) // the stall warning goroutine too: its text has no marker
	e.hasMessage(t, thread, "Нет активности")
	p.arm("Нет активности")
	c.add(2 * time.Minute)
	e.m.mu.Lock()
	e.m.sessions[thread].warned = false
	e.m.mu.Unlock()
	e.m.runTick(ctx)
	time.Sleep(50 * time.Millisecond)
	p.arm("")
	s.Emit(result())
	e.sessionState(t, thread, store.StateIdle)
}

// panicRunner panics on its first Start.
type panicRunner struct {
	agent.Fake
	mu     sync.Mutex
	panics int
}

func (r *panicRunner) Start(ctx context.Context, o agent.StartOptions) (agent.Session, error) {
	r.mu.Lock()
	first := r.panics == 0
	r.panics++
	r.mu.Unlock()
	if first {
		panic("runner boom")
	}
	return r.Fake.Start(ctx, o)
}

func TestPanicStartingTurnFailsIt(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	r := &panicRunner{}
	e.m.d.Runner = r
	thread, err := e.m.New(ctx, "demo", "/w/demo", "a")
	if err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "Внутренняя ошибка tgsync")
	e.sessionState(t, thread, store.StateFailed)
	_ = e.m.Message(ctx, thread, "again")
	testutil.Eventually(t, "next turn", func() bool { return len(r.Sessions()) == 1 })
	if sent := r.Sessions()[0].Sent(); len(sent) != 1 || !strings.Contains(sent[0], "again") {
		t.Fatalf("sent: %v", sent)
	}
}

func TestPanicInSendFileToolIsAnError(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.API = &docPanicAPI{panicAPI: &panicAPI{Fake: e.api}}
	dir := project(t, map[string]string{"plan.md": "# plan\n"})
	_, _ = e.m.New(ctx, "demo", dir, "a")
	s := e.session(t, 0)
	_, err := sendFileTool(t, s).Handler(map[string]any{"path": "plan.md"})
	if err == nil || !strings.Contains(err.Error(), "внутренняя ошибка") {
		t.Fatalf("err: %v", err)
	}
}

// docPanicAPI panics on every document upload.
type docPanicAPI struct{ *panicAPI }

func (docPanicAPI) SendDocument(context.Context, int, string, []byte, string, bool) (int, error) {
	panic(errors.New("upload boom"))
}
