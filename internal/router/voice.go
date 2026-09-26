package router

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// voice transcribes a voice message on the self-hosted STT server and hands
// the text to the agent, which restates the task and waits for a "yes".
// It runs synchronously so the user's next message reaches the agent after it.
func (r *Router) voice(ctx context.Context, u telegram.Update) {
	thread, v := u.ThreadID, u.Voice
	switch {
	case thread == r.Topics.Control():
		r.reply(ctx, thread, "🎙 Голосовые работают в теме сессии.")
		return
	case !r.Sessions.Owns(thread):
		return
	case r.STT == nil:
		r.reply(ctx, thread, "🎙 Распознавание не настроено (STT_URL).")
		return
	case r.STTMaxSeconds > 0 && v.Duration > r.STTMaxSeconds:
		r.reply(ctx, thread, fmt.Sprintf("🎙 Слишком длинное: %d с, максимум %d с.", v.Duration, r.STTMaxSeconds))
		return
	case v.Size > telegram.MaxDownload:
		r.reply(ctx, thread, "⚠️ Аудио больше 20 МБ — Telegram не даёт ботам скачивать такие.")
		return
	}
	status, err := r.API.SendMessage(ctx, thread, "🎙 Распознаю…", nil, true)
	if err != nil {
		r.warn(ctx, thread, err)
		return
	}
	text, err := r.transcribe(ctx, v)
	switch {
	case err != nil:
		_ = r.API.EditMessage(ctx, status, "⚠️ STT: "+render.Escape(err.Error()), nil)
		return
	case text == "":
		_ = r.API.EditMessage(ctx, status, "🎙 Не расслышал, повтори.", nil)
		return
	}
	_ = r.API.EditMessage(ctx, status, "🎙 Распознано:\n<blockquote expandable>"+render.Escape(cut(text, maxShown))+"</blockquote>", nil)
	// «Свой ответ» is waiting: the voice is the answer, not a new task.
	if r.Broker.HandleText(ctx, thread, text) {
		return
	}
	r.report(ctx, thread, r.Sessions.MessageFrom(ctx, thread, voicePrompt(text, strings.TrimSpace(u.Text)), u.MessageID))
}

// maxShown keeps the transcript message under Telegram's 4096-character limit.
const maxShown = 3500

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (r *Router) transcribe(ctx context.Context, v *telegram.Voice) (string, error) {
	if r.STTTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.STTTimeout)
		defer cancel()
	}
	data, err := r.API.DownloadFile(ctx, v.ID)
	if err != nil {
		return "", err
	}
	return r.STT.Transcribe(ctx, bytes.NewReader(data), v.Name)
}

// voicePrompt wraps a transcript so the agent confirms before acting.
// The transcript comes first: the session names its topic after the first line.
func voicePrompt(text, caption string) string {
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n[Voice message above — speech recognition may contain errors]")
	if caption != "" {
		b.WriteString("\n[Caption: " + caption + "]")
	}
	b.WriteString("\nBefore doing anything, restate in one or two sentences how you understood the task and wait for my confirmation. Do not start work yet.")
	return b.String()
}
