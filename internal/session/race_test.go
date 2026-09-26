package session

import (
	"context"
	"sync"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

// holdDeleteAPI blocks the DeleteMessage of an armed message id until
// release, so a test can act in the middle of finishTurn.
type holdDeleteAPI struct {
	*telegram.Fake
	mu      sync.Mutex
	target  int
	hit     chan struct{}
	release chan struct{}
}

func (h *holdDeleteAPI) arm(id int) {
	h.mu.Lock()
	h.target = id
	h.mu.Unlock()
}

func (h *holdDeleteAPI) DeleteMessage(ctx context.Context, id int) error {
	h.mu.Lock()
	hold := h.target != 0 && h.target == id
	if hold {
		h.target = 0
	}
	h.mu.Unlock()
	if hold {
		close(h.hit)
		<-h.release
	}
	return h.Fake.DeleteMessage(ctx, id)
}

func TestTurnStartedDuringFinishKeepsAnswer(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	api := &holdDeleteAPI{Fake: e.api, hit: make(chan struct{}), release: make(chan struct{})}
	e.m.d.API = api
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	_ = e.m.MessageFrom(ctx, thread, "first", 501)
	s := e.session(t, 0)
	e.m.mu.Lock()
	status := e.m.sessions[thread].statusMsg
	e.m.mu.Unlock()
	api.arm(status)
	s.Emit(agent.Event{Kind: agent.EventText, Text: "answer A"})
	s.Emit(result())
	<-api.hit // finishTurn freed the slot and waits for Telegram
	_ = e.m.MessageFrom(ctx, thread, "second", 502)
	testutil.Eventually(t, "second turn", func() bool { return len(s.Sent()) == 2 })
	close(api.release)
	e.hasMessage(t, thread, "answer A")
	testutil.Eventually(t, "first reaction removed", func() bool { return e.api.Reaction(501) == "" })
	if e.api.Reaction(502) != "👀" {
		t.Fatal("the running turn's message lost its reaction")
	}
}

// gateRunner holds Start until release, like a slow claude start.
type gateRunner struct {
	agent.Fake
	entered, release chan struct{}
}

func (r *gateRunner) Start(ctx context.Context, o agent.StartOptions) (agent.Session, error) {
	close(r.entered)
	<-r.release
	return r.Fake.Start(ctx, o)
}

func TestCloseDuringStartStopsProcess(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	run := &gateRunner{entered: make(chan struct{}), release: make(chan struct{})}
	e.m.d.Runner = run
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	done := make(chan struct{})
	go func() { _ = e.m.Message(ctx, thread, "task"); close(done) }()
	<-run.entered
	if err := e.m.Close(ctx, thread); err != nil {
		t.Fatal(err)
	}
	close(run.release)
	<-done
	ss := run.Sessions()
	if len(ss) != 1 {
		t.Fatalf("sessions: %d", len(ss))
	}
	if !ss[0].Closed() || len(ss[0].Sent()) != 0 {
		t.Fatalf("orphan process: closed=%v sent=%v", ss[0].Closed(), ss[0].Sent())
	}
}

func TestStartTurnAfterCloseDoesNothing(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	thread, _ := e.m.New(ctx, "demo", "/w/demo", "")
	s := e.m.lookup(thread)
	_ = e.m.Close(ctx, thread) // between pick and startTurn: the inbox is gone
	e.m.startTurn(ctx, s)
	if n := len(e.ag.Sessions()); n != 0 {
		t.Fatalf("closed session started %d processes", n)
	}
}
