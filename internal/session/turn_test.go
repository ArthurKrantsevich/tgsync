package session

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

// gitProject is project() with the files committed.
func gitProject(t *testing.T, fs map[string]string) string {
	t.Helper()
	dir := project(t, fs)
	// core.autocrlf=false keeps contents byte-exact on Windows runners.
	for _, args := range [][]string{{"init", "-q"}, {"config", "core.autocrlf", "false"}, {"add", "-A"}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-qm", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// agentTurn emits writes of names and ends the turn.
func agentTurn(s *agent.FakeSession, dir string, names ...string) {
	for _, n := range names {
		s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(dir, n)}})
	}
	s.Emit(result())
}

// press waits for a button whose text starts with label and presses it.
func pressTurn(t *testing.T, e *env, thread int, label string) string {
	t.Helper()
	var btn telegram.Button
	testutil.Eventually(t, "button "+label, func() bool { var ok bool; btn, ok = e.api.Button(thread, label); return ok })
	msgID := 0 // the message that carries the button, as Telegram reports it
	for _, m := range e.api.Messages(thread) {
		for _, row := range m.Keyboard {
			for _, b := range row {
				if b == btn {
					msgID = m.ID
				}
			}
		}
	}
	alert, handled := e.m.HandleButton(context.Background(), telegram.Update{ThreadID: thread, MessageID: msgID, CallbackData: btn.Data})
	if !handled {
		t.Fatalf("button %s not handled", label)
	}
	return alert
}

func TestTurnSummaryStatsAndDiff(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "one\ntwo\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "one\n2\nthree\n")
	write(t, dir, "b.go", "x\n")
	agentTurn(s, dir, "a.go", "b.go")
	e.hasMessage(t, thread, "Изменено за ход: 2 · +3 −1")
	e.hasMessage(t, thread, "<code>a.go</code> +2 −1")
	e.hasMessage(t, thread, "<code>b.go</code> +1 (новый)")
	if alert := pressTurn(t, e, thread, "🔀 Diff"); alert != "" {
		t.Fatalf("alert %q", alert)
	}
	testutil.Eventually(t, "turn diff", func() bool {
		for _, d := range e.api.Documents(thread) {
			if d.Name == "turn-1.diff" && strings.Contains(string(d.Data), "+three") && strings.Contains(string(d.Data), "+x") {
				return true
			}
		}
		return false
	})
}

func TestTurnSummaryOutsideGitUnchanged(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	agentTurn(e.session(t, 0), dir, "a.go")
	e.hasMessage(t, thread, "Изменено за ход: 1")
	for _, label := range []string{"🔀 Diff", "✅ Коммит", "↩ Откатить"} {
		if _, ok := e.api.Button(thread, label); ok {
			t.Fatalf("%s must not be shown outside git", label)
		}
	}
}

func TestTurnCommitButton(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "b\n")
	agentTurn(s, dir, "a.go")
	if alert := pressTurn(t, e, thread, "✅ Коммит"); alert != "Задача на коммит отправлена" {
		t.Fatalf("alert %q", alert)
	}
	testutil.Eventually(t, "commit prompt", func() bool {
		sent := s.Sent()
		return len(sent) == 2 && strings.HasPrefix(sent[1], "Commit the changes of the previous turn: a.go.") &&
			strings.Contains(sent[1], "git diff ") && strings.Contains(sent[1], "ask the user")
	})
	if _, ok := e.api.Button(thread, "✅ Коммит"); ok {
		t.Fatal("commit button must be gone after the press")
	}
}

func TestTurnRollback(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "old\n"})
	write(t, dir, "a.go", "mine\n") // the user's uncommitted work survives the rollback
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "agent\n")
	write(t, dir, "n.go", "new\n")
	agentTurn(s, dir, "a.go", "n.go")
	pressTurn(t, e, thread, "↩ Откатить")
	e.hasMessage(t, thread, "↩ Откатить 2 файла к началу хода? Новые файлы будут удалены.")
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "agent\n" {
		t.Fatal("nothing may change before the confirmation")
	}
	pressTurn(t, e, thread, "Да, откатить")
	e.hasMessage(t, thread, "↩ Откачено 2 файла")
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "mine\n" {
		t.Fatalf("a.go = %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "n.go")); !os.IsNotExist(err) {
		t.Fatal("n.go must be removed")
	}
	if _, ok := e.api.Button(thread, "↩ Откатить"); ok {
		t.Fatal("summary buttons must be removed after the rollback")
	}
	_ = e.m.Message(ctx, thread, "дальше")
	testutil.Eventually(t, "rollback note", func() bool {
		sent := s.Sent()
		return len(sent) == 2 && sent[1] == "(The user rolled back the previous turn's changes in these files: a.go, n.go.)\n\nдальше"
	})
}

