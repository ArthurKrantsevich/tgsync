//go:build unix

package session

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSendFileRefusesFIFO(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{})
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	thread, _ := e.m.New(ctx, "demo", dir, "")
	errc := make(chan error, 1)
	go func() { errc <- e.m.SendFile(ctx, thread, "pipe") }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("a FIFO must not be sent")
		}
	case <-time.After(3 * time.Second):
		if w, err := os.OpenFile(pipe, os.O_WRONLY, 0); err == nil { // let the stuck reader go
			w.Close()
		}
		t.Fatal("sending a FIFO blocked the delivery")
	}
}
