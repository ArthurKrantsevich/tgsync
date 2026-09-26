package telegram

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestDispatcherKeepsOrderPerThread(t *testing.T) {
	var mu sync.Mutex
	var got []string
	release := make(chan struct{})
	d := newDispatcher(func(ctx context.Context, u Update) {
		if u.Text == "1" {
			<-release
		}
		if u.CallbackID != "" {
			close(release)
			return
		}
		mu.Lock()
		got = append(got, u.Text)
		mu.Unlock()
	})
	ctx := context.Background()
	for _, s := range []string{"1", "2", "3"} {
		d.dispatch(ctx, Update{ThreadID: 7, Text: s})
	}
	d.dispatch(ctx, Update{ThreadID: 7, CallbackID: "cb"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || got[0] != "1" || got[1] != "2" || got[2] != "3" {
		t.Fatalf("order: %v (a callback must not wait behind slow messages)", got)
	}
}

func TestDispatcherSurvivesPanics(t *testing.T) {
	var mu sync.Mutex
	var got []string
	d := newDispatcher(func(ctx context.Context, u Update) {
		if u.Text == "boom" || u.CallbackData == "boom" {
			panic("handler bug")
		}
		mu.Lock()
		got = append(got, u.Text+u.CallbackData)
		mu.Unlock()
	})
	ctx := context.Background()
	d.dispatch(ctx, Update{ThreadID: 7, Text: "boom"})
	d.dispatch(ctx, Update{ThreadID: 7, Text: "after"})
	d.dispatch(ctx, Update{ThreadID: 7, CallbackID: "cb", CallbackData: "boom"})
	d.dispatch(ctx, Update{ThreadID: 7, CallbackID: "cb2", CallbackData: "ok"})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		d.mu.Lock()
		idle := len(d.queues) == 0
		d.mu.Unlock()
		if n == 2 && idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("handled %v; the queue must go on after a panic and end when empty", got)
}
