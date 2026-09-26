package telegram

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
)

func TestDownloadFileTimesOut(t *testing.T) {
	stall := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			_, _ = w.Write([]byte(`{"ok":true,"result":{"file_id":"f1","file_path":"docs/f1"}}`))
			return
		}
		<-stall // the file never comes
	}))
	defer srv.Close()
	defer close(stall)

	old := downloadTimeout
	downloadTimeout = 200 * time.Millisecond
	defer func() { downloadTimeout = old }()
	b, err := newBot("123:abc", chat, func(context.Context, Update) {}, bot.WithServerURL(srv.URL), bot.WithSkipGetMe())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := b.DownloadFile(context.Background(), "f1"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stalled download must fail")
		}
		if strings.Contains(err.Error(), "123:abc") {
			t.Fatalf("error leaks the token: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a stalled download must time out instead of blocking the topic queue")
	}
}
