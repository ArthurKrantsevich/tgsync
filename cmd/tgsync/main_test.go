package main

import (
	"path/filepath"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/permissions"
)

// TestProtectedPaths: profiles.yaml (its env may hold API keys and it picks
// the agent's settings) and the sudo askpass socket are refused to the agent
// like .env and the database.
func TestProtectedPaths(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "askpass.sock")
	protected := protectedPaths(dir, "data/tgsync.db", sock)
	project := filepath.Join(dir, "projects", "demo")
	for _, target := range []string{
		filepath.Join(dir, ".env"),
		filepath.Join(dir, "data", "tgsync.db"),
		filepath.Join(dir, "profiles.yaml"),
		sock,
	} {
		for _, c := range []struct {
			tool string
			args map[string]any
		}{
			{"Read", map[string]any{"file_path": target}},
			{"Write", map[string]any{"file_path": target, "content": "x"}},
			{"Edit", map[string]any{"file_path": target}},
			{"Bash", map[string]any{"command": "cat " + target}},
			{"Grep", map[string]any{"pattern": ".", "path": target}},
		} {
			got, _ := permissions.Evaluate(permissions.Input{Tool: c.tool, Args: c.args, ProjectDir: project, Protected: protected})
			if got != permissions.Deny {
				t.Errorf("%s %s: %v, want deny", c.tool, filepath.Base(target), got)
			}
		}
	}
}
