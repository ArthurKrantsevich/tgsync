package session

import (
	"context"
	"log/slog"
	"runtime/debug"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
)

// A panic in a goroutine the package starts (the event reader, the file
// summary, the tick loop, the timer notices) would end the whole node, with
// every session on it. Each entry point defers a recover that logs the
// stack and puts the session back into a usable state instead.

// internalError is what the user sees after a recovered panic.
const internalError = "❌ Внутренняя ошибка tgsync, подробности в логе ноды."

// lockWait bounds the check that m.mu is free after a panic.
const lockWait = 2 * time.Second

// crashed logs a recovered panic r of goroutine where. Critical sections do
// not defer Unlock, so a panic inside one leaves m.mu locked for good and
// the node could only hang: then the panic goes on and ends the process, as
// it did before, and the restart marks running turns as interrupted.
func (m *Manager) crashed(where string, s *sess, r any) {
	thread := 0
	if s != nil {
		thread = s.row.ThreadID
	}
	slog.Error("panic recovered", "where", where, "thread", thread, "panic", r, "stack", string(debug.Stack()))
	deadline := time.Now().Add(lockWait)
	for !m.mu.TryLock() {
		if time.Now().After(deadline) {
			slog.Error("panic left the session lock held, stopping the node", "where", where)
			panic(r)
		}
		time.Sleep(time.Millisecond)
	}
	m.mu.Unlock()
}

// safely runs f and recovers a panic in it; recovered reports whether one
// was recovered.
func (m *Manager) safely(where string, s *sess, f func()) (recovered bool) {
	defer func() {
		if r := recover(); r != nil {
			m.crashed(where, s, r)
			recovered = true
		}
	}()
	f()
	return false
}

// eventPanicked handles a panic while handling ev. The events keep being
// read. A turn still running is interrupted: the CLI then ends it with a
// result as usual. A result that panicked cannot come again, so the turn it
// ended is closed here, and the queue moves on. When the interrupt fails,
// claude may still be working: ending the turn would hand the next message
// to the busy process, whose old result would end the new turn early. The
// process is closed instead; readEvents then sees its events end and fails
// the turn, and the next one starts a fresh process resuming the session.
func (m *Manager) eventPanicked(s *sess, ev agent.Event) {
	ctx := context.Background()
	m.mu.Lock()
	closed, inTurn, a := s.closed || m.stopping, s.inTurn, s.agent
	m.mu.Unlock()
	if closed {
		return
	}
	m.say(ctx, s, internalError, false)
	switch {
	case ev.Kind == agent.EventResult:
		m.failTurn(ctx, s)
	case inTurn && a != nil:
		if err := m.interrupt(ctx, s, a); err != nil {
			slog.Warn("interrupt after panic failed, closing the process", "thread", s.row.ThreadID, "err", err)
			_ = a.Close()
			return
		}
	}
	m.schedule(ctx)
}

// failTurn ends the current turn as failed without waiting for the agent.
// It does nothing outside a turn.
func (m *Manager) failTurn(ctx context.Context, s *sess) {
	m.mu.Lock()
	if !s.inTurn || s.closed {
		m.mu.Unlock()
		return
	}
	s.inTurn, s.waits = false, 0
	s.pending, s.interrupted, s.lastText = "", false, ""
	st, msgID := s.status, s.statusMsg
	s.statusMsg, s.dirty = 0, false
	if len(s.inbox) > 0 {
		m.enqueue(s.row.ThreadID)
	}
	m.mu.Unlock()
	m.d.Broker.CancelThread(ctx, s.row.ThreadID)
	if msgID != 0 {
		st.State = store.StateFailed
		m.edit(ctx, s, msgID, render.StatusText(st, m.d.Now()))
	}
	m.endReaction(ctx, s)
	m.setState(ctx, s, store.StateFailed)
}
