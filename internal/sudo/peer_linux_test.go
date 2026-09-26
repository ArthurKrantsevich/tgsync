package sudo

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeProc writes /proc entries for an askpass process 100 whose parent 42
// is named comm and runs with the given Uid line.
func fakeProc(t *testing.T, comm, uid string) string {
	t.Helper()
	proc := t.TempDir()
	write := func(pid, name, text string) {
		dir := filepath.Join(proc, pid)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("100", "stat", "100 (tgsync) S 42 100 100 0 -1 4194560")
	write("42", "comm", comm+"\n")
	write("42", "status", "Name:\t"+comm+"\nUmask:\t0022\nState:\tS (sleeping)\nUid:\t"+uid+"\nGid:\t1000\t1000\t1000\t1000\n")
	return proc
}

func TestSudoParentMustRunAsRoot(t *testing.T) {
	// Real sudo is setuid root: its real uid is the user, its effective uid 0.
	if ppid, err := sudoParent(fakeProc(t, "sudo", "1000\t0\t0\t0"), 100, 0); err != nil || ppid != 42 {
		t.Fatalf("real sudo refused: %d %v", ppid, err)
	}
	// Any program of the user can be named sudo, but cannot run as root.
	if _, err := sudoParent(fakeProc(t, "sudo", "1000\t1000\t1000\t1000"), 100, 0); err == nil {
		t.Fatal("a process named sudo without root must be refused")
	}
	if _, err := sudoParent(fakeProc(t, "bash", "1000\t0\t0\t0"), 100, 0); err == nil {
		t.Fatal("a parent that is not sudo must be refused")
	}
	if _, err := sudoParent(fakeProc(t, "sudo", "garbage"), 100, 0); err == nil {
		t.Fatal("an unreadable Uid line must be refused")
	}
}
