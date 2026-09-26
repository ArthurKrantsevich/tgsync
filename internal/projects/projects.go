// Package projects lists and creates project directories under PROJECTS_ROOT.
package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
)

// ErrBadName rejects names that could escape the projects root.
var ErrBadName = errors.New("имя проекта: латиница, цифры, «.», «_», «-», до 64 символов, первый символ — буква или цифра")

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Registry is the set of projects under Root.
type Registry struct{ Root string }

// List returns project directories in alphabetical order. Hidden folders and
// folders whose names Dir rejects ("My App", non-Latin) are left out: they
// could not be opened.
func (r Registry) List() ([]string, error) {
	entries, err := os.ReadDir(r.Root)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && nameRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// Dir returns the directory of an existing project.
func (r Registry) Dir(name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", ErrBadName
	}
	dir := filepath.Join(r.Root, name)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("проект %q не найден в %s", name, r.Root)
	}
	return dir, nil
}

// Create makes a new project directory with an empty git repository.
func (r Registry) Create(ctx context.Context, name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", ErrBadName
	}
	dir := filepath.Join(r.Root, name)
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("проект %q уже существует", name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if out, err := exec.CommandContext(ctx, "git", "init", "-q", dir).CombinedOutput(); err != nil {
		return "", fmt.Errorf("git init: %v: %s", err, out)
	}
	return dir, nil
}
