package agent

import (
	"context"
	"testing"
	"time"
)

func TestFakeSessionLifecycle(t *testing.T) {
	f := &Fake{}
	var got PermissionRequest
	sess, err := f.Start(context.Background(), StartOptions{Cwd: "/w", CanUseTool: func(ctx context.Context, r PermissionRequest) PermissionDecision {
		got = r
		return PermissionDecision{Allow: true}
	}})
	if err != nil {
		t.Fatal(err)
	}
	fs := f.Sessions()[0]
	if fs.Opts.Cwd != "/w" {
		t.Fatalf("opts: %+v", fs.Opts)
	}
	if err := sess.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	if s := fs.Sent(); len(s) != 1 || s[0] != "hi" {
		t.Fatalf("sent: %v", s)
	}
	if d := fs.Ask(PermissionRequest{ToolName: "Bash"}); !d.Allow || got.ToolName != "Bash" {
		t.Fatalf("ask: %+v %+v", d, got)
	}
	fs.Emit(Event{Kind: EventText, Text: "x"})
	if ev := <-sess.Events(); ev.Text != "x" {
		t.Fatalf("event: %+v", ev)
	}
	_ = sess.Interrupt(context.Background())
	_ = sess.SetPermissionMode(context.Background(), "plan")
	if fs.Interrupts() != 1 || fs.Mode() != "plan" {
		t.Fatalf("interrupts=%d mode=%q", fs.Interrupts(), fs.Mode())
	}
	_ = sess.Close()
	_ = sess.Close()
	if _, ok := <-sess.Events(); ok || !fs.Closed() {
		t.Fatal("events must be closed after Close")
	}
	if err := sess.Send(context.Background(), "late"); err == nil {
		t.Fatal("Send after Close must fail")
	}
}

func TestFakeAskCancelledOnClose(t *testing.T) {
	f := &Fake{}
	_, _ = f.Start(context.Background(), StartOptions{CanUseTool: func(ctx context.Context, r PermissionRequest) PermissionDecision {
		<-ctx.Done()
		return PermissionDecision{Message: "cancelled"}
	}})
	fs := f.Sessions()[0]
	done := make(chan PermissionDecision)
	go func() { done <- fs.Ask(PermissionRequest{ToolName: "Bash"}) }()
	_ = fs.Close()
	select {
	case d := <-done:
		if d.Message != "cancelled" {
			t.Fatalf("decision: %+v", d)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask not cancelled by Close")
	}
}
