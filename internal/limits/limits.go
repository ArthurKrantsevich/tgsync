// Package limits watches the Claude subscription limits reported by the CLI
// and tells the user when a window gets close to its limit or runs out.
package limits

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// Tracker keeps the node-wide state of every limit window.
type Tracker struct {
	Now func() time.Time

	api     telegram.API
	st      *store.Store
	control func() int

	mu       sync.Mutex
	windows  map[string]store.RateLimitRow
	notified map[string]string // window → status|reset of the last notice
}

func New(api telegram.API, st *store.Store, control func() int) *Tracker {
	return &Tracker{Now: time.Now, api: api, st: st, control: control,
		windows: map[string]store.RateLimitRow{}, notified: map[string]string{}}
}

func noticeKey(status string, resets time.Time) string {
	return status + "|" + strconv.FormatInt(resets.Unix(), 10)
}

// Load restores the windows saved before a restart, so notices already sent
// are not sent again.
func (t *Tracker) Load(ctx context.Context) error {
	rows, err := t.st.RateLimits(ctx)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, r := range rows {
		t.windows[r.Name] = r
		if r.Status == "allowed_warning" || r.Status == "rejected" {
			t.notified[r.Name] = noticeKey(r.Status, r.ResetsAt)
		}
	}
	return nil
}

// Windows returns the known windows by name.
func (t *Tracker) Windows() []store.RateLimitRow {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]store.RateLimitRow, 0, len(t.windows))
	for _, w := range t.windows {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Observe applies a limit event from the session in thread.
func (t *Tracker) Observe(ctx context.Context, thread int, rl agent.RateLimit) {
	if rl.Window == "" {
		t.observeUnnamed(ctx, thread, rl)
		return
	}
	now := t.Now()
	row := store.RateLimitRow{Name: rl.Window, Status: rl.Status, Utilization: rl.Utilization, ResetsAt: rl.ResetsAt, UpdatedAt: now}
	name := render.Escape(render.WindowName(rl.Window))
	t.mu.Lock()
	prev := t.windows[rl.Window]
	t.windows[rl.Window] = row
	key := noticeKey(rl.Status, rl.ResetsAt)
	var text string
	silent, toSession := true, true
	switch rl.Status {
	case "allowed_warning":
		if t.notified[rl.Window] != key {
			text = fmt.Sprintf("⚠️ Лимит подписки «%s» почти исчерпан", name)
			if rl.Utilization >= 0 {
				text = fmt.Sprintf("⚠️ Лимит подписки «%s»: %d%%", name, int(rl.Utilization*100+0.5))
			}
			if !rl.ResetsAt.IsZero() {
				text += " · сброс " + render.ResetTime(rl.ResetsAt, now)
			}
			t.notified[rl.Window] = key
		}
	case "rejected":
		if t.notified[rl.Window] != key {
			text = fmt.Sprintf("⛔ Лимит подписки «%s» исчерпан", name)
			if !rl.ResetsAt.IsZero() {
				text += " · сброс " + render.ResetTime(rl.ResetsAt, now)
			}
			silent = false
			t.notified[rl.Window] = key
		}
	case "allowed":
		if prev.Status == "rejected" {
			text = fmt.Sprintf("✅ Лимит «%s» снова доступен", name)
			toSession = false
		}
		delete(t.notified, rl.Window)
	}
	t.mu.Unlock()
	if err := t.st.SaveRateLimit(ctx, row); err != nil {
		slog.Warn("save rate limit", "err", err)
	}
	if text == "" {
		return
	}
	control := t.control()
	if toSession && thread != 0 && thread != control {
		t.send(ctx, thread, text, silent)
	}
	if !t.send(ctx, control, text, silent) {
		// Not delivered to the node topic: let the next event try again.
		t.mu.Lock()
		if t.notified[rl.Window] == key {
			delete(t.notified, rl.Window)
		}
		t.mu.Unlock()
	}
}

func (t *Tracker) send(ctx context.Context, thread int, text string, silent bool) bool {
	if _, err := t.api.SendMessage(ctx, thread, text, nil, silent); err != nil {
		slog.Warn("send limit notice", "thread", thread, "err", err)
		return false
	}
	return true
}

// observeUnnamed handles an event without a window name. The CLI names the
// window only when it knows it: an unnamed "allowed" clears every window
// that was limited; an unnamed warning or rejection is shown but not stored.
func (t *Tracker) observeUnnamed(ctx context.Context, thread int, rl agent.RateLimit) {
	now := t.Now()
	if rl.Status != "allowed" {
		t.mu.Lock()
		key := noticeKey(rl.Status, rl.ResetsAt)
		repeat := t.notified[""] == key
		t.notified[""] = key
		t.mu.Unlock()
		if repeat {
			return
		}
		text, silent := "⚠️ Лимит подписки почти исчерпан", true
		if rl.Status == "rejected" {
			text, silent = "⛔ Лимит подписки исчерпан", false
		}
		if !rl.ResetsAt.IsZero() {
			text += " · сброс " + render.ResetTime(rl.ResetsAt, now)
		}
		control := t.control()
		if thread != 0 && thread != control {
			t.send(ctx, thread, text, silent)
		}
		t.send(ctx, control, text, silent)
		return
	}
	var cleared []store.RateLimitRow
	var recovered []string
	t.mu.Lock()
	delete(t.notified, "")
	for name, w := range t.windows {
		if w.Status != "rejected" && w.Status != "allowed_warning" {
			continue
		}
		if w.Status == "rejected" {
			recovered = append(recovered, name)
		}
		w.Status, w.UpdatedAt = "allowed", now
		t.windows[name] = w
		delete(t.notified, name)
		cleared = append(cleared, w)
	}
	t.mu.Unlock()
	for _, w := range cleared {
		if err := t.st.SaveRateLimit(ctx, w); err != nil {
			slog.Warn("save rate limit", "err", err)
		}
	}
	sort.Strings(recovered)
	for _, name := range recovered {
		t.send(ctx, t.control(), fmt.Sprintf("✅ Лимит «%s» снова доступен", render.Escape(render.WindowName(name))), true)
	}
}
