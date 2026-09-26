// Package check diagnoses a node installation.
package check

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// findClaude finds the claude CLI on PATH. On Windows only the native
// claude.exe works: the SDK refuses npm's claude.cmd shim.
func findClaude(goos string, look func(string) (string, error)) (string, error) {
	if goos != "windows" {
		if bin, err := look("claude"); err == nil {
			return bin, nil
		}
		return "", fmt.Errorf("claude не найден в PATH; установи Claude Code или задай CLAUDE_CLI_PATH")
	}
	if bin, err := look("claude.exe"); err == nil {
		return bin, nil
	}
	if bin, err := look("claude"); err == nil {
		return "", fmt.Errorf("найден %s (обёртка npm), он не запускается; установи Claude Code для Windows нативным установщиком или задай CLAUDE_CLI_PATH", filepath.Base(bin))
	}
	return "", fmt.Errorf("claude.exe не найден в PATH; установи Claude Code или задай CLAUDE_CLI_PATH")
}

// Check is one diagnostic step. Run returns a short detail on success.
type Check struct {
	Name string
	Run  func(ctx context.Context) (string, error)
}

// Run executes the checks in order, prints one line per check and reports
// whether all of them passed.
func Run(ctx context.Context, w io.Writer, checks []Check) bool {
	ok := true
	for _, c := range checks {
		detail, err := c.Run(ctx)
		if err != nil {
			ok = false
			fmt.Fprintf(w, "✗ %s — %v\n", c.Name, err)
			continue
		}
		if detail == "" {
			fmt.Fprintf(w, "✓ %s\n", c.Name)
		} else {
			fmt.Fprintf(w, "✓ %s — %s\n", c.Name, detail)
		}
	}
	return ok
}

// ClaudeCLI finds the claude CLI (path, or PATH when empty) and reads its version.
func ClaudeCLI(path string) Check {
	return Check{Name: "claude CLI", Run: func(ctx context.Context) (string, error) {
		bin := path
		if bin == "" {
			var err error
			if bin, err = findClaude(runtime.GOOS, exec.LookPath); err != nil {
				return "", err
			}
		}
		out, err := exec.CommandContext(ctx, bin, "--version").Output()
		if err != nil {
			return "", fmt.Errorf("%s --version: %v", bin, err)
		}
		return bin + " " + strings.TrimSpace(string(out)), nil
	}}
}

// Dir checks that path is a directory and, when writable is set, that files can be created in it.
func Dir(name, path string, writable bool) Check {
	return Check{Name: name, Run: func(context.Context) (string, error) {
		st, err := os.Stat(path)
		if err != nil || !st.IsDir() {
			return "", fmt.Errorf("%s: папка не найдена", path)
		}
		if writable {
			f, err := os.CreateTemp(path, ".tgsync-check-*")
			if err != nil {
				return "", fmt.Errorf("%s: нет прав на запись", path)
			}
			f.Close()
			os.Remove(f.Name())
		}
		return path, nil
	}}
}
