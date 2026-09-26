// Package stt transcribes audio on a self-hosted speech-to-text server with
// an OpenAI-compatible API (for example speaches / faster-whisper).
package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// defaultTimeout bounds the HTTP call whenever Client has no HTTP client of
// its own and no Timeout configured. It backstops a stalled STT server even
// when the caller's context has no deadline (STT_TIMEOUT=0), so the request
// cannot hang forever.
const defaultTimeout = 2 * time.Minute

// Transcriber turns audio into text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio io.Reader, filename string) (string, error)
}

// Client calls POST {URL}/v1/audio/transcriptions.
type Client struct {
	URL   string // base URL, for example http://127.0.0.1:8000
	Model string
	HTTP  *http.Client // nil means a client built from Timeout (or defaultTimeout)
	// Timeout bounds the HTTP call when HTTP is nil. It is independent of
	// the caller's context: even with STT_TIMEOUT=0 (no context deadline),
	// this stops a stalled server from blocking forever. Zero uses
	// defaultTimeout.
	Timeout time.Duration
}

// Transcribe uploads the audio and returns the trimmed transcript.
func (c *Client) Transcribe(ctx context.Context, audio io.Reader, filename string) (string, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(fw, audio); err != nil {
		return "", err
	}
	if c.Model != "" {
		_ = w.WriteField("model", c.Model)
	}
	_ = w.WriteField("response_format", "json")
	if err := w.Close(); err != nil {
		return "", err
	}
	url := strings.TrimRight(c.URL, "/") + "/v1/audio/transcriptions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	hc := c.HTTP
	if hc == nil {
		timeout := c.Timeout
		if timeout <= 0 {
			timeout = defaultTimeout
		}
		hc = &http.Client{Timeout: timeout}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf(i18n.T("stt.read"), err)
	}
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(strings.TrimSpace(string(raw)), 200))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", fmt.Errorf(i18n.T("stt.bad_json"), err)
	}
	return strings.TrimSpace(out.Text), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
