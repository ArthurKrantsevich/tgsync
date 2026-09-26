package sudo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocketPath(t *testing.T) {
	short := filepath.Join(string(filepath.Separator)+"h", "tgsync")
	if got := SocketPath(short); got != filepath.Join(short, "askpass.sock") {
		t.Fatalf("short home: %q", got)
	}
	long := filepath.Join(string(filepath.Separator)+"h", strings.Repeat("x", 120))
	got := SocketPath(long)
	if filepath.Dir(got) != filepath.Clean(os.TempDir()) {
		t.Fatalf("long home must use the temp folder: %q", got)
	}
	if got != SocketPath(long) {
		t.Fatal("the path must be stable for the same home")
	}
	if got == SocketPath(long+"y") {
		t.Fatal("different homes need different sockets")
	}
}
