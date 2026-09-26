// Package files tracks what the agent changes and prepares files for Telegram.
package files

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// MaxSize is the Bot API upload limit.
const MaxSize = 50 << 20

var writeTools = map[string]bool{"Write": true, "Edit": true, "MultiEdit": true, "NotebookEdit": true}

// Tracker remembers files changed inside a project: in the current turn and
// in the whole session, plus which versions were already sent.
type Tracker struct {
	dir string

	mu      sync.Mutex
	turn    map[string]bool
	session map[string]bool
	sent    map[string]string
}

func NewTracker(projectDir string) *Tracker {
	return &Tracker{dir: projectDir, turn: map[string]bool{}, session: map[string]bool{}, sent: map[string]string{}}
}

// Note records a tool call; only file writes inside the project count.
func (t *Tracker) Note(tool string, input map[string]any) {
	if !writeTools[tool] {
		return
	}
	p, _ := input["file_path"].(string)
	if p == "" {
		p, _ = input["notebook_path"].(string)
	}
	rel, ok := relIn(t.dir, p)
	if !ok {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turn[rel], t.session[rel] = true, true
}

// Changed returns the files changed in the current turn, sorted.
func (t *Tracker) Changed() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return sorted(t.turn)
}

// ResetTurn starts a new turn; the session-wide list is kept.
func (t *Tracker) ResetTurn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.turn = map[string]bool{}
}

// Mentioned returns files changed in this session whose path appears in text.
func (t *Tracker) Mentioned(text string) []string {
	t.mu.Lock()
	all := sorted(t.session)
	t.mu.Unlock()
	var out []string
	for _, rel := range all {
		full := filepath.Join(t.dir, rel)
		if containsPath(text, rel) || containsPath(text, full) ||
			containsPath(text, filepath.ToSlash(rel)) || containsPath(text, filepath.ToSlash(full)) {
			out = append(out, rel)
		}
	}
	return out
}

// WasSent reports whether exactly this version (content hash) of rel was sent.
func (t *Tracker) WasSent(rel, sum string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sent[rel] == sum
}

// MarkSent records that this version of rel reached the user.
func (t *Tracker) MarkSent(rel, sum string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent[rel] = sum
}

// IsSent reports whether some version of rel was sent in this session.
func (t *Tracker) IsSent(rel string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sent[rel] != ""
}

func sorted(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func relIn(dir, p string) (string, bool) {
	if p == "" {
		return "", false
	}
	rel, ok := Within(dir, Abs(dir, p))
	if !ok || rel == "." {
		return "", false
	}
	return rel, true
}

// containsPath finds rel in text as a whole path, not as part of a longer name.
func containsPath(text, rel string) bool {
	part := func(c byte) bool {
		return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '/' || c == '\\'
	}
	for from := 0; ; {
		i := strings.Index(text[from:], rel)
		if i < 0 {
			return false
		}
		i += from
		end := i + len(rel)
		dotSlash := i >= 2 && text[i-2:i] == "./" && (i == 2 || !part(text[i-3]) && text[i-3] != '.')
		before := i == 0 || dotSlash || !part(text[i-1]) && text[i-1] != '.'
		after := end == len(text) || !part(text[end]) && !(text[end] == '.' && end+1 < len(text) && part(text[end+1]))
		if before && after {
			return true
		}
		from = i + 1
	}
}

// Resolve checks that p is a regular file inside the project that may be
// sent: not a tgsync file and within the upload limit.
func Resolve(projectDir, p string, protected []string) (abs, rel string, err error) {
	c, err := resolve(projectDir, p, protected)
	return c.abs, c.rel, err
}

// Read resolves p like Resolve and reads it through one handle opened on
// the checked target: a file swapped in after the check, a FIFO or a device
// is refused instead of read, and no more than MaxSize bytes are read.
func Read(projectDir, p string, protected []string) (rel string, data []byte, err error) {
	c, err := resolve(projectDir, p, protected)
	if err != nil {
		return "", nil, err
	}
	data, err = readChecked(c.real, c.info, MaxSize)
	if err != nil {
		return "", nil, fmt.Errorf("%s: %w", c.rel, err)
	}
	return c.rel, data, nil
}

// checked is a file that passed resolve: its target and identity.
type checked struct {
	abs, rel string
	real     string      // abs with symlinks resolved
	info     os.FileInfo // the target as it was checked
}

func resolve(projectDir, p string, protected []string) (checked, error) {
	rel, ok := relIn(projectDir, p)
	if !ok {
		return checked{}, fmt.Errorf("%s: файл вне папки проекта", p)
	}
	abs := filepath.Join(projectDir, rel)
	st, err := os.Stat(abs)
	var real string
	// Symlinks are followed by the upload, so the target must pass the same checks.
	if err == nil {
		var rerr error
		real, rerr = filepath.EvalSymlinks(abs)
		root, rootErr := filepath.EvalSymlinks(projectDir)
		if rerr != nil || rootErr != nil {
			return checked{}, fmt.Errorf("%s: не удалось проверить путь", rel)
		}
		if _, ok := relIn(root, real); !ok {
			return checked{}, fmt.Errorf("%s: ссылка ведёт за пределы проекта", rel)
		}
		for _, pr := range protected {
			if pr == "" {
				continue
			}
			prReal, perr := filepath.EvalSymlinks(pr)
			if perr != nil {
				prReal = filepath.Clean(pr)
			}
			if SamePath(real, prReal) || SamePath(abs, pr) || SameFile(abs, pr) {
				return checked{}, fmt.Errorf("%s: служебный файл tgsync не отправляется", rel)
			}
		}
	}
	switch {
	case err != nil:
		return checked{}, fmt.Errorf("%s: файл не найден", rel)
	case st.IsDir():
		return checked{}, fmt.Errorf("%s: это папка", rel)
	case !st.Mode().IsRegular():
		return checked{}, fmt.Errorf("%s: не обычный файл", rel)
	case st.Size() > MaxSize:
		return checked{}, fmt.Errorf("%s: %d МБ, больше лимита Telegram 50 МБ", rel, st.Size()>>20)
	}
	return checked{abs: abs, rel: rel, real: real, info: st}, nil
}

// readChecked opens path without following a final symlink and without
// waiting for a FIFO writer, makes sure the handle is the regular file
// that was checked and reads at most limit bytes.
func readChecked(path string, want os.FileInfo, limit int64) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|openFlags, 0)
	if err != nil {
		return nil, errors.New("не удалось открыть файл")
	}
	defer f.Close()
	st, err := f.Stat()
	switch {
	case err != nil:
		return nil, errors.New("не удалось проверить файл")
	case !st.Mode().IsRegular():
		return nil, errors.New("не обычный файл")
	case !os.SameFile(st, want):
		return nil, errors.New("файл подменили после проверки")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("больше лимита Telegram 50 МБ")
	}
	return data, nil
}

