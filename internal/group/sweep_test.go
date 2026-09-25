package group

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/limits"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

func menuKB() telegram.Keyboard { return telegram.Keyboard{{{Text: "x", Data: "m:menu"}}} }

func TestSweepDeletesOldControlMessages(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	if err := g.EnsureCard(ctx); err != nil {
		t.Fatal(err)
	}
	card := lastControl(g, api).ID
	ctrl := g.ControlAPI(api)
	old, _ := ctrl.SendMessage(ctx, g.Topics.Control(), "old", nil, false)
	g.Track(ctx, 999) // a user command the fake does not hold: delete fails, row goes
	g.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	fresh, _ := ctrl.SendMessage(ctx, g.Topics.Control(), "fresh", nil, false)
	if n := g.Sweep(ctx, g.now().Add(-sweepAfter)); n != 1 {
		t.Fatalf("swept %d", n)
	}
	if got := api.DeletedMessages(); !reflect.DeepEqual(got, []int{old}) {
		t.Fatalf("deleted %v", got)
	}
	if n := g.Sweep(ctx, g.now().Add(time.Second)); n != 1 { // 🧽 button: everything tracked
		t.Fatalf("swept all %d", n)
	}
	if got := api.DeletedMessages(); !reflect.DeepEqual(got, []int{old, fresh}) {
		t.Fatalf("deleted %v", got)
	}
	if msgs := api.Messages(g.Topics.Control()); len(msgs) != 1 || msgs[0].ID != card {
		t.Fatalf("card must stay: %+v", msgs)
	}
}

// deleteDown fails deletes as Telegram does while it is unreachable; with
// block set a delete waits until its context ends.
type deleteDown struct {
	*telegram.Fake
	block bool
}

func (d deleteDown) DeleteMessage(ctx context.Context, _ int) error {
	if d.block {
		<-ctx.Done()
	}
	return &telegram.RetryError{Err: errors.New("connection refused")}
}

func TestSweepKeepsMessagesOnTemporaryErrors(t *testing.T) {
	g, api, st := setup(t)
	ctx := context.Background()
	id, _ := g.ControlAPI(api).SendMessage(ctx, g.Topics.Control(), "old", nil, false)
	g.API = deleteDown{Fake: api}
	all := g.now().Add(time.Second)
	if n := g.Sweep(ctx, all); n != 0 {
		t.Fatalf("swept %d", n)
	}
	if ids, _ := st.TrackedMessages(ctx, all); !reflect.DeepEqual(ids, []int{id}) {
		t.Fatalf("tracked %v: a message must stay tracked after a temporary failure", ids)
	}
	old := sweepTimeout
	sweepTimeout = 100 * time.Millisecond
	defer func() { sweepTimeout = old }()
	g.API = deleteDown{Fake: api, block: true}
	start := time.Now()
	g.Sweep(ctx, all)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("a sweep must be bounded in time, took %v", d)
	}
	g.API = api
	if n := g.Sweep(ctx, all); n != 1 || !reflect.DeepEqual(api.DeletedMessages(), []int{id}) {
		t.Fatalf("after the outage: swept %d, deleted %v", n, api.DeletedMessages())
	}
}

func TestOnlyOneMenuLives(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	ctrl := g.ControlAPI(api)
	first, _ := ctrl.SendMessage(ctx, g.Topics.Control(), "menu 1", menuKB(), false)
	_, _ = ctrl.SendMessage(ctx, g.Topics.Control(), "plain", nil, false)
	_, _ = ctrl.SendMessage(ctx, 12345, "session menu", menuKB(), false) // other topics are not touched
	_, _ = ctrl.SendMessage(ctx, g.Topics.Control(), "menu 2", menuKB(), false)
	if got := api.DeletedMessages(); !reflect.DeepEqual(got, []int{first}) {
		t.Fatalf("deleted %v", got)
	}
}

func TestCardShowsStartAndSweepButton(t *testing.T) {
	g, api, _ := setup(t)
	g.Started = time.Date(2026, 9, 26, 3, 20, 0, 0, time.Local)
	g.Now = func() time.Time { return g.Started.Add(time.Hour) }
	if err := g.EnsureCard(context.Background()); err != nil {
		t.Fatal(err)
	}
	card := lastControl(g, api)
	if !strings.Contains(card.HTML, "🟢 онлайн с 03:20") {
		t.Fatalf("card: %s", card.HTML)
	}
	if len(card.Keyboard[0]) != 2 || card.Keyboard[0][1].Data != "cl:sweep" {
		t.Fatalf("keyboard: %+v", card.Keyboard)
	}
	g.Now = func() time.Time { return g.Started.Add(48 * time.Hour) }
	if text, _ := g.cardText(context.Background()); !strings.Contains(text, "онлайн с 26.09 03:20") {
		t.Fatalf("other day: %s", text)
	}
}

// Limit notices go to the control topic through ControlAPI (see
// cmd/tgsync): they must be swept like other control messages, and having
// no buttons they must not replace the menu.
func TestLimitNoticesAreSweptAndKeepTheMenu(t *testing.T) {
	g, api, st := setup(t)
	ctx := context.Background()
	ctrl := g.ControlAPI(api)
	menu, _ := ctrl.SendMessage(ctx, g.Topics.Control(), "menu", menuKB(), false)
	tr := limits.New(ctrl, st, g.Topics.Control)
	tr.Observe(ctx, 0, agent.RateLimit{Window: "five_hour", Status: "rejected", Utilization: -1})
	notice := lastControl(g, api)
	if notice.ID == menu || !strings.Contains(notice.HTML, "исчерпан") {
		t.Fatalf("no limit notice: %+v", notice)
	}
	if got := api.DeletedMessages(); len(got) != 0 {
		t.Fatalf("a limit notice must not replace the menu, deleted %v", got)
	}
	ids, _ := st.TrackedMessages(ctx, g.now().Add(time.Second))
	if !reflect.DeepEqual(ids, []int{menu, notice.ID}) {
		t.Fatalf("tracked %v, want menu %d and notice %d", ids, menu, notice.ID)
	}
}
