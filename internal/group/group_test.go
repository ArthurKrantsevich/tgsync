package group

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

func setup(t *testing.T) (*Group, *telegram.Fake, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := telegram.NewFake()
	tp := topics.New(api, st, "node1")
	if _, err := tp.EnsureControl(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Group{API: api, Store: st, Topics: tp, Avatar: []byte{1}, Description: "desc"}, api, st
}

func TestSetupHidesGeneralAndSetsProfileOnce(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	g.Setup(ctx)
	if !api.GeneralHidden || string(api.GroupPhoto) != "\x01" || api.GroupDescription != "desc" {
		t.Fatalf("hidden=%v photo=%v desc=%q", api.GeneralHidden, api.GroupPhoto, api.GroupDescription)
	}
	api.GroupPhoto, api.GroupDescription = nil, "" // the user removed them
	api.GeneralHidden = false                      // and showed General again
	g.Setup(ctx)
	if api.GroupPhoto != nil || api.GroupDescription != "" {
		t.Fatal("profile must not be set again")
	}
	if api.GeneralHidden {
		t.Fatal("General must be hidden once, not on every start")
	}
}

func TestSetupDoesNotHideGeneralOnExistingGroup(t *testing.T) {
	g, api, st := setup(t)
	_ = st.Set(context.Background(), keyProfile, "1") // set up by an older version
	g.Setup(context.Background())
	if api.GeneralHidden {
		t.Fatal("a group set up before must keep General as the user left it")
	}
}

func TestSetupKeepsUserProfile(t *testing.T) {
	g, api, _ := setup(t)
	api.GroupPhoto, api.GroupDescription = []byte{9}, "mine"
	g.Setup(context.Background())
	if api.GroupPhoto[0] != 9 || api.GroupDescription != "mine" {
		t.Fatal("user profile overwritten")
	}
}

func TestMissingRightsNoticeOnce(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	api.Perms.PinMessages, api.Perms.ChangeInfo = false, false
	g.Setup(ctx)
	g.Setup(ctx)
	n := 0
	for _, m := range api.Messages(g.Topics.Control()) {
		if strings.Contains(m.HTML, "Закрепление сообщений") && strings.Contains(m.HTML, "Изменение профиля группы") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("notices: %d", n)
	}
	if api.GroupDescription != "" {
		t.Fatal("profile set without can_change_info")
	}
}

func TestNoticeNamesDeleteRight(t *testing.T) {
	g, api, _ := setup(t)
	api.Perms.DeleteMessages = false
	g.Setup(context.Background())
	if m := lastMessage(g, api); !strings.Contains(m.HTML, "Удаление сообщений") {
		t.Fatalf("notice: %s", m.HTML)
	}
}

func lastMessage(g *Group, api *telegram.Fake) telegram.FakeMessage {
	msgs := api.Messages(g.Topics.Control())
	if len(msgs) == 0 {
		return telegram.FakeMessage{}
	}
	return msgs[len(msgs)-1]
}

// rightsFail fails Rights; everything else is the Fake.
type rightsFail struct{ *telegram.Fake }

func (rightsFail) Rights(context.Context) (telegram.Rights, error) {
	return telegram.Rights{}, errors.New("network down")
}

func TestSetupWithoutRightsStillHidesGeneralAndLoadsIcons(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	g.API = rightsFail{api}
	g.Setup(ctx)
	if !api.GeneralHidden {
		t.Fatal("General not hidden")
	}
	id, _ := g.Topics.CreateSession(ctx, "demo", "x")
	if api.Topic(id).Icon != "i-active" {
		t.Fatal("icons not loaded")
	}
	if api.GroupDescription != "" {
		t.Fatal("profile set without known rights")
	}
}

// editFail fails every edit with a transient error.
type editFail struct{ *telegram.Fake }

func (editFail) EditMessage(context.Context, int, string, telegram.Keyboard) error {
	return errors.New("Bad Gateway")
}

func TestCardEditErrorDoesNotSendNewCard(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	if err := g.EnsureCard(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(api.Messages(g.Topics.Control()))
	g.API = editFail{api}
	if err := g.EnsureCard(ctx); err == nil {
		t.Fatal("edit error must be returned")
	}
	if n := len(api.Messages(g.Topics.Control())); n != before || len(api.Pinned()) != 1 {
		t.Fatalf("new card sent: messages %d->%d pinned %v", before, n, api.Pinned())
	}
}

func TestCardRecreatedOnce(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	if err := g.EnsureCard(ctx); err != nil {
		t.Fatal(err)
	}
	_ = api.DeleteMessage(ctx, api.Pinned()[0])
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = g.EnsureCard(ctx) }()
	}
	wg.Wait()
	if p := api.Pinned(); len(p) != 2 {
		t.Fatalf("pinned: %v", p)
	}
}

func TestCardRecreatesDeletedControlTopic(t *testing.T) {
	g, api, _ := setup(t)
	ctx := context.Background()
	if err := g.EnsureCard(ctx); err != nil {
		t.Fatal(err)
	}
	old := g.Topics.Control()
	api.DeleteTopic(old) // the user deleted the control topic
	if err := g.EnsureCard(ctx); err != nil {
		t.Fatalf("card after the topic was deleted: %v", err)
	}
	now := g.Topics.Control()
	if now == old || api.Topic(now).ID == 0 {
		t.Fatalf("control topic not re-created: old %d now %d", old, now)
	}
	msgs := api.Messages(now)
	if len(msgs) != 1 || !strings.Contains(msgs[0].HTML, "Открытых сессий") {
		t.Fatalf("card in the new topic: %+v", msgs)
	}
	if p := api.Pinned(); p[len(p)-1] != msgs[0].ID {
		t.Fatalf("new card not pinned: %v", p)
	}
	if err := g.EnsureCard(ctx); err != nil || len(api.Messages(now)) != 1 {
		t.Fatalf("next refresh must edit the new card: err %v, messages %d", err, len(api.Messages(now)))
	}
}

func TestCardPinnedOnceAndRecreated(t *testing.T) {
	g, api, st := setup(t)
	ctx := context.Background()
	clock := time.Now()
	g.Now = func() time.Time { return clock }
	if err := g.EnsureCard(ctx); err != nil {
		t.Fatal(err)
	}
	if err := g.EnsureCard(ctx); err != nil { // restart: reuse the stored card
		t.Fatal(err)
	}
	if p := api.Pinned(); len(p) != 1 {
		t.Fatalf("pinned: %v", p)
	}
	_ = st.CreateSession(ctx, &store.SessionRow{ThreadID: 7, Project: "p", Cwd: "/w", Title: "t", State: store.StateRunning})
	if err := g.RefreshCard(ctx); err != nil {
		t.Fatal(err)
	}
	msgs := api.Messages(g.Topics.Control())
	last := msgs[len(msgs)-1]
	if !strings.Contains(last.HTML, "Открытых сессий: 1") || last.Keyboard[0][0].Data != "cl:ask" {
		t.Fatalf("card: %+v", last)
	}
	_ = api.DeleteMessage(ctx, api.Pinned()[0]) // the user deleted the card
	clock = clock.Add(2 * time.Minute)
	if err := g.RefreshCard(ctx); err != nil {
		t.Fatal(err)
	}
	if p := api.Pinned(); len(p) != 2 {
		t.Fatalf("card not re-created: %v", p)
	}
}
