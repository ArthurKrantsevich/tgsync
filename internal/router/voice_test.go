package router

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

type fakeSTT struct {
	text string
	err  error

	mu  sync.Mutex
	got []string // "name:data" per call
}

func (s *fakeSTT) Transcribe(_ context.Context, audio io.Reader, name string) (string, error) {
	data, _ := io.ReadAll(audio)
	s.mu.Lock()
	s.got = append(s.got, name+":"+string(data))
	s.mu.Unlock()
	return s.text, s.err
}

func (s *fakeSTT) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.got...)
}

func (f *fx) voice(thread int, v telegram.Voice, caption string) {
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, Voice: &v, Text: caption})
}

func (f *fx) lastIn(t *testing.T, thread int) string {
	t.Helper()
	msgs := f.api.Messages(thread)
	if len(msgs) == 0 {
		t.Fatal("no messages")
	}
	return msgs[len(msgs)-1].HTML
}

func newVoiceSession(t *testing.T, s *fakeSTT) (*fx, int) {
	t.Helper()
	f := setup(t)
	f.r.STT, f.r.STTMaxSeconds = s, 300
	f.send(f.r.Topics.Control(), "/new demo")
	thread := f.sessionThread(t)
	f.api.AddFile("v1", []byte("OGG"))
	return f, thread
}

func TestVoiceGoesToAgent(t *testing.T) {
	s := &fakeSTT{text: "почини <b>тесты</b>"}
	f, thread := newVoiceSession(t, s)
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 4}, "срочно")
	if c := s.calls(); len(c) != 1 || c[0] != "voice.ogg:OGG" {
		t.Fatalf("stt calls %v", c)
	}
	var status string
	for _, m := range f.api.Messages(thread) {
		if strings.Contains(m.HTML, "Распознано") {
			status = m.HTML
		}
	}
	if !strings.Contains(status, "<blockquote expandable>") || !strings.Contains(status, "почини &lt;b&gt;тесты&lt;/b&gt;") {
		t.Fatalf("status %q", status)
	}
	testutil.Eventually(t, "agent got the voice prompt", func() bool {
		ss := f.ag.Sessions()
		if len(ss) != 1 || len(ss[0].Sent()) != 1 {
			return false
		}
		p := ss[0].Sent()[0]
		return strings.Contains(p, "[Voice message") && strings.Contains(p, "почини <b>тесты</b>") &&
			strings.Contains(p, "[Caption: срочно]") && strings.Contains(p, "wait for my confirmation")
	})
}

// assertNothingSent: after a failed voice, the next text is the agent's only message.
func assertNothingSent(t *testing.T, f *fx, thread int) {
	t.Helper()
	f.send(thread, "ping")
	testutil.Eventually(t, "only ping reached the agent", func() bool {
		ss := f.ag.Sessions()
		return len(ss) == 1 && len(ss[0].Sent()) == 1 && ss[0].Sent()[0] == "ping"
	})
}

func TestVoiceSTTError(t *testing.T) {
	f, thread := newVoiceSession(t, &fakeSTT{err: errors.New("connection refused")})
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 4}, "")
	if got := f.lastIn(t, thread); !strings.Contains(got, "STT: connection refused") {
		t.Fatalf("status %q", got)
	}
	assertNothingSent(t, f, thread)
}

func TestVoiceEmptyTranscript(t *testing.T) {
	f, thread := newVoiceSession(t, &fakeSTT{text: ""})
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 4}, "")
	if got := f.lastIn(t, thread); !strings.Contains(got, "Не расслышал") {
		t.Fatalf("status %q", got)
	}
	assertNothingSent(t, f, thread)
}

func TestVoiceDownloadError(t *testing.T) {
	s := &fakeSTT{text: "x"}
	f, thread := newVoiceSession(t, s)
	f.voice(thread, telegram.Voice{ID: "missing", Name: "voice.ogg", Duration: 4}, "")
	if got := f.lastIn(t, thread); !strings.Contains(got, "STT:") || len(s.calls()) != 0 {
		t.Fatalf("status %q calls %v", got, s.calls())
	}
}

