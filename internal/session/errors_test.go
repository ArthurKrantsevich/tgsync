package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

// failingSession is a FakeSession whose Interrupt and SetPermissionMode fail.
type failingSession struct {
	*agent.FakeSession
}

func (s *failingSession) Interrupt(context.Context) error {
	return errors.New("control channel closed")
}

func (s *failingSession) SetPermissionMode(context.Context, string) error {
	return errors.New("control channel closed")
}

type failingRunner struct{ agent.Fake }

func (r *failingRunner) Start(ctx context.Context, o agent.StartOptions) (agent.Session, error) {
	a, err := r.Fake.Start(ctx, o)
	if err != nil {
		return nil, err
	}
	return &failingSession{a.(*agent.FakeSession)}, nil
}

func TestMaxTurnReportsFailedInterrupt(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	c := withClock(e)
	e.m.d.Runner = &failingRunner{}
	e.m.d.MaxTurn = time.Hour
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	c.add(61 * time.Minute)
	e.m.tick(ctx)
	e.hasMessage(t, thread, "control channel closed")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "прерываю") {
			t.Fatalf("claims an interrupt that failed: %q", m.HTML)
		}
	}
	testutil.Eventually(t, "retry allowed", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		return !e.m.sessions[thread].overtime
	})
}

func TestSetModeKeptWhenProcessRefuses(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	e.m.d.Runner = &failingRunner{}
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "a")
	e.sessionState(t, thread, "running")
	if err := e.m.SetMode(ctx, thread, "plan"); err == nil {
		t.Fatal("the process refused the mode: SetMode must fail")
	}
	rows, _ := e.st.OpenSessions(ctx)
	if len(rows) != 1 || rows[0].Mode != "default" {
		t.Fatalf("stored mode: %+v", rows)
	}
	e.m.mu.Lock()
	mode := e.m.sessions[thread].row.Mode
	e.m.mu.Unlock()
	if mode != "default" {
		t.Fatalf("session mode: %q", mode)
	}
}
