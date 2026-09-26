package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

func ctxInfo(pct float64) *agent.ContextInfo {
	return &agent.ContextInfo{Model: "opus", Total: int(pct * 2000), Max: 200000, Percent: pct, AutoCompact: true,
		Categories: []agent.Category{{Name: "Messages", Tokens: int(pct * 1500)}}}
}

func resultWith(models ...agent.ModelUsage) agent.Event {
	return agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "success", CostUSD: 0.01, Models: models}}
}

func countPrefix(e *env, thread int, prefix string) (int, telegram.FakeMessage) {
	n, last := 0, telegram.FakeMessage{}
	for _, m := range e.api.Messages(thread) {
		if strings.HasPrefix(m.HTML, prefix) {
			n, last = n+1, m
		}
	}
	return n, last
}

func TestContextInResultAndHint(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.SetContext(ctxInfo(40), nil, 0)
	s.Emit(result())
	e.hasMessage(t, thread, "· 🧠 40%")
	for i, pct := range []float64{78, 80} {
		_ = e.m.Message(ctx, thread, "more")
		want := i + 2
		testutil.Eventually(t, "next turn", func() bool { return len(s.Sent()) == want })
		s.SetContext(ctxInfo(pct), nil, 0)
		s.Emit(result())
		e.hasMessage(t, thread, fmt.Sprintf("🧠 %d%%", int(pct)))
	}
	e.hasMessage(t, thread, "💡 Контекст 78% — сожми историю, пока агент не сделал это сам")
	n, hint := countPrefix(e, thread, "💡 Контекст")
	if n != 1 {
		t.Fatalf("hint once per crossing, got %d", n)
	}
	btn := hint.Keyboard[0][0]
	if btn.Text != "🗜 Сжать" {
		t.Fatalf("hint button: %+v", btn)
	}
	if alert, ok := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: btn.Data}); !ok || alert != "Сжимаю…" {
		t.Fatalf("compact button: %q %v", alert, ok)
	}
	testutil.Eventually(t, "/compact sent", func() bool {
		sent := s.Sent()
		return sent[len(sent)-1] == "/compact"
	})
}

func TestHintAgainAfterDrop(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	for i, pct := range []float64{75, 30, 76} {
		if i > 0 {
			_ = e.m.Message(ctx, thread, "next")
			want := i + 1
			testutil.Eventually(t, "turn", func() bool { return len(s.Sent()) == want })
		}
		s.SetContext(ctxInfo(pct), nil, 0)
		s.Emit(result())
		e.hasMessage(t, thread, fmt.Sprintf("🧠 %d%%", int(pct)))
	}
	if n, _ := countPrefix(e, thread, "💡 Контекст"); n != 2 {
		t.Fatalf("hint after dropping below and crossing again: %d", n)
	}
}

func TestNoContextWithoutProcess(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(result()) // the fake reports no context by default
	e.hasMessage(t, thread, "Ход завершён")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "🧠") || strings.Contains(m.HTML, "💡") {
			t.Fatalf("no context data, no context line: %q", m.HTML)
		}
	}
	if err := e.m.Context(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "Данных о контексте пока нет")
}

func TestContextUsageTimeout(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.SetContext(ctxInfo(50), nil, time.Hour) // hangs
	start := time.Now()
	s.Emit(result())
	for !strings.Contains(lastHTML(e, thread), "Ход завершён") {
		if time.Since(start) > contextTimeout+2*time.Second {
			t.Fatalf("the answer waited more than %v for the context", contextTimeout+2*time.Second)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestContextCommand(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.SetContext(ctxInfo(71), nil, 0)
	s.Emit(result())
	e.hasMessage(t, thread, "🧠 71%")
	if err := e.m.Context(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "🧠 <b>Контекст</b> · opus · 142K / 200K (71%)")
	_ = s.Close()
	testutil.Eventually(t, "process gone", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		return e.m.sessions[thread].agent == nil
	})
	if err := e.m.Context(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "процесс сейчас не запущен")
}

func TestCompactionNotice(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventCompacted, Text: "auto", Tokens: 182000})
	e.hasMessage(t, thread, "🗜 История сжата (авто), было 182K")
}

// fakeLimits records observed limit events.
type fakeLimits struct {
	mu   sync.Mutex
	seen []agent.RateLimit
	rows []store.RateLimitRow
}

func (f *fakeLimits) Observe(_ context.Context, _ int, rl agent.RateLimit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, rl)
}

func (f *fakeLimits) Windows() []store.RateLimitRow { return f.rows }

