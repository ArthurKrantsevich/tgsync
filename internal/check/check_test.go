package check

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	var out bytes.Buffer
	ok := Run(context.Background(), &out, []Check{
		{Name: "good", Run: func(context.Context) (string, error) { return "fine", nil }},
		{Name: "bad", Run: func(context.Context) (string, error) { return "", errors.New("broken: fix it") }},
	})
	if ok {
		t.Fatal("a failed check must make Run return false")
	}
	got := out.String()
	if !strings.Contains(got, "✓ good — fine") || !strings.Contains(got, "✗ bad — broken: fix it") {
		t.Fatalf("output: %q", got)
	}
	if !Run(context.Background(), &out, []Check{{Name: "x", Run: func(context.Context) (string, error) { return "", nil }}}) {
		t.Fatal("all good must return true")
	}
}

func TestClaudeCLI(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "claude")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho '2.1.280 (Claude Code)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	detail, err := ClaudeCLI(fake).Run(context.Background())
	if err != nil || !strings.Contains(detail, "2.1.280") {
		t.Fatalf("detail=%q err=%v", detail, err)
	}
	if _, err := ClaudeCLI(filepath.Join(dir, "missing")).Run(context.Background()); err == nil {
		t.Fatal("missing CLI must fail")
	}
}

func TestDir(t *testing.T) {
	dir := t.TempDir()
	if _, err := Dir("projects", dir, true).Run(context.Background()); err != nil {
		t.Fatalf("writable dir: %v", err)
	}
	if _, err := Dir("projects", filepath.Join(dir, "none"), false).Run(context.Background()); err == nil {
		t.Fatal("missing dir must fail")
	}
	ro := filepath.Join(dir, "ro")
	_ = os.Mkdir(ro, 0o555)
	if runtime.GOOS != "windows" && os.Getuid() != 0 {
		if _, err := Dir("db", ro, true).Run(context.Background()); err == nil {
			t.Fatal("read-only dir must fail when writable is required")
		}
	}
}

func TestFindClaude(t *testing.T) {
	look := func(have ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, h := range have {
				base := h[strings.LastIndexAny(h, `/\`)+1:]
				if base == name || strings.TrimSuffix(base, ".cmd") == name {
					return h, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	if got, err := findClaude("windows", look(`C:\bin\claude.exe`)); err != nil || got != `C:\bin\claude.exe` {
		t.Fatalf("exe: %q %v", got, err)
	}
	_, err := findClaude("windows", look(`C:\npm\claude.cmd`))
	if err == nil || !strings.Contains(err.Error(), "claude.cmd") {
		t.Fatalf("npm shim must be refused with a hint, got %v", err)
	}
	if got, err := findClaude("linux", look("/usr/bin/claude")); err != nil || got != "/usr/bin/claude" {
		t.Fatalf("linux: %q %v", got, err)
	}
}
