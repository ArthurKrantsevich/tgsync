package history

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncode(t *testing.T) {
	if got := Encode("/home/user/Projects/tgsync"); got != "-home-user-Projects-tgsync" {
		t.Fatalf("got %q", got)
	}
	if got := Dir("/h/.claude", "/w/a.b"); got != filepath.FromSlash("/h/.claude/projects/-w-a-b") {
		t.Fatalf("got %q", got)
	}
}

func write(t *testing.T, dir, id string, age time.Duration, lines ...string) {
	t.Helper()
	p := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	_ = os.Chtimes(p, ts, ts)
}

func TestList(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 200_000)
	write(t, dir, "old", 3*time.Hour,
		`{"type":"user","isMeta":true,"message":{"content":"<meta>"},"entrypoint":"cli"}`,
		`{"type":"user","message":{"content":"fix login bug"},"entrypoint":"cli"}`,
		`{"type":"assistant","message":{"content":"`+big+`"}}`,
		`{"type":"last-prompt","lastPrompt":"and add tests"}`)
	write(t, dir, "new", time.Minute,
		`{"type":"user","isSidechain":true,"message":{"content":"sub"}}`,
		`{"type":"user","message":{"content":[{"type":"text","text":"block prompt"}]},"entrypoint":"claude-desktop"}`,
		`{"type":"custom-title","customTitle":"Old title"}`,
		`{"type":"custom-title","customTitle":"Telegram bot"}`)
	write(t, dir, "empty", time.Minute, `{"type":"summary"}`)
	_ = os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600)

	got, err := List(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("entries: %+v", got)
	}
	if got[0].Title != "Telegram bot" || got[0].Entrypoint != "claude-desktop" {
		t.Fatalf("new: %+v", got[0])
	}
	if got[1].Title != "fix login bug" || got[1].LastPrompt != "and add tests" || got[1].Entrypoint != "cli" {
		t.Fatalf("old: %+v", got[1])
	}
	if !got[0].Active(time.Now()) || got[1].Active(time.Now()) {
		t.Fatal("activity by mtime is wrong")
	}
	if one, _ := List(dir, 1); len(one) != 1 {
		t.Fatalf("limit: %d", len(one))
	}
	if none, err := List(filepath.Join(dir, "missing"), 5); err != nil || len(none) != 0 {
		t.Fatalf("missing dir: %v %v", none, err)
	}
}

// TestListBoundedRead pins the read cap that keeps /history from reading
// gigabytes across many projects: the custom title and last prompt come from
// a bounded tail of the file (256 KB), not a full scan. A custom-title
// record that a huge line pushes out of that tail window must be ignored
// (falling back to the first prompt), while a last-prompt inside the window
// must still be picked up, and the giant line itself must never need to be
// read during the front scan for the first prompt.
func TestListBoundedRead(t *testing.T) {
	dir := t.TempDir()
	pad := strings.Repeat("x", 300_000) // bigger than the 256 KB tail window
	write(t, dir, "big", time.Minute,
		`{"type":"user","message":{"content":"first prompt"},"entrypoint":"cli"}`,
		`{"type":"custom-title","customTitle":"outside the tail window"}`,
		`{"type":"assistant","message":{"content":"`+pad+`"}}`,
		`{"type":"last-prompt","lastPrompt":"inside the tail"}`)

	got, err := List(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("entries: %+v", got)
	}
	e := got[0]
	if e.Title != "first prompt" {
		t.Fatalf("Title = %q, want fallback to the first prompt (a title before the tail window must be ignored)", e.Title)
	}
	if e.LastPrompt != "inside the tail" {
		t.Fatalf("LastPrompt = %q", e.LastPrompt)
	}
	if e.Entrypoint != "cli" {
		t.Fatalf("Entrypoint = %q", e.Entrypoint)
	}
}