func TestRateLimitForwardedAndUsageRecorded(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	lim := &fakeLimits{rows: []store.RateLimitRow{{Name: "five_hour", Status: "allowed", Utilization: 0.5}}}
	e.m.d.Limits = lim
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventRateLimit, Limit: &agent.RateLimit{Window: "five_hour", Status: "allowed_warning"}})
	s.Emit(resultWith(agent.ModelUsage{Model: "opus", Input: 100, Output: 50, CacheRead: 5000, CacheCreate: 10, CostUSD: 0.3}))
	e.hasMessage(t, thread, "Ход завершён")
	testutil.Eventually(t, "limit forwarded", func() bool {
		lim.mu.Lock()
		defer lim.mu.Unlock()
		return len(lim.seen) == 1 && lim.seen[0].Status == "allowed_warning"
	})
	if err := e.m.SessionUsage(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "📊 <b>Эта сессия</b>: ходов 1 · 160 ток. (+5K из кеша) · ≈$0.30")
	if err := e.m.NodeUsage(ctx, 3); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, 3, "5 часов: 50%")
	e.hasMessage(t, 3, "demo 160 · ≈$0.30")
}

func TestNoUsageRowsWithoutModels(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "error_during_execution", IsError: true}})
	e.hasMessage(t, thread, "Ход завершился с ошибкой")
	u, err := e.st.SessionUsage(ctx, thread)
	if err != nil || u.Turns != 0 {
		t.Fatalf("usage %+v %v", u, err)
	}
}

func lastHTML(e *env, thread int) string {
	msgs := e.api.Messages(thread)
	if len(msgs) == 0 {
		return ""
	}
	return msgs[len(msgs)-1].HTML
}

func TestUsageIsDeltaOfCumulativeTotals(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.Emit(resultWith(agent.ModelUsage{Model: "opus", Input: 100, CostUSD: 1}))
	e.hasMessage(t, thread, "Ход завершён")
	_ = e.m.Message(ctx, thread, "more")
	testutil.Eventually(t, "second turn", func() bool { return len(s.Sent()) == 2 })
	s.Emit(resultWith(agent.ModelUsage{Model: "opus", Input: 250, CostUSD: 2.5})) // running totals
	testutil.Eventually(t, "second result", func() bool {
		u, _ := e.st.SessionUsage(ctx, thread)
		return u.Turns == 2
	})
	u, _ := e.st.SessionUsage(ctx, thread)
	if u.Tokens != 250 || u.CostUSD != 2.5 {
		t.Fatalf("usage must add the difference of running totals: %+v", u)
	}
}

func TestAnswerDoesNotWaitForContext(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.SetContext(ctxInfo(60), nil, 2*time.Second)
	s.Emit(agent.Event{Kind: agent.EventText, Text: "answer"})
	start := time.Now()
	s.Emit(result())
	e.hasMessage(t, thread, "answer\n\n<i>✅ Ход завершён")
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the answer waited %v for the context", d)
	}
	e.hasMessage(t, thread, "answer\n\n<i>✅ Ход завершён · 0s · шагов: 0 · $0.01 · 🧠 60%</i>")
	n := 0
	for _, m := range e.api.Messages(thread) {
		if strings.HasPrefix(m.HTML, "answer") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("🧠 is added by editing the answer, not a new message: %d", n)
	}
}

func TestNewTurnDuringContextQueryStaysRunning(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.SetContext(ctxInfo(60), nil, time.Second)
	s.Emit(result())
	e.hasMessage(t, thread, "Ход завершён")
	_ = e.m.Message(ctx, thread, "next") // while the context query still runs
	testutil.Eventually(t, "second turn", func() bool { return len(s.Sent()) == 2 })
	time.Sleep(1500 * time.Millisecond) // the first turn's context query ends
	e.sessionState(t, thread, store.StateRunning)
}

func TestContextSaysProcessDidNotAnswer(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go")
	s := e.session(t, 0)
	s.SetContext(ctxInfo(55), nil, 0)
	s.Emit(result())
	e.hasMessage(t, thread, "🧠 55%")
	s.SetContext(nil, errors.New("busy"), 0)
	if err := e.m.Context(ctx, thread); err != nil {
		t.Fatal(err)
	}
	e.hasMessage(t, thread, "процесс не ответил")
}

func TestCompactButtonWhenBusy(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "go") // a turn is running
	e.session(t, 0)
	data := compactKeyboard(thread)[0][0].Data
	if alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: data}); alert != "Сжатие в очереди" {
		t.Fatalf("busy: %q", alert)
	}
	if alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: data}); alert != "Сжатие уже в очереди" {
		t.Fatalf("second press: %q", alert)
	}
	e.m.mu.Lock()
	n := len(e.m.sessions[thread].inbox)
	e.m.mu.Unlock()
	if n != 1 {
		t.Fatalf("one /compact queued, got %d", n)
	}
}
