//go:build unix

package files

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRefusesFIFO(t *testing.T) {
	dir := t.TempDir()
	pipe := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	if _, _, err := Read(dir, "pipe", nil); err == nil {
		t.Fatal("a FIFO must be refused by the path check")
	}
	// Even past the path check, opening must not wait for a writer.
	info, err := os.Stat(pipe)
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { _, err := readChecked(pipe, info, MaxSize); errc <- err }()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("a FIFO must not be read")
		}
	case <-time.After(3 * time.Second):
		if w, err := os.OpenFile(pipe, os.O_WRONLY, 0); err == nil {
			w.Close()
		}
		t.Fatal("opening a FIFO blocked")
	}
}
