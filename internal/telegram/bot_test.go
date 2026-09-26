package telegram

import (
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/go-telegram/bot"

	"github.com/go-telegram/bot/models"
)

const chat = int64(-100123)

func TestNormalizeMessage(t *testing.T) {
	u, ok := normalize(&models.Update{Message: &models.Message{
		ID: 7, Chat: models.Chat{ID: chat}, From: &models.User{ID: 5},
		Text: "/new demo", IsTopicMessage: true, MessageThreadID: 42,
	}}, chat)
	if !ok || u.UserID != 5 || u.ThreadID != 42 || u.MessageID != 7 || u.Text != "/new demo" {
		t.Fatalf("update: %+v ok=%v", u, ok)
	}
}

func TestNormalizeGeneralTopicHasZeroThread(t *testing.T) {
	u, ok := normalize(&models.Update{Message: &models.Message{
		Chat: models.Chat{ID: chat}, From: &models.User{ID: 5}, Text: "x", MessageThreadID: 1,
	}}, chat)
	if !ok || u.ThreadID != 0 {
		t.Fatalf("update: %+v", u)
	}
}

func TestNormalizeRejects(t *testing.T) {
	cases := map[string]*models.Update{
		"other chat": {Message: &models.Message{Chat: models.Chat{ID: -1}, From: &models.User{ID: 5}}},
		"bot author": {Message: &models.Message{Chat: models.Chat{ID: chat}, From: &models.User{ID: 5, IsBot: true}}},
		"no author":  {Message: &models.Message{Chat: models.Chat{ID: chat}}},
		"empty":      {},
	}
	for name, u := range cases {
		if _, ok := normalize(u, chat); ok {
			t.Errorf("%s: must be rejected", name)
		}
	}
}

func TestNormalizeCallback(t *testing.T) {
	u, ok := normalize(&models.Update{CallbackQuery: &models.CallbackQuery{
		ID: "cb1", From: models.User{ID: 5}, Data: "p:1:a",
		Message: models.MaybeInaccessibleMessage{Message: &models.Message{
			ID: 9, Chat: models.Chat{ID: chat}, IsTopicMessage: true, MessageThreadID: 42,
		}},
	}}, chat)
	if !ok || u.CallbackID != "cb1" || u.CallbackData != "p:1:a" || u.ThreadID != 42 || u.UserID != 5 {
		t.Fatalf("update: %+v ok=%v", u, ok)
	}
}

func TestPlainText(t *testing.T) {
	if got := plainText(`<b>a &lt; b</b> <a href="x">link</a>`); got != "a < b link" {
		t.Fatalf("got %q", got)
	}
}

func TestTopicGoneErrors(t *testing.T) {
	for _, msg := range []string{"Bad Request: message thread not found", "Bad Request: TOPIC_ID_INVALID", "Bad Request: TOPIC_DELETED"} {
		if err := mapErr(errors.New(msg)); !errors.Is(err, ErrTopicGone) {
			t.Errorf("%q must map to ErrTopicGone", msg)
		}
	}
	if err := mapErr(errors.New("Too Many Requests")); errors.Is(err, ErrTopicGone) {
		t.Error("unrelated error must not map")
	}
	if mapErr(nil) != nil {
		t.Error("nil must stay nil")
	}
}

func TestMessageGoneErrors(t *testing.T) {
	for _, msg := range []string{"Bad Request: message to edit not found", "Bad Request: MESSAGE_ID_INVALID"} {
		if err := mapErr(errors.New(msg)); !errors.Is(err, ErrMessageGone) {
			t.Errorf("%q must map to ErrMessageGone", msg)
		}
	}
	if err := mapErr(errors.New("Bad Gateway")); errors.Is(err, ErrMessageGone) {
		t.Error("unrelated error must not map")
	}
}

func TestRetryableErrors(t *testing.T) {
	var re *RetryError
	if err := mapErr(&bot.TooManyRequestsError{Message: "Too Many Requests", RetryAfter: 12}); !errors.As(err, &re) || re.After != 12*time.Second {
		t.Fatalf("429: %v", err)
	}
	if err := mapErr(&url.Error{Op: "Post", URL: "https://api.telegram.org", Err: errors.New("connection refused")}); !errors.As(err, &re) {
		t.Fatalf("network: %v", err)
	}
}

func TestNormalizeAttachments(t *testing.T) {
	u, ok := normalize(&models.Update{Message: &models.Message{
		ID: 3, Chat: models.Chat{ID: chat}, From: &models.User{ID: 5}, IsTopicMessage: true, MessageThreadID: 42,
		Caption:  "посмотри лог",
		Document: &models.Document{FileID: "doc1", FileName: "app.log", FileSize: 1234},
	}}, chat)
	if !ok || u.File == nil || u.File.ID != "doc1" || u.File.Name != "app.log" || u.File.Size != 1234 || u.Text != "посмотри лог" {
		t.Fatalf("document: %+v %+v", u, u.File)
	}
	u, _ = normalize(&models.Update{Message: &models.Message{
		Chat: models.Chat{ID: chat}, From: &models.User{ID: 5},
		Photo: []models.PhotoSize{{FileID: "small", FileSize: 10, Width: 90}, {FileID: "big", FileSize: 900, Width: 1280}},
	}}, chat)
	if u.File == nil || u.File.ID != "big" || u.File.Name != "photo.jpg" || u.Text != "" {
		t.Fatalf("photo: %+v", u.File)
	}
}

func TestNormalizeVoice(t *testing.T) {
	u, ok := normalize(&models.Update{Message: &models.Message{
		ID: 3, Chat: models.Chat{ID: chat}, From: &models.User{ID: 5}, Caption: "срочно",
		Voice: &models.Voice{FileID: "v1", Duration: 12, FileSize: 4000, MimeType: "audio/ogg"},
	}}, chat)
	if !ok || u.Voice == nil || u.File != nil {
		t.Fatalf("update: %+v ok=%v", u, ok)
	}
	if *u.Voice != (Voice{ID: "v1", Name: "voice.ogg", Size: 4000, Duration: 12}) || u.Text != "срочно" {
		t.Fatalf("voice: %+v text=%q", *u.Voice, u.Text)
	}
}

func TestNormalizeAudio(t *testing.T) {
	cases := map[string]string{"memo.m4a": "memo.m4a", "": "audio.mp3"}
	for fileName, want := range cases {
		u, ok := normalize(&models.Update{Message: &models.Message{
			Chat: models.Chat{ID: chat}, From: &models.User{ID: 5},
			Audio: &models.Audio{FileID: "a1", Duration: 30, FileName: fileName},
		}}, chat)
		if !ok || u.Voice == nil || u.Voice.Name != want || u.Voice.Duration != 30 || u.Voice.ID != "a1" {
			t.Fatalf("%q: %+v", fileName, u.Voice)
		}
	}
}