var documentExt = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".pdf": true, ".html": true,
	".htm": true, ".csv": true, ".png": true, ".jpg": true, ".jpeg": true, ".svg": true}

// IsDocument reports whether a file is meant to be read by a person rather than source code.
func IsDocument(rel string) bool { return documentExt[strings.ToLower(filepath.Ext(rel))] }

// Match reports whether rel matches any glob. A "**/" prefix means "in any folder".
func Match(globs []string, rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, g := range globs {
		if rest, ok := strings.CutPrefix(g, "**/"); ok {
			if m, _ := path.Match(rest, path.Base(rel)); m {
				return true
			}
			if m, _ := path.Match(rest, rel); m {
				return true
			}
			continue
		}
		if m, _ := path.Match(g, rel); m {
			return true
		}
	}
	return false
}

func capDiff(out []byte) (string, error) {
	if len(out) > MaxSize {
		return "", errors.New("diff больше 50 МБ")
	}
	return string(out), nil
}

func ResolveDir(projectDir, p string) (abs, rel string, err error) {
	p = strings.TrimSpace(p)
	if p == "" || p == "." {
		p = projectDir
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectDir, p)
	}
	p = filepath.Clean(p)
	if p != filepath.Clean(projectDir) {
		if rel, ok := relIn(projectDir, p); ok {
			p = filepath.Join(projectDir, rel)
		} else {
			return "", "", fmt.Errorf("%s: папка вне проекта", p)
		}
	}
	st, err := os.Stat(p)
	if err != nil || !st.IsDir() {
		return "", "", fmt.Errorf("%s: папка не найдена", p)
	}
	real, err1 := filepath.EvalSymlinks(p)
	root, err2 := filepath.EvalSymlinks(projectDir)
	if err1 != nil || err2 != nil {
		return "", "", fmt.Errorf("%s: не удалось проверить путь", p)
	}
	if real != root {
		if _, ok := relIn(root, real); !ok {
			return "", "", fmt.Errorf("%s: ссылка ведёт за пределы проекта", p)
		}
	}
	rel, _ = filepath.Rel(projectDir, p)
	if rel == "." {
		rel = ""
	}
	return p, rel, nil
}

// SafeName turns a file name sent by the user into a safe base name.
func SafeName(name string) string {
	name = path.Base(filepath.ToSlash(name))
	var b strings.Builder
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := b.String()
	if r := []rune(out); len(r) > 100 {
		out = string(r[len(r)-100:])
	}
	if strings.Trim(out, "._") == "" {
		return "file"
	}
	return out
}

// Size formats a byte count for people.
func Size(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%d Б", n)
	case n < 1<<20:
		return fmt.Sprintf("%d КБ", n>>10)
	default:
		return fmt.Sprintf("%.1f МБ", float64(n)/(1<<20))
	}
}
