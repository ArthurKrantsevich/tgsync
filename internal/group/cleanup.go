package group

import (
	"context"
	"errors"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
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
		return i18n.T("group.cleanup.nothing"), nil
	}
	lines := []string{i18n.N("group.cleanup.n.ask", len(rows), len(rows))}
	for i, r := range rows {
		if i == maxListed {
			lines = append(lines, i18n.T("group.cleanup.more", len(rows)-maxListed))
			break
		}
		why := i18n.T("group.cleanup.crashed")
		if r.State == store.StateClosed {
			why = i18n.T("group.cleanup.closed_today")
			if days := int(g.now().Sub(r.UpdatedAt).Hours() / 24); days > 0 {
				why = i18n.N("group.cleanup.n.closed_days", days, days)
			}
		}
		lines = append(lines, "• "+render.Escape(topics.Name(r.Project, r.Title))+" ("+why+")")
	}
	kb := telegram.Keyboard{{{Text: i18n.T("group.btn.delete"), Data: "cl:yes"}, {Text: i18n.T("group.btn.cancel"), Data: "cl:no"}}}
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
		fails = append(fails, i18n.T("group.cleanup.more", failed-maxListed))
	}
	text := i18n.N("group.cleanup.n.done", done, done)
	if failed > 0 {
		text = i18n.T("group.cleanup.partial", done, len(rows), strings.Join(fails, "\n"))
	}
	// A confirmation that is gone (swept, deleted) takes no result; the
	// topics are deleted all the same, so the card is refreshed anyway.
	if err := g.API.EditMessage(ctx, msgID, text, nil); err != nil &&
		!errors.Is(err, telegram.ErrMessageGone) && !errors.Is(err, telegram.ErrTopicGone) {
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
	return g.API.EditMessage(ctx, msgID, i18n.T("group.cleanup.canceled"), nil)
}