func TestTurnRollbackCancelAndStale(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "old\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "agent\n")
	agentTurn(s, dir, "a.go")
	var old telegram.Button
	testutil.Eventually(t, "rollback button", func() bool { var ok bool; old, ok = e.api.Button(thread, "↩ Откатить"); return ok })
	pressTurn(t, e, thread, "↩ Откатить")
	pressTurn(t, e, thread, "Нет")
	e.hasMessage(t, thread, "Откат отменён.")
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "agent\n" {
		t.Fatal("cancel must not change files")
	}
	_ = e.m.Message(ctx, thread, "next") // a new turn makes the old buttons stale
	alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: old.Data})
	if alert != "Кнопка устарела" {
		t.Fatalf("alert %q", alert)
	}
}

func TestTurnRollbackKeepsEditsAfterTheTurn(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "old\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "agent\n")
	agentTurn(s, dir, "a.go")
	e.hasMessage(t, thread, "Изменено за ход: 1 · +1 −1")
	write(t, dir, "a.go", "user after the turn\n")
	pressTurn(t, e, thread, "↩ Откатить")
	pressTurn(t, e, thread, "Да, откатить")
	e.hasMessage(t, thread, "не тронуты, изменены после хода: a.go")
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "user after the turn\n" {
		t.Fatalf("a.go = %q", b)
	}
}

func TestTurnButtonsWaitForQueuedTurn(t *testing.T) {
	e, ctx := newEnv(t, 1), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "old\n"})
	other := project(t, map[string]string{"b.go": "b\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "agent\n")
	agentTurn(s, dir, "a.go")
	e.hasMessage(t, thread, "Изменено за ход")
	_, _ = e.m.New(ctx, "other", other, "busy") // takes the only slot
	e.session(t, 1)
	_ = e.m.Message(ctx, thread, "next") // queued behind it
	if alert := pressTurn(t, e, thread, "↩ Откатить"); alert != "Агент работает — дождись конца хода" {
		t.Fatalf("alert %q", alert)
	}
}

func TestTurnDiffSkipsProtectedFiles(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n", ".env": "TOKEN=old\n"})
	e.m.d.Protected = []string{filepath.Join(dir, ".env")}
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "b\n")
	write(t, dir, ".env", "TOKEN=secret\n")
	agentTurn(s, dir, "a.go", ".env")
	pressTurn(t, e, thread, "🔀 Diff")
	testutil.Eventually(t, "turn diff", func() bool {
		for _, d := range e.api.Documents(thread) {
			if d.Name == "turn-1.diff" {
				if strings.Contains(string(d.Data), "secret") {
					t.Fatal("protected file leaked into the diff")
				}
				return strings.Contains(string(d.Data), "+b")
			}
		}
		return false
	})
}

func TestTurnCommitOnce(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "b\n")
	agentTurn(s, dir, "a.go")
	var btn telegram.Button
	testutil.Eventually(t, "commit button", func() bool { var ok bool; btn, ok = e.api.Button(thread, "✅ Коммит"); return ok })
	done := make(chan string, 2)
	for i := 0; i < 2; i++ { // a double tap arrives as two concurrent callbacks
		go func() {
			alert, _ := e.m.HandleButton(ctx, telegram.Update{ThreadID: thread, CallbackData: btn.Data})
			done <- alert
		}()
	}
	<-done
	<-done
	time.Sleep(100 * time.Millisecond)
	n := 0
	for _, p := range s.Sent() {
		if strings.HasPrefix(p, "Commit the changes") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("commit prompts: %d", n)
	}
}