func TestVoiceDisabled(t *testing.T) {
	f := setup(t) // f.r.STT stays nil
	f.send(f.r.Topics.Control(), "/new demo")
	thread := f.sessionThread(t)
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 4}, "")
	if got := f.lastIn(t, thread); !strings.Contains(got, "не настроено") {
		t.Fatalf("reply %q", got)
	}
}

func TestVoiceTooLong(t *testing.T) {
	s := &fakeSTT{text: "x"}
	f, thread := newVoiceSession(t, s)
	f.r.STTMaxSeconds = 10
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 11}, "")
	if got := f.lastIn(t, thread); !strings.Contains(got, "Слишком длинное") || len(s.calls()) != 0 {
		t.Fatalf("reply %q calls %v", got, s.calls())
	}
}

func TestVoiceOutsideSession(t *testing.T) {
	s := &fakeSTT{text: "x"}
	f := setup(t)
	f.r.STT = s
	f.voice(f.r.Topics.Control(), telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 4}, "")
	if got := f.lastIn(t, f.r.Topics.Control()); !strings.Contains(got, "в теме сессии") || len(s.calls()) != 0 {
		t.Fatalf("reply %q", got)
	}
}

func TestVoiceLongTranscriptFitsTelegram(t *testing.T) {
	long := strings.Repeat("слово ", 1000)
	f, thread := newVoiceSession(t, &fakeSTT{text: long})
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 290}, "")
	var status string
	for _, m := range f.api.Messages(thread) {
		if strings.Contains(m.HTML, "Распознано") {
			status = m.HTML
		}
	}
	if n := len([]rune(status)); n == 0 || n > 4096 || !strings.Contains(status, "…") {
		t.Fatalf("status must be cut to fit 4096 runes with …, got %d runes", n)
	}
	testutil.Eventually(t, "agent got the full transcript", func() bool {
		ss := f.ag.Sessions()
		return len(ss) == 1 && len(ss[0].Sent()) == 1 && strings.Contains(ss[0].Sent()[0], strings.TrimSpace(long))
	})
}

func TestVoiceFirstMessageNamesTopic(t *testing.T) {
	f, thread := newVoiceSession(t, &fakeSTT{text: "почини тесты"})
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 4}, "")
	testutil.Eventually(t, "topic named after the transcript", func() bool {
		name := f.api.Topic(thread).Name
		return strings.Contains(name, "почини") && !strings.Contains(name, "[Voice")
	})
}

func TestVoiceAnswersPendingQuestion(t *testing.T) {
	f := setup(t)
	f.r.STT = &fakeSTT{text: "Green, please"}
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	f.api.AddFile("v1", []byte("OGG"))
	testutil.Eventually(t, "agent started", func() bool { return len(f.ag.Sessions()) == 1 })
	s := f.ag.Sessions()[0]
	done := make(chan agent.PermissionDecision, 1)
	go func() {
		done <- s.Ask(agent.PermissionRequest{ToolName: "AskUserQuestion", Input: map[string]any{"questions": []any{
			map[string]any{"question": "Color?", "options": []any{map[string]any{"label": "Red"}}},
		}}})
	}()
	f.press(t, thread, "✍")
	f.voice(thread, telegram.Voice{ID: "v1", Name: "voice.ogg", Duration: 2}, "")
	select {
	case d := <-done:
		a, _ := d.UpdatedInput["answers"].(map[string]any)
		if !d.Allow || a["Color?"] != "Green, please" {
			t.Fatalf("decision %+v", d)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("voice did not answer the pending question")
	}
	for _, m := range s.Sent() {
		if strings.Contains(m, "[Voice") {
			t.Fatal("an answer must not also go to the agent as a voice prompt")
		}
	}
}
