package session

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// LimitObserver receives subscription limit events (see package limits).
type LimitObserver interface {
	Observe(ctx context.Context, thread int, rl agent.RateLimit)
	Windows() []store.RateLimitRow
}

// contextTimeout bounds the context query at the end of a turn.
const contextTimeout = 5 * time.Second

// hintAt is the context percentage from which compaction is suggested: 70,
// or 10 points before auto-compaction when that comes earlier.
func hintAt(c *agent.ContextInfo) int {
	at := 70
	if c.AutoCompact && c.AutoCompactAt > 0 && c.Max > 0 {
		if p := c.AutoCompactAt*100/c.Max - 10; p < at {
			at = p
		}
	}
	return at
}

func compactKeyboard(thread int) telegram.Keyboard {
	return telegram.Keyboard{{{Text: "🗜 Сжать", Data: "cx:" + strconv.Itoa(thread)}}}
}

// recordUsage stores what each model used in the turn. The CLI reports
// running totals of the process, so the turn's usage is the difference from
// the last totals; a total that went down (a /clear) counts in full.
func (m *Manager) recordUsage(ctx context.Context, s *sess, r agent.ResultInfo, now time.Time) {
	m.mu.Lock()
	thread, project := s.row.ThreadID, s.row.Project
	if s.usageBase == nil {
		s.usageBase = map[string]agent.ModelUsage{}
	}
	var turn []agent.ModelUsage
	for _, total := range r.Models {
		d, base := total, s.usageBase[total.Model]
		if total.Input >= base.Input && total.Output >= base.Output && total.CacheRead >= base.CacheRead &&
			total.CacheCreate >= base.CacheCreate && total.CostUSD >= base.CostUSD {
			d.Input, d.Output = total.Input-base.Input, total.Output-base.Output
			d.CacheRead, d.CacheCreate = total.CacheRead-base.CacheRead, total.CacheCreate-base.CacheCreate
			d.CostUSD = total.CostUSD - base.CostUSD
		}
		s.usageBase[total.Model] = total
		if d.Input+d.Output+d.CacheRead+d.CacheCreate > 0 || d.CostUSD > 0 {
			turn = append(turn, d)
		}
	}
	m.mu.Unlock()
	for _, u := range turn {
		row := store.UsageRow{At: now, ThreadID: thread, Project: project, Model: u.Model, Input: u.Input,
			Output: u.Output, CacheRead: u.CacheRead, CacheCreate: u.CacheCreate, CostUSD: u.CostUSD}
		if err := m.d.Store.AddUsage(ctx, row); err != nil {
			slog.Warn("record usage", "thread", thread, "err", err)
		}
	}
}

// snapshotContext asks the live process how full the context is. It returns
// nil when there is no process or no answer within contextTimeout.
func (m *Manager) snapshotContext(ctx context.Context, s *sess) *agent.ContextInfo {
	m.mu.Lock()
	a := s.agent
	m.mu.Unlock()
	if a == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, contextTimeout)
	defer cancel()
	info, err := a.ContextUsage(cctx)
	if err != nil || info == nil || info.Max == 0 {
		slog.Debug("context usage", "thread", s.row.ThreadID, "err", err)
		return nil
	}
	m.mu.Lock()
	s.ctxSnap, s.ctxAt = info, m.d.Now()
	m.mu.Unlock()
	return info
}

// contextHint posts the compaction hint once per crossing of the threshold.
func (m *Manager) contextHint(ctx context.Context, s *sess, info *agent.ContextInfo) {
	pct := int(info.Percent + 0.5)
	over := pct >= hintAt(info)
	m.mu.Lock()
	show := over && !s.hinted
	s.hinted = over
	thread := s.row.ThreadID
	m.mu.Unlock()
	if !show {
		return
	}
	text := fmt.Sprintf("💡 Контекст %d%% — сожми историю, пока агент не сделал это сам", pct)
	if _, err := m.d.API.SendMessage(ctx, thread, text, compactKeyboard(thread), true); err != nil {
		m.telegramFailed(s, "send context hint", err)
	}
}

func contextView(info *agent.ContextInfo) render.ContextView {
	v := render.ContextView{Model: info.Model, Total: info.Total, Max: info.Max, Percent: info.Percent, AutoCompact: info.AutoCompact}
	if info.AutoCompactAt > 0 && info.Max > 0 {
		v.AutoCompactPct = info.AutoCompactAt * 100 / info.Max
	}
	for _, c := range info.Categories {
		v.Categories = append(v.Categories, render.CategoryView{Name: c.Name, Tokens: c.Tokens})
	}
	return v
}

