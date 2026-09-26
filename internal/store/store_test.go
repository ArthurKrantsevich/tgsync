package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "sub", "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func newSession(t *testing.T, s *Store, thread int, state string, age time.Duration) {
	t.Helper()
	ctx := context.Background()
	r := &SessionRow{ThreadID: thread, Project: "p", Cwd: "/w/p", Title: fmt.Sprint("t", thread), State: state}
	if err := s.CreateSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`,
		time.Now().Add(-age).Unix(), r.ID); err != nil {
		t.Fatal(err)
	}
}

func TestControlTopic(t *testing.T) {
	s, ctx := open(t), context.Background()
	id, err := s.ControlTopic(ctx)
	if err != nil || id != 0 {
		t.Fatalf("empty store: id=%d err=%v", id, err)
	}
	if err := s.SetControlTopic(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.SetControlTopic(ctx, 43); err != nil {
		t.Fatal(err)
	}
	if id, _ := s.ControlTopic(ctx); id != 43 {
		t.Fatalf("want 43, got %d", id)
	}
}

func TestSessions(t *testing.T) {
	s, ctx := open(t), context.Background()
	a := SessionRow{ThreadID: 10, Project: "demo", Cwd: "/w/demo", Title: "fix", State: StateIdle}
	if err := s.CreateSession(ctx, &a); err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.Mode != "default" || a.CreatedAt.IsZero() {
		t.Fatalf("defaults not set: %+v", a)
	}
	b := SessionRow{ThreadID: 11, Project: "demo", Cwd: "/w/demo", Title: "other", State: StateIdle}
	if err := s.CreateSession(ctx, &b); err != nil {
		t.Fatal(err)
	}
	a.ClaudeSessionID, a.State, a.Mode, a.Title = "sid", StateRunning, "plan", "fix bug"
	if err := s.UpdateSession(ctx, &a); err != nil {
		t.Fatal(err)
	}
	b.State = StateClosed
	if err := s.UpdateSession(ctx, &b); err != nil {
		t.Fatal(err)
	}
	rows, err := s.OpenSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 open session, got %d", len(rows))
	}
	got := rows[0]
	if got.ThreadID != 10 || got.ClaudeSessionID != "sid" || got.State != StateRunning || got.Mode != "plan" || got.Title != "fix bug" {
		t.Fatalf("row: %+v", got)
	}
}

func TestDuplicateThreadRejected(t *testing.T) {
	s, ctx := open(t), context.Background()
	r := SessionRow{ThreadID: 10, Project: "p", Cwd: "/p", Title: "t", State: StateIdle}
	if err := s.CreateSession(ctx, &r); err != nil {
		t.Fatal(err)
	}
	dup := r
	if err := s.CreateSession(ctx, &dup); err == nil {
		t.Fatal("thread_id must be unique")
	}
}

func TestRules(t *testing.T) {
	s, ctx := open(t), context.Background()
	for i := 0; i < 2; i++ {
		if err := s.AddRule(ctx, "demo", Rule{Tool: "Bash", Pattern: "go test"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddRule(ctx, "other", Rule{Tool: "WebFetch"}); err != nil {
		t.Fatal(err)
	}
	rules, err := s.Rules(ctx, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0] != (Rule{Tool: "Bash", Pattern: "go test"}) {
		t.Fatalf("rules: %+v", rules)
	}
}

func TestProfileColumnAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, _ := Open(path)
	r := SessionRow{ThreadID: 1, Project: "p", Cwd: "/p", Title: "t", State: StateIdle, Profile: "lean"}
	if err := s.CreateSession(context.Background(), &r); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen must not fail on an existing column: %v", err)
	}
	defer s2.Close()
	rows, _ := s2.OpenSessions(context.Background())
	if len(rows) != 1 || rows[0].Profile != "lean" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestUsageTotals(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	now := time.Now()
	rows := []UsageRow{
		{At: now.Add(-time.Hour), ThreadID: 1, Project: "a", Model: "m", Input: 100, Output: 50, CacheRead: 1000, CacheCreate: 10, CostUSD: 1},
		{At: now.Add(-time.Hour), ThreadID: 1, Project: "a", Model: "n", Input: 1, CostUSD: 0.5},
		{At: now.Add(-30 * time.Minute), ThreadID: 2, Project: "b", Model: "m", Input: 500, CostUSD: 2},
		{At: now.Add(-72 * time.Hour), ThreadID: 1, Project: "a", Model: "m", Input: 7, CostUSD: 3},
	}
	for _, r := range rows {
		if err := st.AddUsage(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	day, err := st.UsageSince(ctx, now.Add(-2*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(day) != 2 || day[0].Project != "b" || day[0].Tokens != 500 || day[1].Tokens != 161 || day[1].CostUSD != 1.5 {
		t.Fatalf("day: %+v", day)
	}
	week, _ := st.UsageSince(ctx, now.Add(-7*24*time.Hour))
	if len(week) != 2 || week[1].Project != "a" || week[1].Tokens != 168 {
		t.Fatalf("week: %+v", week)
	}
	s1, err := st.SessionUsage(ctx, 1)
	if err != nil || s1.Turns != 2 || s1.Tokens != 168 || s1.CacheRead != 1000 || s1.CostUSD != 4.5 {
		t.Fatalf("session: %+v %v", s1, err)
	}
}

func TestRateLimitsRoundTrip(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	resets := time.Unix(1790300000, 0)
	_ = st.SaveRateLimit(ctx, RateLimitRow{Name: "seven_day", Status: "allowed", Utilization: 0.4, ResetsAt: resets, UpdatedAt: resets})
	_ = st.SaveRateLimit(ctx, RateLimitRow{Name: "five_hour", Status: "allowed", Utilization: 0.1, ResetsAt: resets, UpdatedAt: resets})
	_ = st.SaveRateLimit(ctx, RateLimitRow{Name: "five_hour", Status: "allowed_warning", Utilization: 0.85, ResetsAt: resets, UpdatedAt: resets})
	got, err := st.RateLimits(ctx)
	if err != nil || len(got) != 2 || got[0].Name != "five_hour" || got[0].Status != "allowed_warning" ||
		got[0].Utilization != 0.85 || !got[0].ResetsAt.Equal(resets) {
		t.Fatalf("limits: %+v %v", got, err)
	}
}

func TestCleanable(t *testing.T) {
	s, ctx := open(t), context.Background()
	newSession(t, s, 1, StateClosed, 10*24*time.Hour)  // old closed: yes
	newSession(t, s, 2, StateClosed, time.Hour)        // fresh closed: no
	newSession(t, s, 3, StateFailed, time.Hour)        // failed, no usage: yes
	newSession(t, s, 4, StateFailed, time.Hour)        // failed with usage: no
	newSession(t, s, 5, StateRunning, 30*24*time.Hour) // open: no
	newSession(t, s, 6, StateClosed, 20*24*time.Hour)  // already deleted: no
	if err := s.AddUsage(ctx, UsageRow{At: time.Now(), ThreadID: 4, Project: "p", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTopicDeleted(ctx, 6); err != nil {
		t.Fatal(err)
	}
	rows, err := s.Cleanable(ctx, time.Now().Add(-7*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, r := range rows {
		got = append(got, r.ThreadID)
	}
	if fmt.Sprint(got) != "[1 3]" {
		t.Fatalf("cleanable: %v", got)
	}
}

func TestSessionCounts(t *testing.T) {
	s, ctx := open(t), context.Background()
	newSession(t, s, 1, StateRunning, 0)
	newSession(t, s, 2, StateFailed, 0)
	newSession(t, s, 3, StateClosed, 0)
	newSession(t, s, 4, StateClosed, 0)
	_ = s.MarkTopicDeleted(ctx, 4)
	open, closed, err := s.SessionCounts(ctx)
	if err != nil || open != 2 || closed != 1 {
		t.Fatalf("open=%d closed=%d err=%v", open, closed, err)
	}
}

func TestKV(t *testing.T) {
	s, ctx := open(t), context.Background()
	if v, err := s.Get(ctx, "k"); err != nil || v != "" {
		t.Fatalf("missing key: %q %v", v, err)
	}
	_ = s.Set(ctx, "k", "1")
	_ = s.Set(ctx, "k", "2")
	if v, _ := s.Get(ctx, "k"); v != "2" {
		t.Fatalf("got %q", v)
	}
}

func TestMigrationRerunsCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path) // second open re-runs migrations: must not fail
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.MarkTopicDeleted(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
}
