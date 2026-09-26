package telegram

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
)

// dispatcher keeps messages of one topic in arrival order, so a long paste
// split by Telegram reaches the agent in order. Button presses skip the
// queue to stay responsive while a message is being handled.
type dispatcher struct {
	h      Handler
	mu     sync.Mutex
	queues map[int]*topicQueue
}

// topicQueue is the backlog of one topic. Its worker exits when the backlog
// is empty, so idle and closed topics keep no goroutine.
type topicQueue struct {
	ch      chan dispatched
	pending int // items dispatched and not yet handled; guarded by dispatcher.mu
}

type dispatched struct {
	ctx context.Context
	u   Update
}

func newDispatcher(h Handler) *dispatcher {
	return &dispatcher{h: h, queues: map[int]*topicQueue{}}
}

// handle runs the handler and turns a panic into a log line: one bad update
// must not take the node down with every running agent turn.
func (d *dispatcher) handle(ctx context.Context, u Update) {
	defer func() {
		if v := recover(); v != nil {
			slog.Error("update handler panic", "panic", v, "thread", u.ThreadID,
				"callback", u.CallbackData, "stack", string(debug.Stack()))
		}
	}()
	d.h(ctx, u)
}

func (d *dispatcher) dispatch(ctx context.Context, u Update) {
	if u.CallbackID != "" {
		go d.handle(ctx, u)
		return
	}
	d.mu.Lock()
	q, ok := d.queues[u.ThreadID]
	if !ok {
		q = &topicQueue{ch: make(chan dispatched, 256)}
		d.queues[u.ThreadID] = q
		go d.worker(u.ThreadID, q)
	}
	q.pending++
	d.mu.Unlock()
	q.ch <- dispatched{ctx: ctx, u: u}
}

func (d *dispatcher) worker(thread int, q *topicQueue) {
	for item := range q.ch {
		d.handle(item.ctx, item.u)
		d.mu.Lock()
		q.pending--
		if q.pending == 0 {
			delete(d.queues, thread)
			d.mu.Unlock()
			return
		}
		d.mu.Unlock()
	}
}
