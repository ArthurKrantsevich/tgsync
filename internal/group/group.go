// Package group keeps the node's Telegram group tidy: admin rights notice,
// hidden General topic, group avatar and description, the pinned control
// card and the 🧹 cleanup of old topics.
package group

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

const (
	keyCard     = "control_card_msg"
	keyProfile  = "group_profile_done"
	keyGeneral  = "general_hidden"
	keyMissing  = "missing_rights"
	cardRefresh = 10 * time.Second
)

// Group owns group-wide decoration and cleanup.
type Group struct {
	API         telegram.API
	Store       *store.Store
	Topics      *topics.Manager
	Avatar      []byte
	Description string
	Now         func() time.Time // nil: time.Now
	Forget      func(thread int) // drops a loaded session after cleanup; may be nil
	Started     time.Time        // node start, shown on the card; zero hides it

	cardMu   sync.Mutex // one card update at a time: no duplicate cards
	menuMu   sync.Mutex // one menu replacement at a time
	mu       sync.Mutex
	lastCard string
	lastEdit time.Time
}

func (g *Group) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// Setup applies group-wide settings once per start. Failures are logged:
// none of this is needed for sessions to work.
func (g *Group) Setup(ctx context.Context) {
	if err := g.Topics.LoadIcons(ctx); err != nil {
		slog.Warn("topic icons", "err", err)
	}
	g.hideGeneral(ctx)
	r, err := g.API.Rights(ctx)
	if err != nil { // rights unknown: skip the parts that depend on them
		slog.Warn("group rights", "err", err)
		return
	}
	g.noticeRights(ctx, r)
	if r.ChangeInfo {
		g.profile(ctx)
	}
}

// noticeRights tells the user once which optional rights are missing. It
// posts again only when the set changes.
func (g *Group) noticeRights(ctx context.Context, r telegram.Rights) {
	var missing []string
	if !r.PinMessages {
		missing = append(missing, i18n.T("group.rights.pin"))
	}
	if !r.ChangeInfo {
		missing = append(missing, i18n.T("group.rights.info"))
	}
	if !r.DeleteMessages {
		missing = append(missing, i18n.T("group.rights.delete"))
	}
	key := strings.Join(missing, "\n")
	prev, err := g.Store.Get(ctx, keyMissing)
	if err != nil || prev == key {
		return
	}
	if key != "" {
		text := i18n.T("group.rights.notice", key)
		if _, err := g.API.SendMessage(ctx, g.Topics.Control(), text, nil, true); err != nil {
			slog.Warn("rights notice", "err", err)
			return
		}
	}
	_ = g.Store.Set(ctx, keyMissing, key)
}

// hideGeneral hides the General topic once per group: a topic the user
// shows again stays visible. Groups set up by older versions (profile done)
// count as done, since they were hidden then.
func (g *Group) hideGeneral(ctx context.Context) {
	if done, _ := g.Store.Get(ctx, keyGeneral); done == "1" {
		return
	}
	if old, _ := g.Store.Get(ctx, keyProfile); old == "1" {
		_ = g.Store.Set(ctx, keyGeneral, "1")
		return
	}
	if err := g.API.HideGeneral(ctx); err != nil {
		slog.Warn("hide General", "err", err)
		return
	}
	_ = g.Store.Set(ctx, keyGeneral, "1")
}

// profile sets the group avatar and description when the group has none.
// It runs once per group: a removed avatar is not put back.
func (g *Group) profile(ctx context.Context) {
	if done, _ := g.Store.Get(ctx, keyProfile); done == "1" {
		return
	}
	hasPhoto, hasDesc, err := g.API.GroupInfo(ctx)
	if err != nil {
		slog.Warn("group info", "err", err)
		return
	}
	if !hasPhoto && len(g.Avatar) > 0 {
		if err := g.API.SetGroupPhoto(ctx, g.Avatar); err != nil {
			slog.Warn("group photo", "err", err)
			return
		}
	}
	if !hasDesc && g.Description != "" {
		if err := g.API.SetGroupDescription(ctx, g.Description); err != nil {
			slog.Warn("group description", "err", err)
			return
		}
	}
	_ = g.Store.Set(ctx, keyProfile, "1")
}