// Context posts how full the session's context is (/context).
func (m *Manager) Context(ctx context.Context, thread int) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	info := m.snapshotContext(ctx, s)
	stale, up := false, false
	var at time.Time
	if info == nil {
		m.mu.Lock()
		info, at, up = s.ctxSnap, s.ctxAt, s.agent != nil
		m.mu.Unlock()
		stale = true
	}
	if info == nil {
		m.say(ctx, s, "🧠 Данных о контексте пока нет — они появятся после первого хода.", true)
		return nil
	}
	v := contextView(info)
	v.Stale, v.ProcessUp, v.At = stale, up, at
	if _, err := m.d.API.SendMessage(ctx, thread, render.ContextText(v), compactKeyboard(thread), true); err != nil {
		m.telegramFailed(s, "send context", err)
	}
	return nil
}

// SessionUsage posts what this session used (/usage in a session topic).
func (m *Manager) SessionUsage(ctx context.Context, thread int) error {
	s := m.lookup(thread)
	if s == nil {
		return ErrUnknownSession
	}
	u, err := m.d.Store.SessionUsage(ctx, thread)
	if err != nil {
		return err
	}
	m.mu.Lock()
	snap := s.ctxSnap
	m.mu.Unlock()
	line := ""
	if snap != nil {
		line = fmt.Sprintf("🧠 %d%% · %s / %s", int(snap.Percent+0.5), render.Tokens(snap.Total), render.Tokens(snap.Max))
	}
	m.say(ctx, s, render.SessionUsageText(u.Turns, u.Tokens, u.CacheRead, u.CostUSD, line), true)
	return nil
}

// NodeUsage posts the subscription limits and the usage of today and the
// last 7 days to thread (/usage in the node topic).
func (m *Manager) NodeUsage(ctx context.Context, thread int) error {
	now := m.d.Now()
	y, mo, d := now.Date()
	today, err := m.d.Store.UsageSince(ctx, time.Date(y, mo, d, 0, 0, 0, 0, now.Location()))
	if err != nil {
		return err
	}
	week, err := m.d.Store.UsageSince(ctx, now.Add(-7*24*time.Hour))
	if err != nil {
		return err
	}
	var limits []render.LimitView
	if m.d.Limits != nil {
		for _, w := range m.d.Limits.Windows() {
			limits = append(limits, render.LimitView{Window: w.Name, Utilization: w.Utilization, ResetsAt: w.ResetsAt})
		}
	}
	_, err = m.d.API.SendMessage(ctx, thread, render.UsageText(limits, totals(today), totals(week), now), nil, true)
	return err
}

func totals(us []store.UsageTotal) []render.ProjectTotal {
	out := make([]render.ProjectTotal, len(us))
	for i, u := range us {
		out[i] = render.ProjectTotal{Project: u.Project, Tokens: u.Tokens, CostUSD: u.CostUSD}
	}
	return out
}

// compacted notes that the CLI compacted the history.
func (m *Manager) compacted(ctx context.Context, s *sess, trigger string, before int) {
	m.mu.Lock()
	s.ctxSnap, s.hinted = nil, false
	m.mu.Unlock()
	how := "вручную"
	if trigger == "auto" {
		how = "авто"
	}
	text := "🗜 История сжата (" + how + ")"
	if before > 0 {
		text += ", было " + render.Tokens(before)
	}
	m.say(ctx, s, text, true)
}

// compact asks the agent to compact its history (the 🗜 button) and returns
// the callback alert. A /compact already waiting is not queued twice.
func (m *Manager) compact(ctx context.Context, thread int) (string, error) {
	m.mu.Lock()
	s := m.sessions[thread]
	busy, queued := false, false
	if s != nil {
		busy = s.inTurn || s.queued
		for _, it := range s.inbox {
			queued = queued || it.text == "/compact"
		}
	}
	m.mu.Unlock()
	switch {
	case s == nil:
		return "", ErrUnknownSession
	case queued:
		return "Сжатие уже в очереди", nil
	}
	if err := m.Message(ctx, thread, "/compact"); err != nil {
		return "", err
	}
	if busy {
		return "Сжатие в очереди", nil
	}
	return "Сжимаю…", nil
}
