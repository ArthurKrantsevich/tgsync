package render

import (
	"strings"
	"testing"
	"time"
)

func TestTokens(t *testing.T) {
	for n, want := range map[int]string{950: "950", 142300: "142K", 1234567: "1.2M", 0: "0"} {
		if got := Tokens(n); got != want {
			t.Errorf("Tokens(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestResetTime(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local) // Friday
	cases := map[time.Time]string{
		time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local): "в 14:00",
		time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local):  "пн 09:00",
		time.Date(2026, 10, 9, 9, 30, 0, 0, time.Local): "09.10 09:30",
	}
	for at, want := range cases {
		if got := ResetTime(at, now); got != want {
			t.Errorf("ResetTime(%v) = %q, want %q", at, got, want)
		}
	}
	if got := LimitLine("five_hour", 0.724, time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local), now); got != "5 часов: 72% · сброс в 14:00" {
		t.Errorf("LimitLine = %q", got)
	}
	if WindowName("weird") != "weird" {
		t.Error("unknown window keeps its name")
	}
}

func TestContextText(t *testing.T) {
	c := ContextView{Model: "claude-opus-4", Total: 142000, Max: 200000, Percent: 71, AutoCompact: true, AutoCompactPct: 92,
		Categories: []CategoryView{{"System prompt", 12000}, {"Messages", 98000}, {"Free space", 50000}, {"MCP tools", 7000},
			{"System tools", 21000}, {"Memory files", 4000}, {"Custom agents", 1000}}}
	got := ContextText(c)
	want := "🧠 <b>Контекст</b> · claude-opus-4 · 142K / 200K (71%)\n" +
		"сообщения 98K · инструменты 21K · системный промпт 12K · MCP 7K · память 4K\n" +
		"Авто-сжатие: вкл, при 92%"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	c.Stale, c.At, c.AutoCompact = true, time.Date(2026, 9, 25, 14, 5, 0, 0, time.Local), false
	got = ContextText(c)
	if !strings.Contains(got, "Авто-сжатие: выкл") || !strings.Contains(got, "(на конец хода 14:05, процесс сейчас не запущен)") {
		t.Fatalf("stale:\n%s", got)
	}
}

func TestUsageText(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	text := UsageText([]LimitView{{Window: "five_hour", Utilization: 0.72, ResetsAt: time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local)}},
		[]ProjectTotal{{"tg", 820000, 2.9}, {"my<app>", 380000, 1.2}},
		[]ProjectTotal{{"tg", 6000000, 20}, {"a", 1, 0}, {"b", 1, 0}, {"c", 1, 0}, {"d", 1, 0}, {"e", 1, 0}, {"f", 2, 0.5}}, now)
	for _, want := range []string{
		"📊 <b>Подписка</b>\n5 часов: 72% · сброс в 14:00",
		"<b>Сегодня</b>: 1.2M ток. · ≈$4.10\n  tg 820K · ≈$2.90\n  my&lt;app&gt; 380K · ≈$1.20",
		"<b>7 дней</b>: 6.0M ток. · ≈$20.50",
		"  прочие 3 · ≈$0.50",
		"≈ — оценка CLI по ценам API; на подписке деньги не списываются.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("usage lacks %q:\n%s", want, text)
		}
	}
	if none := UsageText(nil, nil, nil, now); !strings.Contains(none, "Подписка: данных нет") || !strings.Contains(none, "<b>Сегодня</b>: 0 ток.") {
		t.Errorf("empty usage:\n%s", none)
	}
}

func TestSessionUsageText(t *testing.T) {
	got := SessionUsageText(3, 150000, 2000000, 1.234, "🧠 71% · 142K / 200K")
	want := "📊 <b>Эта сессия</b>: ходов 3 · 150K ток. (+2.0M из кеша) · ≈$1.23\n🧠 71% · 142K / 200K"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLimitLineUnknownUtilization(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local)
	if got := LimitLine("five_hour", -1, time.Time{}, now); got != "5 часов: нет данных" {
		t.Fatalf("LimitLine = %q", got)
	}
}

func TestResetPassedAndNextWeekSameWeekday(t *testing.T) {
	now := time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local) // Friday
	if got := LimitLine("five_hour", 0.95, now.Add(-time.Hour), now); got != "5 часов: сброшен" {
		t.Errorf("passed reset: %q", got)
	}
	if got := ResetTime(time.Date(2026, 10, 2, 9, 0, 0, 0, time.Local), now); got != "02.10 09:00" {
		t.Errorf("next Friday must show the date, got %q", got)
	}
	if got := ResetTime(time.Date(2026, 9, 30, 9, 0, 0, 0, time.Local), now); got != "ср 09:00" {
		t.Errorf("within the week: %q", got)
	}
}

func TestContextTextProcessDidNotAnswer(t *testing.T) {
	c := ContextView{Total: 1000, Max: 2000, Percent: 50, Stale: true, ProcessUp: true, At: time.Date(2026, 9, 25, 14, 5, 0, 0, time.Local)}
	if got := ContextText(c); !strings.Contains(got, "(на конец хода 14:05, процесс не ответил)") {
		t.Fatalf("got:\n%s", got)
	}
}
