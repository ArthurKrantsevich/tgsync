package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Fake is a Runner for tests. Tests drive its sessions with Emit and Ask.
type Fake struct {
	StartErr error

	mu       sync.Mutex
	sessions []*FakeSession
}

// Start records opts and returns a new FakeSession.
func (f *Fake) Start(_ context.Context, opts StartOptions) (Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.StartErr != nil {
		return nil, f.StartErr
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &FakeSession{Opts: opts, events: make(chan Event, 100), ctx: ctx, cancel: cancel}
	f.sessions = append(f.sessions, s)
	return s, nil
}

// Sessions returns the started sessions in start order.
func (f *Fake) Sessions() []*FakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*FakeSession(nil), f.sessions...)
}

// FakeSession records calls and lets tests emit events.
type FakeSession struct {
	Opts StartOptions

	ctx    context.Context
	cancel context.CancelFunc

	mu         sync.Mutex
	events     chan Event
	sent       []string
	interrupts int
	stopped    []string
	ctxInfo    *ContextInfo
	ctxErr     error
	ctxDelay   time.Duration
	mode       string
	closed     bool
}

func (s *FakeSession) Events() <-chan Event { return s.events }

func (s *FakeSession) Send(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("session closed")
	}
	s.sent = append(s.sent, text)
	return nil
}

func (s *FakeSession) Interrupt(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupts++
	return nil
}

func (s *FakeSession) StopTask(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = append(s.stopped, id)
	return nil
}

// Stopped returns the task ids passed to StopTask.
func (s *FakeSession) Stopped() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.stopped...)
}

func (s *FakeSession) SetPermissionMode(_ context.Context, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	return nil
}

func (s *FakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		s.cancel()
		close(s.events)
	}
	return nil
}

// Emit delivers an event as if the CLI produced it. It does nothing after Close.
func (s *FakeSession) Emit(e Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.events <- e
	}
}

// Ask calls the CanUseTool callback the session was started with.
func (s *FakeSession) Ask(req PermissionRequest) PermissionDecision {
	return s.Opts.CanUseTool(s.ctx, req)
}

func (s *FakeSession) Sent() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.sent...)
}

func (s *FakeSession) Interrupts() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interrupts
}

func (s *FakeSession) Mode() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *FakeSession) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// SetContext sets what ContextUsage returns, after delay.
func (s *FakeSession) SetContext(info *ContextInfo, err error, delay time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ctxInfo, s.ctxErr, s.ctxDelay = info, err, delay
}

func (s *FakeSession) ContextUsage(ctx context.Context) (*ContextInfo, error) {
	s.mu.Lock()
	info, err, delay := s.ctxInfo, s.ctxErr, s.ctxDelay
	s.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if info == nil && err == nil {
		return nil, errors.New("no context data")
	}
	return info, err
}
