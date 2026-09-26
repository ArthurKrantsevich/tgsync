package stt

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestTranscribeSendsMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/audio/transcriptions" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		if got := r.FormValue("model"); got != "m1" {
			t.Errorf("model %q", got)
		}
		if got := r.FormValue("response_format"); got != "json" {
			t.Errorf("response_format %q", got)
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(f)
		if h.Filename != "voice.ogg" || string(data) != "OGG" {
			t.Errorf("file %q %q", h.Filename, data)
		}
		_, _ = w.Write([]byte(`{"text":"  привет мир \n"}`))
	}))
	defer srv.Close()
	c := &Client{URL: srv.URL + "/", Model: "m1"}
	got, err := c.Transcribe(context.Background(), strings.NewReader("OGG"), "voice.ogg")
	if err != nil || got != "привет мир" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestTranscribeHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(strings.Repeat("x", 5000)))
	}))
	defer srv.Close()
	_, err := (&Client{URL: srv.URL}).Transcribe(context.Background(), strings.NewReader("a"), "a.ogg")
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("err %v", err)
	}
	if n := len([]rune(err.Error())); n > 300 {
		t.Fatalf("error not truncated: %d runes", n)
	}
}

func TestTranscribeBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()
	if _, err := (&Client{URL: srv.URL}).Transcribe(context.Background(), strings.NewReader("a"), "a.ogg"); err == nil {
		t.Fatal("want error for malformed JSON")
	}
}

func TestTranscribeContextTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (&Client{URL: srv.URL}).Transcribe(ctx, strings.NewReader("a"), "a.ogg"); err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not honoured")
	}
}

// TestTranscribeNoContextDeadlineStillBounded covers a stalled STT server with
// STT_TIMEOUT=0 (config.Load lets the per-request context deadline be
// disabled): the caller's ctx has no deadline of its own, so only the
// Client's own http.Client timeout can stop it from hanging forever.
func TestTranscribeNoContextDeadlineStillBounded(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(block)
	c := &Client{URL: srv.URL, Timeout: 50 * time.Millisecond}
	start := time.Now()
	if _, err := c.Transcribe(context.Background(), strings.NewReader("a"), "a.ogg"); err == nil {
		t.Fatal("want timeout error")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("Client.Timeout not honoured without a context deadline")
	}
}
