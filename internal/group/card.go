package group

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

func cardKeyboard() telegram.Keyboard {
	return telegram.Keyboard{{{Text: "🧹 Уборка", Data: "cl:ask"}, {Text: "🧽 Очистить тему", Data: "cl:sweep"}}}
}

func (g *Group) cardText(ctx context.Context) (string, error) {
	open, closed, err := g.Store.SessionCounts(ctx)
	if err != nil {
		return "", err
	}
	text := fmt.Sprintf("🖥 <b>%s</b>\nОткрытых сессий: %d · закрытых: %d",
		render.Escape(g.Topics.Node()), open, closed)
	if !g.Started.IsZero() {
		layout := "15:04"
		if y, m, d := g.now().Date(); g.Started.Day() != d || g.Started.Month() != m || g.Started.Year() != y {
			layout = "02.01 15:04"
		}
		text += "\n🟢 онлайн с " + g.Started.Format(layout)
	}
	return text, nil
}

func (g *Group) cardID(ctx context.Context) int {
	v, _ := g.Store.Get(ctx, keyCard)
	id, _ := strconv.Atoi(v)
	return id
}

func (g *Group) shown(text string) {
	g.mu.Lock()
	g.lastCard, g.lastEdit = text, g.now()
	g.mu.Unlock()
}

// EnsureCard shows the card: it edits the stored message or, when that is
// gone, sends and pins a new one. When the control topic itself is gone (the
// user deleted it), the topic is created again first. Any other edit error
// is returned and the next tick retries. A failed pin (no right) is only
// logged.
func (g *Group) EnsureCard(ctx context.Context) error {
	g.cardMu.Lock() // the ticker and Confirm must not both send a card
	defer g.cardMu.Unlock()
	text, err := g.cardText(ctx)
	if err != nil {
		return err
	}
	if id := g.cardID(ctx); id != 0 {
		err := g.API.EditMessage(ctx, id, text, cardKeyboard())
		if err == nil {
			g.shown(text)
			return nil
		}
		if !errors.Is(err, telegram.ErrMessageGone) && !errors.Is(err, telegram.ErrTopicGone) {
			return err
		}
	}
	id, err := g.API.SendMessage(ctx, g.Topics.Control(), text, cardKeyboard(), true)
	if errors.Is(err, telegram.ErrTopicGone) {
		if _, err = g.Topics.EnsureControl(ctx); err != nil {
			return err
		}
		slog.Info("control topic re-created", "thread", g.Topics.Control())
		id, err = g.API.SendMessage(ctx, g.Topics.Control(), text, cardKeyboard(), true)
	}
	if err != nil {
		return err
	}
	if err := g.API.PinMessage(ctx, id); err != nil {
		slog.Warn("pin card", "err", err)
	}
	g.shown(text)
	return g.Store.Set(ctx, keyCard, strconv.Itoa(id))
}

// RefreshCard edits the card when its counters changed, and at least once a
// minute so a card the user deleted is noticed and re-created.
func (g *Group) RefreshCard(ctx context.Context) error {
	text, err := g.cardText(ctx)
	if err != nil {
		return err
	}
	g.mu.Lock()
	skip := text == g.lastCard && g.now().Sub(g.lastEdit) < time.Minute
	g.mu.Unlock()
	if skip {
		return nil
	}
	return g.EnsureCard(ctx)
}

// Run keeps the card current and sweeps the control topic until ctx is done.
func (g *Group) Run(ctx context.Context) {
	t := time.NewTicker(cardRefresh)
	defer t.Stop()
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := g.RefreshCard(ctx); err != nil {
				slog.Warn("refresh card", "err", err)
			}
		case <-sweep.C:
			g.Sweep(ctx, g.now().Add(-sweepAfter))
		}
	}
}
