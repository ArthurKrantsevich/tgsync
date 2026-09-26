package router

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// Tests assert the Russian texts; English ones switch the language locally.
func TestMain(m *testing.M) {
	i18n.Set(i18n.RU)
	os.Exit(m.Run())
}

func english(t *testing.T) {
	t.Helper()
	i18n.Set(i18n.EN)
	t.Cleanup(func() { i18n.Set(i18n.RU) })
}

func TestEnglishTexts(t *testing.T) {
	english(t)
	f := setup(t)
	control := f.r.Topics.Control()
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: control, Text: "/cancel"})
	msgs := f.api.Messages(control)
	if len(msgs) == 0 || msgs[len(msgs)-1].HTML != "Canceled." {
		t.Fatalf("messages: %+v", msgs)
	}
	if got := ago(3 * time.Hour); got != "3 hours ago" {
		t.Errorf("ago = %q", got)
	}
	if got := ago(24 * time.Hour); got != "1 day ago" {
		t.Errorf("ago = %q", got)
	}
	if c := Commands(); c[0].Description != "Main menu" {
		t.Errorf("command = %+v", c[0])
	}
}

func TestRussianAgo(t *testing.T) {
	if got := ago(5 * time.Minute); got != "5 мин назад" {
		t.Errorf("ago = %q", got)
	}
}
