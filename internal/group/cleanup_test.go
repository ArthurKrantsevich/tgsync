package group

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// closedSession creates a closed session with a live topic and moves the
// group clock `age` ahead, as if the session closed that long ago.
func closedSession(t *testing.T, g *Group, title string, age time.Duration) int {
	t.Helper()
	ctx := context.Background()
	id, err := g.Topics.CreateSession(ctx, "demo", title)
	if err != nil {
		t.Fatal(err)
	}
	r := &store.SessionRow{ThreadID: id, Project: "demo", Cwd: "/w", Title: title, State: store.StateClosed}
	if err := g.Store.CreateSession(ctx, r); err != nil {
		t.Fatal(err)
	}
	_ = g.Store.AddUsage(ctx, store.UsageRow{At: time.Now(), ThreadID: id, Project: "demo", Model: "m"})
	g.Now = func() time.Time { return time.Now().Add(age) }
	return id
}

func lastControl(g *Group, api *telegram.Fake) telegram.FakeMessage {
	msgs := api.Messages(g.Topics.Control())
	return msgs[len(msgs)-1]
}

func TestCleanupConfirm(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	old := closedSession(t, g, "old", 10*24*time.Hour)
	var forgotten []int
	g.Forget = func(th int) { forgotten = append(forgotten, th) }
	toast, err := g.Ask(ctx)
	if err != nil || toast != "" {
		t.Fatalf("toast=%q err=%v", toast, err)
	}
	ask := lastControl(g, api)
	if !strings.Contains(ask.HTML, "Удалить 1 тем") || !strings.Contains(ask.HTML, "demo · old") {
		t.Fatalf("ask: %s", ask.HTML)
	}
	if err := g.Confirm(ctx, ask.ID); err != nil {
		t.Fatal(err)
	}
	if api.Topic(old).ID != 0 {
		t.Fatal("topic not deleted")
	}
	if len(forgotten) != 1 || forgotten[0] != old {
		t.Fatalf("forget: %v", forgotten)
	}
	for _, m := range api.Messages(g.Topics.Control()) {
		if m.ID == ask.ID && (!strings.Contains(m.HTML, "Удалено 1") || m.Keyboard != nil) {
			t.Fatalf("result: %+v", m)
		}
	}
}

func TestCleanupNothing(t *testing.T) {
	g, _, _ := setup(t)
	if toast, _ := g.Ask(context.Background()); toast != "Убирать нечего" {
		t.Fatalf("toast: %q", toast)
	}
}

func TestCleanupFreshClosed(t *testing.T) {
	g, api, _ := setup(t)
	closedSession(t, g, "fresh", time.Hour)
	if toast, err := g.Ask(context.Background()); toast != "" || err != nil {
		t.Fatalf("toast: %q, err: %v", toast, err)
	}
	msgs := api.Messages(g.Topics.Control())
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].HTML, "fresh") {
		t.Fatalf("messages: %+v", msgs)
	}
}

func TestCleanupCancel(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	old := closedSession(t, g, "old", 10*24*time.Hour)
	_, _ = g.Ask(ctx)
	if err := g.Cancel(ctx, lastControl(g, api).ID); err != nil {
		t.Fatal(err)
	}
	if api.Topic(old).ID == 0 {
		t.Fatal("cancel deleted the topic")
	}
}

func TestCleanupTopicAlreadyGone(t *testing.T) {
	g, api, st := setup(t)
	ctx := context.Background()
	old := closedSession(t, g, "old", 10*24*time.Hour)
	_, _ = g.Ask(ctx)
	ask := lastControl(g, api)
	api.DeleteTopic(old) // the user deleted it meanwhile
	if err := g.Confirm(ctx, ask.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.Cleanable(ctx, time.Now().Add(365*24*time.Hour)); len(rows) != 0 {
		t.Fatalf("gone topic not marked: %+v", rows)
	}
}

func TestCleanupPartialFailure(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	gone := closedSession(t, g, "gone", 10*24*time.Hour)
	kept := closedSession(t, g, "kept", 10*24*time.Hour)
	_, _ = g.Ask(ctx)
	ask := lastControl(g, api)
	// "gone" is already gone and counts as deleted; "kept" cannot be deleted.
	api.DeleteTopic(gone)
	api.Perms.DeleteMessages = false
	if err := g.Confirm(ctx, ask.ID); err != nil {
		t.Fatal(err)
	}
	if api.Topic(kept).ID == 0 {
		t.Fatal("topic deleted without the right")
	}
	for _, m := range api.Messages(g.Topics.Control()) {
		if m.ID == ask.ID && (!strings.Contains(m.HTML, "Удалено 1 из 2") || !strings.Contains(m.HTML, "kept: ")) {
			t.Fatalf("result: %s", m.HTML)
		}
	}
}

func TestCleanupFailureListCapped(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	for i := 0; i < 40; i++ {
		closedSession(t, g, fmt.Sprint("s", i, strings.Repeat("x", 100)), 10*24*time.Hour)
	}
	_, _ = g.Ask(ctx)
	ask := lastControl(g, api)
	api.Perms.DeleteMessages = false // every removal fails
	if err := g.Confirm(ctx, ask.ID); err != nil {
		t.Fatal(err)
	}
	for _, m := range api.Messages(g.Topics.Control()) {
		if m.ID != ask.ID {
			continue
		}
		if strings.Count(m.HTML, "\n") != maxListed+1 || !strings.Contains(m.HTML, fmt.Sprintf("… и ещё %d", 40-maxListed)) {
			t.Fatalf("result: %s", m.HTML)
		}
		if n := utf8.RuneCountInString(m.HTML); n > 4096 || m.Keyboard != nil {
			t.Fatalf("result must fit a message and drop the buttons: %d chars, kb %v", n, m.Keyboard)
		}
	}
}

func TestCleanupListCapped(t *testing.T) {
	g, api, _ := setup(t)
	for i := 0; i < 12; i++ {
		closedSession(t, g, fmt.Sprint("s", i), 10*24*time.Hour)
	}
	_, _ = g.Ask(context.Background())
	html := lastControl(g, api).HTML
	if strings.Count(html, "• ") != 10 || !strings.Contains(html, "ещё 2") {
		t.Fatalf("list: %s", html)
	}
}
