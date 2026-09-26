package render

import (
	"strings"
	"testing"
	"time"
)

func TestAgentsList(t *testing.T) {
	text := AgentsList([]AgentEntry{
		{Name: "Explore", Description: "ищет <sudo>", State: "running"},
		{Name: "go-reviewer", Description: "ревью", State: "running", Depth: 1},
		{Name: "Plan", Description: "план", State: "completed"},
	}, Hidden{Done: 3})
	want := "🤖 <b>Агенты</b> · 2 работают · 4 готово\n" +
		"▶ <b>Explore</b> — ищет &lt;sudo&gt;\n" +
		"   ↳ ▶ <b>go-reviewer</b> — ревью\n" +
		"✅ <b>Plan</b> — план\n" +
		"… ещё 3 готовых"
	if text != want {
		t.Fatalf("list:\n%s\nwant:\n%s", text, want)
	}
	one := AgentsList([]AgentEntry{{Name: "A", State: "running"}, {Name: "B", State: "stopped"}}, Hidden{Running: 2})
	for _, want := range []string{"🤖 <b>Агенты</b> · 3 работают · 1 готов\n", "⏹ <b>B</b>", "… ещё 2 работают"} {
		if !strings.Contains(one, want) {
			t.Errorf("list lacks %q:\n%s", want, one)
		}
	}
}

func TestAgentCard(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	running := AgentCard(AgentCardInfo{Name: "go-reviewer", Description: "ревью", State: "running",
		Started: now.Add(-72 * time.Second), ToolUses: 14, Action: "📖 Read a.go"}, now)
	for _, want := range []string{"▶ <b>go-reviewer</b> — ревью", "Вызвал: основной агент", "Работает 1m12s · 14 инстр.", "Сейчас: 📖 Read a.go"} {
		if !strings.Contains(running, want) {
			t.Errorf("running card lacks %q:\n%s", want, running)
		}
	}
	done := AgentCard(AgentCardInfo{Name: "Plan", State: "completed", Caller: "Explore",
		Took: 123 * time.Second, ToolUses: 3, Summary: strings.Repeat("я", 1500)}, now)
	for _, want := range []string{"✅ <b>Plan</b>", "Вызвал: Explore", "Готово за 2m03s · 3 инстр.", "<i>"} {
		if !strings.Contains(done, want) {
			t.Errorf("finished card lacks %q:\n%s", want, done)
		}
	}
	if n := strings.Count(done, "я"); n > 1000 {
		t.Fatalf("summary must be cut to 1000, got %d", n)
	}
	if strings.Contains(done, "Сейчас:") {
		t.Fatal("finished card has no current action")
	}
}

func TestAgentsListHiddenPlural(t *testing.T) {
	for _, c := range []struct {
		h    Hidden
		want []string
	}{
		{Hidden{Running: 1, Done: 1}, []string{"… ещё 1 работает", "… ещё 1 готовый"}},
		{Hidden{Running: 2, Done: 11}, []string{"… ещё 2 работают", "… ещё 11 готовых"}},
		{Hidden{Done: 21}, []string{"… ещё 21 готовый"}},
	} {
		text := AgentsList(nil, c.h)
		for _, w := range c.want {
			if !strings.Contains(text, w) {
				t.Errorf("%+v: lacks %q:\n%s", c.h, w, text)
			}
		}
	}
}
