// Package check diagnoses a node installation.
package check

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// findClaude finds the claude CLI on PATH. On Windows only the native
// claude.exe works: the SDK refuses npm's claude.cmd shim.
func findClaude(goos string, look func(string) (string, error)) (string, error) {
	if goos != "windows" {
		if bin, err := look("claude"); err == nil {
			return bin, nil
		}
		return "", errors.New(i18n.T("check.claude_missing"))
	}
	if bin, err := look("claude.exe"); err == nil {
		return bin, nil
	}
	if bin, err := look("claude"); err == nil {
		return "", errors.New(i18n.T("check.claude_npm", filepath.Base(bin)))
	}
	return "", errors.New(i18n.T("check.claude_exe_missing"))
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
			return "", errors.New(i18n.T("check.dir_missing", path))
		}
		if writable {
			f, err := os.CreateTemp(path, ".tgsync-check-*")
			if err != nil {
				return "", errors.New(i18n.T("check.dir_readonly", path))
			}
			f.Close()
			os.Remove(f.Name())
		}
		return path, nil
	}}
}