func TestTurnCounterKeptWhenSendFails(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	agentTurn(s, dir)
	e.sessionState(t, thread, "idle")
	_ = s.Close() // the next Send fails
	_ = e.m.Message(ctx, thread, "next")
	e.m.mu.Lock()
	no := e.m.sessions[thread].turnNo
	e.m.mu.Unlock()
	if no != 1 {
		t.Fatalf("turnNo = %d: a turn that never reached the agent must not count", no)
	}
}

func TestResultPanelCollapsed(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"a.go": "a\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "go")
	agentTurn(e.session(t, 0), dir)
	testutil.Eventually(t, "⋯ button", func() bool { _, ok := e.api.Button(thread, "⋯"); return ok })
	if _, ok := e.api.Button(thread, "✖ Закрыть"); ok {
		t.Fatal("the panel must start collapsed")
	}
	pressTurn(t, e, thread, "⋯")
	for _, label := range []string{"📂 Файлы", "⚙ Режим", "✖ Закрыть"} {
		if _, ok := e.api.Button(thread, label); !ok {
			t.Fatalf("%s missing after ⋯", label)
		}
	}
}

func TestSummaryHasOneButtonRow(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n", "b.go": "b\n"})
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "x\n")
	write(t, dir, "b.go", "y\n")
	agentTurn(s, dir, "a.go", "b.go")
	e.hasMessage(t, thread, "Изменено за ход: 2")
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "Изменено за ход") {
			if len(m.Keyboard) != 1 || len(m.Keyboard[0]) != 3 || m.Keyboard[0][0].Text != "🔀 Diff" {
				t.Fatalf("keyboard: %+v", m.Keyboard)
			}
		}
	}
}

func TestMarkdownSentWithoutPreview(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"docs/plan.md": "# Plan\n\nSecret body"})
	thread, _ := e.m.New(ctx, "demo", dir, "plan")
	s := e.session(t, 0)
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(dir, "docs/plan.md")}})
	s.Emit(agent.Event{Kind: agent.EventText, Text: "See docs/plan.md"})
	s.Emit(result())
	testutil.Eventually(t, "document", func() bool { return len(e.api.Documents(thread)) == 1 })
	time.Sleep(100 * time.Millisecond)
	for _, m := range e.api.Messages(thread) {
		if strings.Contains(m.HTML, "Secret body") {
			t.Fatalf("the .md is sent as a document only, no text preview: %q", m.HTML)
		}
	}
}

func TestAttachRefusesSecondTopicForSameSession(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := project(t, map[string]string{"a.go": "a\n"})
	if _, err := e.m.Attach(ctx, "demo", dir, "claude-1", "t", false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.m.Attach(ctx, "demo", dir, "claude-1", "t", false); !errors.Is(err, ErrAlreadyOpen) {
		t.Fatalf("second attach: %v", err)
	}
	if _, err := e.m.Attach(ctx, "demo", dir, "claude-1", "t", true); err != nil {
		t.Fatalf("a fork gets its own session: %v", err)
	}
}

// TestSnapshotFailureToldOnce: a turn whose git snapshot fails loses its
// statistics and buttons; the user hears why once, not after every turn.
func TestSnapshotFailureToldOnce(t *testing.T) {
	e, ctx := newEnv(t, 3), context.Background()
	dir := gitProject(t, map[string]string{"a.go": "a\n"})
	write(t, dir, ".git/index", "garbage") // every snapshot fails now
	thread, _ := e.m.New(ctx, "demo", dir, "edit")
	s := e.session(t, 0)
	write(t, dir, "a.go", "b\n")
	agentTurn(s, dir, "a.go")
	e.hasMessage(t, thread, "Изменено за ход: 1")
	e.hasMessage(t, thread, "git-снимок")
	_ = e.m.Message(ctx, thread, "again")
	testutil.Eventually(t, "second turn", func() bool { return len(s.Sent()) == 2 })
	write(t, dir, "a.go", "c\n")
	agentTurn(s, dir, "a.go")
	count := func(substr string) int {
		n := 0
		for _, m := range e.api.Messages(thread) {
			if strings.Contains(m.HTML, substr) {
				n++
			}
		}
		return n
	}
	testutil.Eventually(t, "second summary", func() bool { return count("Изменено за ход") == 2 })
	if n := count("git-снимок"); n != 1 {
		t.Fatalf("snapshot notes: %d, want 1", n)
	}
}
