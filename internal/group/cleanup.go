package group

import (
	"context"
	"fmt"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

const maxListed = 10

func (g *Group) cleanable(ctx context.Context) ([]store.SessionRow, error) {
	return g.Store.Cleanable(ctx, g.now())
}

// Ask lists what the cleanup would delete and asks for confirmation.
func (g *Group) Ask(ctx context.Context) (string, error) {
	rows, err := g.cleanable(ctx)
	if err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "Убирать нечего", nil
	}
	lines := []string{fmt.Sprintf("🧹 <b>Удалить %d тем?</b> История в них пропадёт.", len(rows))}
	for i, r := range rows {
		if i == maxListed {
			lines = append(lines, fmt.Sprintf("… и ещё %d", len(rows)-maxListed))
			break
		}
		why := "упала без ответа"
		if r.State == store.StateClosed {
			why = "закрыта сегодня"
			if days := int(g.now().Sub(r.UpdatedAt).Hours() / 24); days > 0 {
				why = fmt.Sprintf("закрыта %d дн. назад", days)
			}
		}
		lines = append(lines, "• "+render.Escape(topics.Name(r.Project, r.Title))+" ("+why+")")
	}
	kb := telegram.Keyboard{{{Text: "🗑 Удалить", Data: "cl:yes"}, {Text: "Отмена", Data: "cl:no"}}}
	id, err := g.API.SendMessage(ctx, g.Topics.Control(), strings.Join(lines, "\n"), kb, true)
	if err == nil {
		g.Track(ctx, id)
	}
	return "", err
}

// Confirm deletes the cleanable topics and reports the result in msgID.
// The list is read again: it may have changed since Ask.
func (g *Group) Confirm(ctx context.Context, msgID int) error {
	rows, err := g.cleanable(ctx)
	if err != nil {
		return err
	}
	done := 0
	var fails []string
	failed := 0
	for _, r := range rows {
		if err := g.Topics.Remove(ctx, r.ThreadID); err != nil {
			// The result must fit one message (4096 characters), or the
			// edit fails and the buttons stay.
			if failed++; failed <= maxListed {
				fails = append(fails, render.Escape(cut(r.Title+": "+err.Error(), 200)))
			}
			continue
		}
		done++
		if g.Forget != nil {
			g.Forget(r.ThreadID)
		}
	}
	if failed > maxListed {
		fails = append(fails, fmt.Sprintf("… и ещё %d", failed-maxListed))
	}
	text := fmt.Sprintf("🧹 Удалено %d тем.", done)
	if failed > 0 {
		text = fmt.Sprintf("🧹 Удалено %d из %d. Ошибки:\n%s", done, len(rows), strings.Join(fails, "\n"))
	}
	if err := g.API.EditMessage(ctx, msgID, text, nil); err != nil {
		return err
	}
	return g.RefreshCard(ctx)
}

// cut shortens s to n runes, marking the cut with "…".
func cut(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// Cancel closes the confirmation without deleting anything.
func (g *Group) Cancel(ctx context.Context, msgID int) error {
	return g.API.EditMessage(ctx, msgID, "🧹 Уборка отменена.", nil)
}
