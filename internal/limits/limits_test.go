package limits

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

const control, session = 3, 101

func setup(t *testing.T) (*Tracker, *telegram.Fake, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := telegram.NewFake()
	tr := New(api, st, func() int { return control })
	tr.Now = func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local) }
	return tr, api, st
}

func count(api *telegram.Fake, thread int, substr string) int {
	n := 0
	for _, m := range api.Messages(thread) {
		if strings.Contains(m.HTML, substr) {
			n++
		}
	}
	return n
}

func TestWarningOncePerReset(t *testing.T) {
	tr, api, _ := setup(t)
	ctx := context.Background()
	resets := time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local)
	warn := agent.RateLimit{Window: "five_hour", Status: "allowed_warning", Utilization: 0.85, ResetsAt: resets}
	tr.Observe(ctx, session, warn)
	tr.Observe(ctx, session, warn)
	want := "⚠️ Лимит подписки «5 часов»: 85% · сброс в 14:00"
	if count(api, session, want) != 1 || count(api, control, want) != 1 {
		t.Fatalf("warning must be sent once to the session and the node topic")
	}
	for _, m := range api.Messages(session) {
		if !m.Silent {
			t.Fatal("a warning is silent")
		}
	}
	warn.ResetsAt = resets.Add(5 * time.Hour)
	tr.Observe(ctx, session, warn)
	if count(api, control, "⚠️ Лимит подписки «5 часов»") != 2 {
		t.Fatal("a new reset time warns again")
	}
}

func TestRejectedAndRecovery(t *testing.T) {
	tr, api, _ := setup(t)
	ctx := context.Background()
	resets := time.Date(2026, 9, 28, 9, 0, 0, 0, time.Local)
	tr.Observe(ctx, session, agent.RateLimit{Window: "seven_day", Status: "rejected", Utilization: 1, ResetsAt: resets})
	want := "⛔ Лимит подписки «7 дней» исчерпан · сброс пн 09:00"
	if count(api, session, want) != 1 || count(api, control, want) != 1 {
		t.Fatal("rejected goes to the session and the node topic")
	}
	for _, m := range api.Messages(control) {
		if strings.Contains(m.HTML, "⛔") && m.Silent {
			t.Fatal("rejected comes with sound")
		}
	}
	tr.Observe(ctx, session, agent.RateLimit{Window: "seven_day", Status: "allowed", Utilization: 0.1, ResetsAt: resets.Add(7 * 24 * time.Hour)})
	if count(api, control, "✅ Лимит «7 дней» снова доступен") != 1 || count(api, session, "✅") != 0 {
		t.Fatal("recovery is announced in the node topic only")
	}
	tr.Observe(ctx, session, agent.RateLimit{Window: "seven_day", Status: "allowed", Utilization: 0.2})
	if count(api, control, "✅") != 1 {
		t.Fatal("allowed after allowed says nothing")
	}
}

func TestTrackerLoadSuppressesRepeat(t *testing.T) {
	tr, api, st := setup(t)
	ctx := context.Background()
	resets := time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local)
	warn := agent.RateLimit{Window: "five_hour", Status: "allowed_warning", Utilization: 0.85, ResetsAt: resets}
	tr.Observe(ctx, session, warn)
	restarted := New(api, st, func() int { return control })
	restarted.Now = tr.Now
	if err := restarted.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if w := restarted.Windows(); len(w) != 1 || w[0].Name != "five_hour" || w[0].Utilization != 0.85 {
		t.Fatalf("windows after load: %+v", w)
	}
	restarted.Observe(ctx, session, warn)
	if count(api, control, "⚠️") != 1 {
		t.Fatal("a warning sent before the restart is not repeated")
	}
}

func TestUnnamedAllowedClearsWindows(t *testing.T) {
	tr, api, _ := setup(t)
	ctx := context.Background()
	resets := time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local)
	tr.Observe(ctx, session, agent.RateLimit{Window: "five_hour", Status: "rejected", Utilization: 1, ResetsAt: resets})
	tr.Observe(ctx, session, agent.RateLimit{Status: "allowed", Utilization: -1})
	if count(api, control, "✅ Лимит «5 часов» снова доступен") != 1 {
		t.Fatal("an unnamed allowed event clears the rejected window")
	}
	for _, w := range tr.Windows() {
		if w.Name == "" || w.Status != "allowed" {
			t.Fatalf("windows: %+v", tr.Windows())
		}
	}
}

func TestUnknownUtilization(t *testing.T) {
	tr, api, _ := setup(t)
	resets := time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local)
	tr.Observe(context.Background(), session, agent.RateLimit{Window: "five_hour", Status: "allowed_warning", Utilization: -1, ResetsAt: resets})
	if count(api, control, "⚠️ Лимит подписки «5 часов» почти исчерпан · сброс в 14:00") != 1 || count(api, control, "0%") != 0 {
		t.Fatalf("unknown utilization: %+v", api.Messages(control))
	}
}

// failingAPI fails sends while fail is set.
type failingAPI struct {
	*telegram.Fake
	fail bool
}

func (f *failingAPI) SendMessage(ctx context.Context, thread int, html string, kb telegram.Keyboard, silent bool) (int, error) {
	if f.fail {
		return 0, telegram.ErrTopicGone
	}
	return f.Fake.SendMessage(ctx, thread, html, kb, silent)
}

func TestFailedNoticeIsRetried(t *testing.T) {
	_, fake, st := setup(t)
	api := &failingAPI{Fake: fake, fail: true}
	tr := New(api, st, func() int { return control })
	tr.Now = func() time.Time { return time.Date(2026, 9, 25, 10, 0, 0, 0, time.Local) }
	warn := agent.RateLimit{Window: "five_hour", Status: "allowed_warning", Utilization: 0.9, ResetsAt: time.Date(2026, 9, 25, 14, 0, 0, 0, time.Local)}
	tr.Observe(context.Background(), session, warn)
	api.fail = false
	tr.Observe(context.Background(), session, warn)
	if count(fake, control, "⚠️") != 1 {
		t.Fatal("a notice that failed to send is sent with the next event")
	}
}
