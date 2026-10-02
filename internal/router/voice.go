package router

import (
	"bytes"
	"context"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
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
		r.reply(ctx, thread, i18n.T("router.voice.control"))
		return
	case !r.Sessions.Owns(thread):
		return
	case v.Size > telegram.MaxDownload:
		r.reply(ctx, thread, i18n.T("router.voice.too_big"))
		return
	// Without a transcript the recording still reaches the agent as a file,
	// e.g. a long audio file sent as a voice sample.
	case r.STT == nil:
		r.reply(ctx, thread, i18n.T("router.voice.no_stt"))
		r.keepAudio(ctx, u)
		return
	case r.STTMaxSeconds > 0 && v.Duration > r.STTMaxSeconds:
		r.reply(ctx, thread, i18n.T("router.voice.too_long", v.Duration, r.STTMaxSeconds))
		r.keepAudio(ctx, u)
		return
	}
	status, err := r.API.SendMessage(ctx, thread, i18n.T("router.voice.working"), nil, true)
	if err != nil {
		r.warn(ctx, thread, err)
		return
	}
	data, text, err := r.transcribe(ctx, v)
	switch {
	case err != nil:
		_ = r.API.EditMessage(ctx, status, "⚠️ STT: "+render.Escape(err.Error()), nil)
		return
	case text == "":
		_ = r.API.EditMessage(ctx, status, i18n.T("router.voice.empty"), nil)
		return
	}
	_ = r.API.EditMessage(ctx, status, i18n.T("router.voice.recognized", render.Escape(cut(text, maxShown))), nil)
	// A "custom answer" is awaited: the voice is the answer, not a new task.
	if r.Broker.HandleText(ctx, thread, text) {
		return
	}
	// The recording stays in the project inbox so the agent can use the
	// audio itself, e.g. as a voice sample; a failed save only loses that.
	audio, err := r.Sessions.StoreFile(thread, v.Name, data)
	if err != nil {
		r.warn(ctx, thread, err)
	}
	r.report(ctx, thread, r.Sessions.MessageFrom(ctx, thread, voicePrompt(text, strings.TrimSpace(u.Text), audio), u.MessageID))
}

// keepAudio stores an untranscribed recording in the project inbox like any
// other file the user sends.
func (r *Router) keepAudio(ctx context.Context, u telegram.Update) {
	data, err := r.API.DownloadFile(ctx, u.Voice.ID)
	if err != nil {
		r.warn(ctx, u.ThreadID, err)
		return
	}
	r.report(ctx, u.ThreadID, r.Sessions.ReceiveFile(ctx, u.ThreadID, u.Voice.Name, data, strings.TrimSpace(u.Text)))
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

// transcribe downloads the recording and returns it with its transcript.
func (r *Router) transcribe(ctx context.Context, v *telegram.Voice) ([]byte, string, error) {
	if r.STTTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.STTTimeout)
		defer cancel()
	}
	data, err := r.API.DownloadFile(ctx, v.ID)
	if err != nil {
		return nil, "", err
	}
	text, err := r.STT.Transcribe(ctx, bytes.NewReader(data), v.Name)
	return data, text, err
}

// voicePrompt wraps a transcript so the agent confirms before acting.
// The transcript comes first: the session names its topic after the first line.
// audio is the saved recording's path in the project, if any.
func voicePrompt(text, caption, audio string) string {
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n[Voice message above — speech recognition may contain errors]")
	if caption != "" {
		b.WriteString("\n[Caption: " + caption + "]")
	}
	if audio != "" {
		b.WriteString("\n[Audio: " + audio + "]")
	}
	b.WriteString("\nBefore doing anything, restate in one or two sentences how you understood the task and wait for my confirmation. Do not start work yet.")
	return b.String()
}
