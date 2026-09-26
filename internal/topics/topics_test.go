package topics

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

func setup(t *testing.T) (*Manager, *telegram.Fake) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := telegram.NewFake()
	return New(api, st, "laptop-1"), api
}

func TestEnsureControlCreatesOnce(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	a, err := m.EnsureControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := m.EnsureControl(ctx)
	if a != b || len(api.Topics()) != 1 || api.Topic(a).Name != "🖥 laptop-1" {
		t.Fatalf("a=%d b=%d topics=%+v", a, b, api.Topics())
	}
}

func TestSessionTopicLifecycle(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	id, err := m.CreateSession(ctx, "demo", "fix auth")
	if err != nil {
		t.Fatal(err)
	}
	if got := api.Topic(id).Name; got != "demo · fix auth" {
		t.Fatalf("name: %q", got)
	}
	if err := m.Finish(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if got := api.Topic(id); got.Name != "demo · fix auth" || !got.Closed {
		t.Fatalf("topic: %+v", got)
	}
}

func TestNameTruncatesByRunes(t *testing.T) {
	n := Name("проект", strings.Repeat("длинное описание задачи 🚀 ", 20))
	if c := utf8.RuneCountInString(n); c > 128 {
		t.Fatalf("want at most 128 runes, got %d", c)
	}
	if !utf8.ValidString(n) || !strings.HasSuffix(n, "…") {
		t.Fatalf("bad truncation: %q", n)
	}
}

func TestLink(t *testing.T) {
	if got := Link(-1001234567890, 42); got != "https://t.me/c/1234567890/42" {
		t.Fatalf("got %q", got)
	}
}

func TestNameFitsUTF16Limit(t *testing.T) {
	n := Name("p", strings.Repeat("🚀", 200))
	if u := len(utf16.Encode([]rune(n))); u > 128 {
		t.Fatalf("name has %d UTF-16 units", u)
	}
	if !utf8.ValidString(n) || !strings.HasSuffix(n, "…") {
		t.Fatalf("bad truncation: %q", n)
	}
}

func TestEnsureControlRecreatesDeletedTopic(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	old, _ := m.EnsureControl(ctx)
	api.DeleteTopic(old)
	id, err := m.EnsureControl(ctx)
	if err != nil || id == old || m.Control() != id || api.Topic(id).Name != "🖥 laptop-1" {
		t.Fatalf("old=%d new=%d control=%d err=%v", old, id, m.Control(), err)
	}
	again, _ := m.EnsureControl(ctx)
	if again != id {
		t.Fatalf("live control topic must be kept: %d vs %d", again, id)
	}
}

// The card refresh and /control in General may both notice a deleted
// control topic at once: only one new topic may come out of it.
func TestEnsureControlConcurrentRecreatesOnce(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	old, _ := m.EnsureControl(ctx)
	api.DeleteTopic(old)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = m.EnsureControl(ctx) }()
	}
	wg.Wait()
	if tps := api.Topics(); len(tps) != 1 || tps[0].ID != m.Control() {
		t.Fatalf("topics %+v, control %d", tps, m.Control())
	}
}

func TestAlive(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	id, _ := m.CreateSession(ctx, "demo", "x")
	if ok, err := m.Alive(ctx, id, "demo", "x"); !ok || err != nil {
		t.Fatalf("alive: %v %v", ok, err)
	}
	api.DeleteTopic(id)
	if ok, err := m.Alive(ctx, id, "demo", "x"); ok || err != nil {
		t.Fatalf("deleted: %v %v", ok, err)
	}
}

func TestRename(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	id, _ := m.CreateSession(ctx, "demo", "Новая задача")
	if err := m.Rename(ctx, id, "demo", "add tests"); err != nil {
		t.Fatal(err)
	}
	if got := api.Topic(id).Name; got != "demo · add tests" {
		t.Fatalf("name: %q", got)
	}
}

func TestColorStableAndAllowed(t *testing.T) {
	allowed := map[int]bool{0x6FB9F0: true, 0xFFD67E: true, 0xCB86DB: true, 0x8EEE98: true, 0xFF93B2: true, 0xFB6F5F: true}
	for _, n := range []string{"laptop-1", "server", "мак", ""} {
		if c := Color(n); !allowed[c] || c != Color(n) {
			t.Fatalf("%q: %#x", n, c)
		}
	}
}

func TestNewSessionTopicHasColorAndActiveIcon(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	if err := m.LoadIcons(ctx); err != nil {
		t.Fatal(err)
	}
	id, _ := m.CreateSession(ctx, "demo", "x")
	if tp := api.Topic(id); tp.Color != Color("laptop-1") || tp.Icon != "i-active" {
		t.Fatalf("topic: %+v", tp)
	}
}

func TestFinishClosesWithIcon(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	_ = m.LoadIcons(ctx)
	id, _ := m.CreateSession(ctx, "demo", "x")
	if err := m.Finish(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if tp := api.Topic(id); !tp.Closed || tp.Icon != "i-ok" {
		t.Fatalf("topic: %+v", tp)
	}
}

func TestFinishEmptyRemoves(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	id, _ := m.CreateSession(ctx, "demo", "x")
	if err := m.Finish(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if api.Topic(id).ID != 0 {
		t.Fatal("topic still exists")
	}
	if err := m.Remove(ctx, id); err != nil { // already gone: success
		t.Fatal(err)
	}
}

func TestFinishEmptyClosesWhenDeleteNotAllowed(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	_ = m.LoadIcons(ctx)
	api.Perms.DeleteMessages = false // deleteForumTopic needs can_delete_messages
	id, _ := m.CreateSession(ctx, "demo", "x")
	if err := m.Finish(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	if tp := api.Topic(id); tp.ID == 0 || !tp.Closed || tp.Icon != "i-ok" {
		t.Fatalf("topic must fall back to closed: %+v", tp)
	}
}

func TestIconFallbacks(t *testing.T) {
	m, api := setup(t)
	ctx := context.Background()
	api.Icons = map[string]string{"👍": "i-thumb", "❗️": "i-bang"} // no ✅/❌/💻; one with FE0F
	_ = m.LoadIcons(ctx)
	id, _ := m.CreateSession(ctx, "demo", "x")
	if api.Topic(id).Icon != "" {
		t.Fatalf("active icon must be skipped: %q", api.Topic(id).Icon)
	}
	_ = m.SetIcon(ctx, id, IconFailed)
	if api.Topic(id).Icon != "i-bang" {
		t.Fatalf("failed fallback: %q", api.Topic(id).Icon)
	}
	_ = m.SetIcon(ctx, id, IconClosed)
	if api.Topic(id).Icon != "i-thumb" {
		t.Fatalf("closed fallback: %q", api.Topic(id).Icon)
	}
	api.Icons = map[string]string{}
	_ = m.LoadIcons(ctx)
	if err := m.SetIcon(ctx, id, IconFailed); err != nil || api.Topic(id).Icon != "i-thumb" {
		t.Fatalf("unknown icon must be a no-op: %v %q", err, api.Topic(id).Icon)
	}
}
