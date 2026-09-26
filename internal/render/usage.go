package render

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Tokens formats a token count: 950, 142K, 1.2M.
func Tokens(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1_000_000:
		return fmt.Sprintf("%dK", (n+500)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	}
}

var weekdays = [...]string{"вс", "пн", "вт", "ср", "чт", "пт", "сб"}

// ResetTime says when a limit resets: "в 15:04" today, "пн 15:04" within a
// week, "02.01 15:04" later, in the node's local zone.
func ResetTime(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return "в " + t.Format("15:04")
	case t.Sub(now) < 6*24*time.Hour: // a weekday name must not read as today

		return weekdays[t.Weekday()] + " " + t.Format("15:04")
	default:
		return t.Format("02.01 15:04")
	}
}

var windowNames = map[string]string{
	"five_hour": "5 часов", "seven_day": "7 дней", "seven_day_opus": "7 дней Opus",
	"seven_day_sonnet": "7 дней Sonnet", "overage": "сверх лимита",
}

// WindowName names a subscription limit window.
func WindowName(w string) string {
	if n, ok := windowNames[w]; ok {
		return n
	}
	if w == "" {
		return "подписка"
	}
	return w
}

// LimitLine is one window of the subscription, e.g. "5 часов: 72% · сброс в 14:00".
func LimitLine(window string, utilization float64, resets, now time.Time) string {
	if !resets.IsZero() && !resets.After(now) {
		return Escape(WindowName(window)) + ": сброшен"
	}
	line := Escape(WindowName(window)) + ": нет данных"
	if utilization >= 0 {
		line = fmt.Sprintf("%s: %d%%", Escape(WindowName(window)), int(utilization*100+0.5))
	}
	if !resets.IsZero() {
		line += " · сброс " + ResetTime(resets, now)
	}
	return line
}

// CategoryView is one part of the context window.
type CategoryView struct {
	Name   string
	Tokens int
}

// ContextView is what /context shows.
type ContextView struct {
	Model          string
	Total, Max     int
	Percent        float64
	Categories     []CategoryView
	AutoCompact    bool
	AutoCompactPct int  // 0 when unknown
	Stale          bool // the data is from the end of the last turn
	ProcessUp      bool // with Stale: the process runs but did not answer
	At             time.Time
}

var categoryNames = map[string]string{
	"messages": "сообщения", "system prompt": "системный промпт", "system tools": "инструменты",
	"mcp tools": "MCP", "memory files": "память", "custom agents": "агенты", "skills": "skills",
}

// hiddenCategories are not content.
var hiddenCategories = map[string]bool{"free space": true, "autocompact buffer": true}

// ContextText renders /context.
func ContextText(c ContextView) string {
	head := "🧠 <b>Контекст</b>"
	if c.Model != "" {
		head += " · " + Escape(c.Model)
	}
	head += fmt.Sprintf(" · %s / %s (%d%%)", Tokens(c.Total), Tokens(c.Max), int(c.Percent+0.5))
	lines := []string{head}
	cats := append([]CategoryView(nil), c.Categories...)
	sort.SliceStable(cats, func(i, j int) bool { return cats[i].Tokens > cats[j].Tokens })
	var parts []string
	for _, cat := range cats {
		key := strings.ToLower(cat.Name)
		if hiddenCategories[key] || cat.Tokens == 0 {
			continue
		}
		name := cat.Name
		if n, ok := categoryNames[key]; ok {
			name = n
		}
		parts = append(parts, Escape(name)+" "+Tokens(cat.Tokens))
		if len(parts) == 5 {
			break
		}
	}
	if len(parts) > 0 {
		lines = append(lines, strings.Join(parts, " · "))
	}
	auto := "Авто-сжатие: выкл"
	if c.AutoCompact {
		auto = "Авто-сжатие: вкл"
		if c.AutoCompactPct > 0 {
			auto += fmt.Sprintf(", при %d%%", c.AutoCompactPct)
		}
	}
	lines = append(lines, auto)
	if c.Stale {
		why := "процесс сейчас не запущен"
		if c.ProcessUp {
			why = "процесс не ответил"
		}
		lines = append(lines, "<i>(на конец хода "+c.At.Local().Format("15:04")+", "+why+")</i>")
	}
	return strings.Join(lines, "\n")
}

// ProjectTotal is one project's usage in a period.
type ProjectTotal struct {
	Project string
	Tokens  int
	CostUSD float64
}

// LimitView is one subscription window for /usage.
type LimitView struct {
	Window      string
	Utilization float64
	ResetsAt    time.Time
}

// UsageText renders /usage for the node.
func UsageText(limits []LimitView, today, week []ProjectTotal, now time.Time) string {
	var lines []string
	if len(limits) == 0 {
		lines = append(lines, "📊 Подписка: данных нет — они приходят вместе с ходами.")
	} else {
		lines = append(lines, "📊 <b>Подписка</b>")
		for _, l := range limits {
			lines = append(lines, LimitLine(l.Window, l.Utilization, l.ResetsAt, now))
		}
	}
	lines = append(lines, "", period("Сегодня", today), "", period("7 дней", week), "",
		"<i>≈ — оценка CLI по ценам API; на подписке деньги не списываются.</i>")
	return strings.Join(lines, "\n")
}

// period renders the total of a period and its five largest projects.
func period(title string, ps []ProjectTotal) string {
	tokens, cost := 0, 0.0
	for _, p := range ps {
		tokens, cost = tokens+p.Tokens, cost+p.CostUSD
	}
	lines := []string{fmt.Sprintf("<b>%s</b>: %s ток. · ≈$%.2f", title, Tokens(tokens), cost)}
	for i, p := range ps {
		if i == 5 {
			rest, restCost := 0, 0.0
			for _, q := range ps[5:] {
				rest, restCost = rest+q.Tokens, restCost+q.CostUSD
			}
			lines = append(lines, fmt.Sprintf("  прочие %s · ≈$%.2f", Tokens(rest), restCost))
			break
		}
		lines = append(lines, fmt.Sprintf("  %s %s · ≈$%.2f", Escape(p.Project), Tokens(p.Tokens), p.CostUSD))
	}
	return strings.Join(lines, "\n")
}

// SessionUsageText renders /usage in a session topic.
func SessionUsageText(turns, tokens, cacheRead int, costUSD float64, contextLine string) string {
	text := fmt.Sprintf("📊 <b>Эта сессия</b>: ходов %d · %s ток.", turns, Tokens(tokens))
	if cacheRead > 0 {
		text += " (+" + Tokens(cacheRead) + " из кеша)"
	}
	text += fmt.Sprintf(" · ≈$%.2f", costUSD)
	if contextLine != "" {
		text += "\n" + contextLine
	}
	return text
}
