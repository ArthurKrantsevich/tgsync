// Package history reads the session transcripts Claude Code keeps in
// ~/.claude/projects, so sessions started anywhere can be continued.
package history

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// activeWindow is how recently a transcript must change to count as a live session.
const activeWindow = 2 * time.Minute

// tailSize bounds how much of a transcript's tail read finds the custom
// title and last prompt in. Both are rewritten records where only the
// latest value matters, so — unlike the first prompt — there is no way to
// find them by reading forward and stopping early; capping the tail instead
// keeps /history from reading gigabytes across many projects' transcripts,
// which can run to multiple megabytes per session.
const tailSize = 256 * 1024

// Entry is one session transcript.
type Entry struct {
	ID         string
	Title      string
	LastPrompt string
	Entrypoint string
	Updated    time.Time
}

// Active reports whether the session is probably still running somewhere.
func (e Entry) Active(now time.Time) bool { return now.Sub(e.Updated) < activeWindow }

// Encode turns a working directory into Claude Code's project folder name.
func Encode(cwd string) string {
	b := []byte(cwd)
	for i, c := range b {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			b[i] = '-'
		}
	}
	return string(b)
}

// Dir returns the transcript folder of a working directory.
func Dir(claudeHome, cwd string) string {
	return filepath.Join(claudeHome, "projects", Encode(cwd))
}

// List returns up to limit sessions of dir, newest first. Transcripts
// without a user prompt are skipped. A missing dir is not an error.
func List(dir string, limit int) ([]Entry, error) {
	files, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	type file struct {
		id  string
		mod time.Time
	}
	var all []file
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}
		info, err := f.Info()
		if err != nil {
			continue
		}
		all = append(all, file{strings.TrimSuffix(f.Name(), ".jsonl"), info.ModTime()})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].mod.After(all[j].mod) })
	var out []Entry
	for _, f := range all {
		if len(out) == limit {
			break
		}
		e, err := read(filepath.Join(dir, f.id+".jsonl"))
		if err != nil || e.Title == "" {
			continue
		}
		e.ID, e.Updated = f.id, f.mod
		out = append(out, e)
	}
	return out, nil
}

type record struct {
	Type        string `json:"type"`
	IsMeta      bool   `json:"isMeta"`
	IsSidechain bool   `json:"isSidechain"`
	Entrypoint  string `json:"entrypoint"`
	CustomTitle string `json:"customTitle"`
	LastPrompt  string `json:"lastPrompt"`
	Message     struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

func read(path string) (Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()
	var e Entry
	firstPrompt, err := scanFront(bufio.NewReaderSize(f, 1<<16), &e)
	if err != nil {
		return e, err
	}
	if err := scanTail(f, &e); err != nil {
		return e, err
	}
	if e.Title == "" {
		e.Title = firstPrompt
	}
	return e, nil
}

// scanFront reads from the start of the transcript only until the first
// non-meta, non-sidechain user prompt is found (or EOF): that is all this
// pass needs, so it stops there instead of reading the rest of a transcript
// that can run to multiple megabytes.
func scanFront(r *bufio.Reader, e *Entry) (string, error) {
	var firstPrompt string
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 && hasType(line, "user") {
			var rec record
			if json.Unmarshal(line, &rec) == nil && rec.Type == "user" && !rec.IsMeta && !rec.IsSidechain {
				if firstPrompt == "" {
					firstPrompt = promptText(rec.Message.Content)
					if e.Entrypoint == "" {
						e.Entrypoint = rec.Entrypoint
					}
				}
			}
		}
		if firstPrompt != "" || err == io.EOF {
			return firstPrompt, nil
		}
		if err != nil {
			return firstPrompt, err
		}
	}
}

// scanTail reads e's custom title and last prompt from at most the last
// tailSize bytes of the transcript, so this pass is bounded regardless of
// how large the whole file has grown.
func scanTail(f *os.File, e *Entry) error {
	size, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	start := int64(0)
	if size > tailSize {
		start = size - tailSize
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	lines := bytes.Split(buf, []byte("\n"))
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // drop the fragment of a line that began before the window
	}
	for _, line := range lines {
		if len(line) == 0 || !hasType(line, "custom-title", "last-prompt") {
			continue
		}
		var rec record
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		switch rec.Type {
		case "custom-title":
			e.Title = rec.CustomTitle
		case "last-prompt":
			e.LastPrompt = rec.LastPrompt
		}
	}
	return nil
}

// hasType skips lines whose type doesn't match cheaply; assistant and tool
// lines can be megabytes long.
func hasType(line []byte, types ...string) bool {
	head := line
	if len(head) > 200 {
		head = head[:200]
	}
	s := string(head)
	for _, t := range types {
		if strings.Contains(s, `"type":"`+t+`"`) {
			return true
		}
	}
	return false
}

func promptText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				return strings.TrimSpace(b.Text)
			}
		}
	}
	return ""
}
