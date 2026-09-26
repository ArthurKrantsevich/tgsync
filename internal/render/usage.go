package render

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
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

// ResetTime says when a limit resets: "at 15:04" today, "Mon 15:04" within a
// week, "02.01 15:04" later, in the node's local zone.
func ResetTime(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return i18n.T("render.reset.today", t.Format("15:04"))
	case t.Sub(now) < 6*24*time.Hour: // a weekday name must not read as today

		return i18n.T("render.weekday."+strconv.Itoa(int(t.Weekday()))) + " " + t.Format("15:04")
	default:
		return t.Format("02.01 15:04")
	}
}

// knownWindows are the limit windows with a catalog name (render.window.<name>).
var knownWindows = map[string]bool{
	"five_hour": true, "seven_day": true, "seven_day_opus": true, "seven_day_sonnet": true, "overage": true,
}

// WindowName names a subscription limit window.
func WindowName(w string) string {
	if knownWindows[w] {
		return i18n.T("render.window." + w)
	}
	if w == "" {
		return i18n.T("render.window.subscription")
	}
	return w
}

// LimitLine is one window of the subscription, e.g. "5 hours: 72% · resets at 14:00".
func LimitLine(window string, utilization float64, resets, now time.Time) string {
	if !resets.IsZero() && !resets.After(now) {
		return i18n.T("render.limit.reset", Escape(WindowName(window)))
	}
	line := i18n.T("render.limit.nodata", Escape(WindowName(window)))
	if utilization >= 0 {
		line = fmt.Sprintf("%s: %d%%", Escape(WindowName(window)), int(utilization*100+0.5))
	}
	if !resets.IsZero() {
		line += i18n.T("render.limit.resets", ResetTime(resets, now))
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

// categoryKeys holds the catalog keys of the context categories the CLI reports.
var categoryKeys = map[string]string{
	"messages": "render.ctx.cat.messages", "system prompt": "render.ctx.cat.system_prompt",
	"system tools": "render.ctx.cat.system_tools", "mcp tools": "render.ctx.cat.mcp_tools",
	"memory files": "render.ctx.cat.memory_files", "custom agents": "render.ctx.cat.custom_agents",
	"skills": "render.ctx.cat.skills",
}

// hiddenCategories are not content.
var hiddenCategories = map[string]bool{"free space": true, "autocompact buffer": true}

// ContextText renders /context.
func ContextText(c ContextView) string {
	head := i18n.T("render.ctx.head")
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
		if k, ok := categoryKeys[key]; ok {
			name = i18n.T(k)
		}
		parts = append(parts, Escape(name)+" "+Tokens(cat.Tokens))
		if len(parts) == 5 {
			break
		}
	}
	if len(parts) > 0 {
		lines = append(lines, strings.Join(parts, " · "))
	}
	auto := i18n.T("render.ctx.auto_off")
	if c.AutoCompact {
		auto = i18n.T("render.ctx.auto_on")
		if c.AutoCompactPct > 0 {
			auto += i18n.T("render.ctx.auto_at", c.AutoCompactPct)
		}
	}
	lines = append(lines, auto)
	if c.Stale {
		why := i18n.T("render.ctx.stale_down")
		if c.ProcessUp {
			why = i18n.T("render.ctx.stale_silent")
		}
		lines = append(lines, i18n.T("render.ctx.stale", c.At.Local().Format("15:04"), why))
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
		lines = append(lines, i18n.T("render.usage.nodata"))
	} else {
		lines = append(lines, i18n.T("render.usage.head"))
		for _, l := range limits {
			lines = append(lines, LimitLine(l.Window, l.Utilization, l.ResetsAt, now))
		}
	}
	lines = append(lines, "", period(i18n.T("render.usage.today"), today), "", period(i18n.T("render.usage.week"), week), "",
		i18n.T("render.usage.footnote"))
	return strings.Join(lines, "\n")
}

// period renders the total of a period and its five largest projects.
func period(title string, ps []ProjectTotal) string {
	tokens, cost := 0, 0.0
	for _, p := range ps {
		tokens, cost = tokens+p.Tokens, cost+p.CostUSD
	}
	lines := []string{i18n.T("render.usage.period", title, Tokens(tokens), cost)}
	for i, p := range ps {
		if i == 5 {
			rest, restCost := 0, 0.0
			for _, q := range ps[5:] {
				rest, restCost = rest+q.Tokens, restCost+q.CostUSD
			}
			lines = append(lines, i18n.T("render.usage.others", Tokens(rest), restCost))
			break
		}
		lines = append(lines, fmt.Sprintf("  %s %s · ≈$%.2f", Escape(p.Project), Tokens(p.Tokens), p.CostUSD))
	}
	return strings.Join(lines, "\n")
}

// SessionUsageText renders /usage in a session topic.
func SessionUsageText(turns, tokens, cacheRead int, costUSD float64, contextLine string) string {
	text := i18n.N("render.usage.n.session", turns, turns, Tokens(tokens))
	if cacheRead > 0 {
		text += i18n.T("render.usage.cache", Tokens(cacheRead))
	}
	text += fmt.Sprintf(" · ≈$%.2f", costUSD)
	if contextLine != "" {
		text += "\n" + contextLine
	}
	return text
}
