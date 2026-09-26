package group

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

const (
	keyMenu    = "control_menu_msg"
	sweepAfter = time.Hour
	sweepEvery = time.Minute
)

// sweepTimeout bounds one sweep, so an outage does not stall Group.Run.
var sweepTimeout = time.Minute

// ControlAPI wraps api so that messages sent to the control topic are
// tracked for the sweep, and a message with buttons replaces the previous
// one: only the latest menu stays. The card and the rights notice are sent
// through g.API and stay.
func (g *Group) ControlAPI(api telegram.API) telegram.API {
	return &controlAPI{API: api, g: g}
}

type controlAPI struct {
	telegram.API
	g *Group
}

func (c *controlAPI) SendMessage(ctx context.Context, thread int, html string, kb telegram.Keyboard, silent bool) (int, error) {
	id, err := c.API.SendMessage(ctx, thread, html, kb, silent)
	if err == nil && thread == c.g.Topics.Control() {
		c.g.Track(ctx, id)
		if kb != nil {
			c.g.replaceMenu(ctx, id)
		}
	}
	return id, err
}

func (c *controlAPI) SendDocument(ctx context.Context, thread int, name string, data []byte, caption string, silent bool) (int, error) {
	id, err := c.API.SendDocument(ctx, thread, name, data, caption, silent)
	if err == nil && thread == c.g.Topics.Control() {
		c.g.Track(ctx, id)
	}
	return id, err
}

// Track marks a control topic message for deletion after sweepAfter.
func (g *Group) Track(ctx context.Context, msgID int) {
	if err := g.Store.TrackMessage(ctx, msgID, g.now()); err != nil {
		slog.Warn("track message", "err", err)
	}
}

// replaceMenu deletes the previous menu and remembers msgID as the current one.
func (g *Group) replaceMenu(ctx context.Context, msgID int) {
	g.menuMu.Lock()
	defer g.menuMu.Unlock()
	if v, _ := g.Store.Get(ctx, keyMenu); v != "" {
		if prev, _ := strconv.Atoi(v); prev != 0 && prev != msgID {
			_ = g.API.DeleteMessage(ctx, prev) // already gone or too old: nothing to do
			_ = g.Store.UntrackMessage(ctx, prev)
		}
	}
	_ = g.Store.Set(ctx, keyMenu, strconv.Itoa(msgID))
}

// Sweep deletes tracked messages sent before `before` and returns how many
// were deleted. A message that cannot be deleted (gone, or older than
// Telegram allows) is forgotten as well; after a temporary failure (Telegram
// unreachable, the sweep out of time) it stays for the next sweep.
func (g *Group) Sweep(ctx context.Context, before time.Time) int {
	ctx, cancel := context.WithTimeout(ctx, sweepTimeout)
	defer cancel()
	ids, err := g.Store.TrackedMessages(ctx, before)
	if err != nil {
		slog.Warn("sweep", "err", err)
		return 0
	}
	n := 0
	for _, id := range ids {
		if ctx.Err() != nil {
			break
		}
		err := g.API.DeleteMessage(ctx, id)
		if err == nil {
			n++
		} else {
			slog.Debug("sweep delete", "msg", id, "err", err)
			var re *telegram.RetryError
			if errors.As(err, &re) || ctx.Err() != nil {
				continue
			}
		}
		// A fresh context: the sweep's one may just have run out.
		_ = g.Store.UntrackMessage(context.WithoutCancel(ctx), id)
	}
	return n
}

// SweepAll deletes every tracked message now: the 🧽 button of the card.
func (g *Group) SweepAll(ctx context.Context) string {
	if n := g.Sweep(ctx, g.now().Add(time.Second)); n > 0 {
		return i18n.T("group.sweep.done", n)
	}
	return i18n.T("group.sweep.nothing")
}
