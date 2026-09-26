//go:build linux

package sudo

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// TestAskpassThroughSudoProcess runs the real tgsync binary in askpass mode,
// once started by a process named "sudo" and once directly.
func TestAskpassThroughSudoProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tgsync")
	if out, err := exec.Command("go", "build", "-o", bin, "../../cmd/tgsync").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	fakeSudo := filepath.Join(dir, "sudo")
	// The askpass must run as a child of a process named "sudo", like with real sudo.
	_ = os.WriteFile(fakeSudo, []byte("#!/bin/sh\n\"$SUDO_ASKPASS\" 'password:'\n"), 0o755)

	s := NewServer(filepath.Join(dir, "a.sock"), ModeEnv, "pw-e2e", nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Real sudo is setuid root and the fake one is not, so after checking
	// that the real check refuses it, the test expects the parent to run as
	// this user to cover the rest of the chain.
	var sameUser atomic.Bool
	real := s.peerCheck
	s.peerCheck = func(c net.Conn) (int, error) {
		if !sameUser.Load() {
			return real(c)
		}
		pid, err := peerPID(c)
		if err != nil {
			return 0, err
		}
		return sudoParent("/proc", pid, os.Geteuid())
	}
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = s.Serve(ctx) }()
	run := func(prog, token string) (string, error) {
		cmd := exec.Command(prog)
		cmd.Env = append(os.Environ(), "SUDO_ASKPASS="+bin, "TGSYNC_ASKPASS=1", "TGSYNC_ASKPASS_SOCK="+s.Socket(), TokenVar+"="+token)
		var out bytes.Buffer
		cmd.Stdout = &out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	}
	tok := NewToken()
	s.Grant(1, tok, 2)
	if pw, err := run(bin, tok); err == nil || pw != "" {
		t.Fatalf("direct call must be refused: %q %v", pw, err)
	}
	if pw, err := run(fakeSudo, tok); err == nil || pw != "" {
		t.Fatalf("a process named sudo without root must be refused: %q %v", pw, err)
	}
	sameUser.Store(true)
	tok = NewToken()
	s.Grant(1, tok, 1)
	if pw, err := run(fakeSudo, tok); err != nil || pw != "pw-e2e" {
		t.Fatalf("call through sudo: %q %v", pw, err)
	}
}
