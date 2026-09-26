package session

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

func TestFolderButtonsCapped(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"a.txt": "a", "b.txt": "b", "c.txt": "c"})
	thread, _ := e.m.New(ctx, "demo", dir, "")
	var pages []telegram.FakeMessage
	for i := 0; i < maxListings+2; i++ {
		if err := e.m.ListDir(ctx, thread, "", 0); err != nil {
			t.Fatal(err)
		}
		msgs := e.api.Messages(thread)
		pages = append(pages, msgs[len(msgs)-1])
	}
	e.m.mu.Lock()
	n := 0
	for _, r := range e.m.fileRefs {
		if r.thread == thread {
			n++
		}
	}
	e.m.mu.Unlock()
	if n != maxListings*3 {
		t.Fatalf("file buttons kept: %d, want %d", n, maxListings*3)
	}
	first, last := pages[0].Keyboard[0][0], pages[len(pages)-1].Keyboard[0][0]
	if alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: pages[0].ID, CallbackData: first.Data}); alert != "Кнопка устарела" {
		t.Fatalf("oldest listing: %q", alert)
	}
	if alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: pages[len(pages)-1].ID, CallbackData: last.Data}); alert != "" {
		t.Fatalf("newest listing: %q", alert)
	}
}

func TestFolderNavigationInPlaceDoesNotGrow(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"sub/a.txt": "a"})
	thread, _ := e.m.New(ctx, "demo", dir, "")
	_ = e.m.ListDir(ctx, thread, "", 0)
	msgs := e.api.Messages(thread)
	listing := msgs[len(msgs)-1]
	for i := 0; i < 10; i++ { // into sub and back up, in the same message
		for _, label := range []string{"📁 sub", "⬆"} {
			btn, ok := e.api.Button(thread, label)
			if !ok {
				t.Fatalf("%s missing", label)
			}
			e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, MessageID: listing.ID, CallbackData: btn.Data})
		}
	}
	e.m.mu.Lock()
	n := len(e.m.fileRefs)
	e.m.mu.Unlock()
	if n > 2 {
		t.Fatalf("buttons of replaced pages kept: %d", n)
	}
}

func TestTurnButtonsCapped(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	turns := maxTurnSummaries + 2
	for i := 0; i < turns; i++ {
		if i > 0 {
			_ = e.m.Message(ctx, thread, "again")
		}
		write(t, dir, "a.go", fmt.Sprintf("%d\n", i))
		agentTurn(s, dir, "a.go")
		testutil.Eventually(t, "summary", func() bool {
			n := 0
			for _, m := range e.api.Messages(thread) {
				if strings.Contains(m.HTML, "Изменено за ход") {
					n++
				}
			}
			return n == i+1
		})
	}
	testutil.Eventually(t, "latest summary registered", func() bool {
		e.m.mu.Lock()
		defer e.m.mu.Unlock()
		for _, r := range e.m.turnRefs {
			if r.turn == turns {
				return true
			}
		}
		return false
	})
	e.m.mu.Lock()
	n := len(e.m.turnRefs)
	e.m.mu.Unlock()
	if n != maxTurnSummaries {
		t.Fatalf("turn summaries kept: %d, want %d", n, maxTurnSummaries)
	}
}
