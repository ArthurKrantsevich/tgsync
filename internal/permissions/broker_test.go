package permissions

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

const thread = 50

type fixture struct {
	b     *Broker
	api   *telegram.Fake
	st    *store.Store
	mu    sync.Mutex
	waits []bool
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := telegram.NewFake()
	return &fixture{b: NewBroker(api, st), api: api, st: st}
}

func (f *fixture) can() agent.CanUseToolFunc {
	return f.b.CanUseTool(SessionInfo{ThreadID: thread, Project: "demo", ProjectDir: osPath("/w/demo"),
		OnWait: func(w bool) { f.mu.Lock(); f.waits = append(f.waits, w); f.mu.Unlock() }})
}

// ask runs a request in the background, like the SDK does.
func (f *fixture) ask(ctx context.Context, tool string, input map[string]any) <-chan agent.PermissionDecision {
	out := make(chan agent.PermissionDecision, 1)
	can := f.can()
	go func() { out <- can(ctx, agent.PermissionRequest{ToolName: tool, Input: input}) }()
	return out
}

func (f *fixture) press(t *testing.T, textPrefix string) {
	t.Helper()
	var btn telegram.Button
	testutil.Eventually(t, "button "+textPrefix, func() bool {
		var ok bool
		btn, ok = f.api.Button(thread, textPrefix)
		return ok
	})
	f.b.HandleCallback(context.Background(), telegram.Update{UserID: 1, ThreadID: thread, CallbackID: "cb", CallbackData: btn.Data})
}

// bash runs a Bash request through can in the background: a request that
// unexpectedly waits for a button fails in decision instead of hanging.
func bash(can agent.CanUseToolFunc, cmd string) <-chan agent.PermissionDecision {
	out := make(chan agent.PermissionDecision, 1)
	go func() {
		out <- can(context.Background(), agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": cmd}})
	}()
	return out
}

func decision(t *testing.T, ch <-chan agent.PermissionDecision) agent.PermissionDecision {
	t.Helper()
	select {
	case d := <-ch:
		return d
	case <-time.After(3 * time.Second):
		t.Fatal("no decision")
		return agent.PermissionDecision{}
	}
}

func TestAutoAllowSendsNothing(t *testing.T) {
	f := newFixture(t)
	d := decision(t, f.ask(context.Background(), "Read", map[string]any{"file_path": osPath("/etc/hosts")}))
	if !d.Allow || len(f.api.Messages(thread)) != 0 {
		t.Fatalf("decision=%+v messages=%v", d, f.api.Messages(thread))
	}
}

func TestSudoDeniedWithoutMessage(t *testing.T) {
	f := newFixture(t)
	d := decision(t, f.ask(context.Background(), "Bash", map[string]any{"command": "sudo reboot"}))
	if d.Allow || !strings.Contains(d.Message, "sudo") || len(f.api.Messages(thread)) != 0 {
		t.Fatalf("decision=%+v", d)
	}
}

func TestAllowButton(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "rm -rf build"})
	f.press(t, "✅")
	if d := decision(t, ch); !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	msgs := f.api.Messages(thread)
	if len(msgs) != 1 || msgs[0].Keyboard != nil || !strings.Contains(msgs[0].HTML, "rm -rf build") || !strings.Contains(msgs[0].HTML, "✅ Разрешено") {
		t.Fatalf("message: %+v", msgs)
	}
	if msgs[0].Silent {
		t.Fatal("permission requests must notify")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.waits) != 2 || !f.waits[0] || f.waits[1] {
		t.Fatalf("OnWait calls: %v", f.waits)
	}
}

func TestDenyButton(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "rm -rf build"})
	f.press(t, "❌")
	if d := decision(t, ch); d.Allow || d.Message == "" {
		t.Fatalf("decision: %+v", d)
	}
}

func TestAlwaysSavesRule(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "go test ./..."})
	f.press(t, "♾ Всегда: go test")
	if d := decision(t, ch); !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	d := decision(t, f.ask(context.Background(), "Bash", map[string]any{"command": "go test ./internal/..."}))
	if !d.Allow || len(f.api.Messages(thread)) != 1 {
		t.Fatalf("second call must be auto-allowed: %+v, messages=%d", d, len(f.api.Messages(thread)))
	}
}

func TestAlwaysForWriteCoversOneFolder(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Write", map[string]any{"file_path": osPath("/srv/app/a.conf"), "content": "x"})
	f.press(t, "♾ Всегда: Write в "+osPath("/srv/app"))
	if d := decision(t, ch); !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	if d := decision(t, f.ask(context.Background(), "Write", map[string]any{"file_path": osPath("/srv/app/b.conf")})); !d.Allow {
		t.Fatalf("same folder must be allowed: %+v", d)
	}
	f.ask(context.Background(), "Write", map[string]any{"file_path": osPath("/srv/other/c.conf")})
	testutil.Eventually(t, "prompt for another folder", func() bool { return len(f.api.Messages(thread)) == 2 })
}

func TestNoAlwaysForInterpreters(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "python3 x.py"})
	testutil.Eventually(t, "prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	if _, ok := f.api.Button(thread, "♾"); ok {
		t.Fatal("«Всегда» must not be offered for an interpreter")
	}
	f.press(t, "✅")
	if d := decision(t, ch); !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
}

func TestStaleButton(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "make deploy"})
	var btn telegram.Button
	testutil.Eventually(t, "button", func() bool { var ok bool; btn, ok = f.api.Button(thread, "✅"); return ok })
	up := telegram.Update{UserID: 1, ThreadID: thread, CallbackID: "cb", CallbackData: btn.Data}
	f.b.HandleCallback(context.Background(), up)
	decision(t, ch)
	if !f.b.HandleCallback(context.Background(), up) {
		t.Fatal("stale broker button must still be handled")
	}
	answers := f.api.Answers()
	if answers[len(answers)-1] != "Запрос устарел" {
		t.Fatalf("answers: %v", answers)
	}
	other := up
	other.ThreadID = thread + 1
	other.CallbackData = "p:999:a"
	f.b.HandleCallback(context.Background(), other)
	if a := f.api.Answers(); a[len(a)-1] != "Запрос устарел" {
		t.Fatalf("unknown id: %v", a)
	}
	if f.b.HandleCallback(context.Background(), telegram.Update{CallbackData: "new:demo"}) {
		t.Fatal("foreign callback data must not be handled")
	}
}

func TestCancelledContext(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := f.ask(ctx, "Bash", map[string]any{"command": "make deploy"})
	testutil.Eventually(t, "request message", func() bool { return len(f.api.Messages(thread)) == 1 })
	cancel()
	if d := decision(t, ch); d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	if m := f.api.Messages(thread)[0]; m.Keyboard != nil || !strings.Contains(m.HTML, "Сессия завершена") {
		t.Fatalf("message: %+v", m)
	}
}

func askInput(multi bool, questions ...string) map[string]any {
	var qs []any
	for _, q := range questions {
		qs = append(qs, map[string]any{
			"question": q, "header": "Выбор", "multiSelect": multi,
			"options": []any{map[string]any{"label": "Red"}, map[string]any{"label": "Blue"}},
		})
	}
	return map[string]any{"questions": qs}
}

func answers(t *testing.T, d agent.PermissionDecision) map[string]any {
	t.Helper()
	if !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	a, _ := d.UpdatedInput["answers"].(map[string]any)
	if d.UpdatedInput["questions"] == nil {
		t.Fatal("original input must be kept")
	}
	return a
}

func TestQuestionSingleChoice(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "AskUserQuestion", askInput(false, "Color?", "Shade?"))
	f.press(t, "Blue")
	testutil.Eventually(t, "second question", func() bool { return len(f.api.Messages(thread)) == 2 })
	f.press(t, "Red")
	a := answers(t, decision(t, ch))
	if a["Color?"] != "Blue" || a["Shade?"] != "Red" {
		t.Fatalf("answers: %v", a)
	}
	first := f.api.Messages(thread)[0]
	if first.Keyboard != nil || !strings.Contains(first.HTML, "💬 Blue") {
		t.Fatalf("answered question must show the answer: %+v", first)
	}
}

func TestQuestionDoubleTapDoesNotAnswerNext(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "AskUserQuestion", askInput(false, "Color?", "Shade?"))
	var btn telegram.Button
	testutil.Eventually(t, "button", func() bool { var ok bool; btn, ok = f.api.Button(thread, "Blue"); return ok })
	up := telegram.Update{UserID: 1, ThreadID: thread, CallbackID: "cb", CallbackData: btn.Data}
	f.b.HandleCallback(context.Background(), up)
	f.b.HandleCallback(context.Background(), up)
	testutil.Eventually(t, "second question", func() bool { return len(f.api.Messages(thread)) == 2 })
	if kb := f.api.Messages(thread)[1].Keyboard; kb == nil {
		t.Fatal("second question must still wait for an answer")
	}
	f.press(t, "Red")
	if a := answers(t, decision(t, ch)); a["Shade?"] != "Red" {
		t.Fatalf("answers: %v", a)
	}
}

func TestQuestionMultiSelect(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "AskUserQuestion", askInput(true, "Colors?"))
	f.press(t, "Готово")
	if a := f.api.Answers(); a[len(a)-1] != "Выбери хотя бы один вариант" {
		t.Fatalf("empty selection must be refused: %v", a)
	}
	f.press(t, "☐ Red")
	f.press(t, "☐ Blue")
	testutil.Eventually(t, "both ticked", func() bool {
		_, ok := f.api.Button(thread, "☑ Blue")
		return ok
	})
	f.press(t, "Готово")
	if a := answers(t, decision(t, ch)); a["Colors?"] != "Red, Blue" {
		t.Fatalf("answers: %v", a)
	}
}

func TestQuestionOwnAnswer(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "AskUserQuestion", askInput(false, "Color?"))
	if f.b.HandleText(context.Background(), thread, "too early") {
		t.Fatal("text before «Свой ответ» must not be consumed")
	}
	f.press(t, "✍")
	if !f.b.HandleText(context.Background(), thread, "Green, please") {
		t.Fatal("own answer not consumed")
	}
	if a := answers(t, decision(t, ch)); a["Color?"] != "Green, please" {
		t.Fatalf("answers: %v", a)
	}
}

func TestCancelThreadWithdrawsRequests(t *testing.T) {
	f := newFixture(t)
	perm := f.ask(context.Background(), "Bash", map[string]any{"command": "make deploy"})
	q := f.ask(context.Background(), "AskUserQuestion", askInput(false, "Color?"))
	f.press(t, "✍")
	testutil.Eventually(t, "two prompts", func() bool { return len(f.api.Messages(thread)) == 2 })
	f.b.CancelThread(context.Background(), thread)
	if d := decision(t, perm); d.Allow {
		t.Fatalf("permission must be denied: %+v", d)
	}
	if d := decision(t, q); d.Allow {
		t.Fatalf("question must be denied: %+v", d)
	}
	if f.b.HandleText(context.Background(), thread, "resume please") {
		t.Fatal("text after cancel must go to the agent, not to a dead question")
	}
	for _, m := range f.api.Messages(thread) {
		if m.Keyboard != nil || !strings.Contains(m.HTML, "Запрос снят") {
			t.Fatalf("prompt not withdrawn: %+v", m)
		}
	}
}

type fakeSudo struct {
	mu      sync.Mutex
	grants  map[int]int
	tokens  map[int]string
	revoked []int
}

func (f *fakeSudo) Enabled() bool { return true }
func (f *fakeSudo) Grant(thread int, token string, uses int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.grants[thread] = uses
	if f.tokens == nil {
		f.tokens = map[int]string{}
	}
	f.tokens[thread] = token
}
func (f *fakeSudo) Revoke(thread int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, thread)
}

func TestPromptShowsBashDescription(t *testing.T) {
	f := newFixture(t)
	f.ask(context.Background(), "Bash", map[string]any{"command": "make build", "description": "Собираю бинарник <для> проверки"})
	testutil.Eventually(t, "prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	if m := f.api.Messages(thread)[0]; !strings.Contains(m.HTML, "💬 <i>Собираю бинарник &lt;для&gt; проверки</i>") {
		t.Fatalf("prompt: %q", m.HTML)
	}
}

func TestSudoApproval(t *testing.T) {
	f := newFixture(t)
	fs := &fakeSudo{grants: map[int]int{}}
	f.b.SetSudo(fs)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "sudo apt update && sudo apt install -y htop"})
	testutil.Eventually(t, "sudo prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	m := f.api.Messages(thread)[0]
	if !strings.Contains(m.HTML, "sudo") {
		t.Fatalf("prompt: %q", m.HTML)
	}
	if _, ok := f.api.Button(thread, "♾"); ok {
		t.Fatal("sudo must never offer «Всегда»")
	}
	f.press(t, "✅")
	d := decision(t, ch)
	fs.mu.Lock()
	uses, tok := fs.grants[thread], fs.tokens[thread]
	fs.mu.Unlock()
	want := "TGSYNC_SUDO_TOKEN=" + tok + " sudo -A apt update && TGSYNC_SUDO_TOKEN=" + tok + " sudo -A apt install -y htop"
	if !d.Allow || tok == "" || d.UpdatedInput["command"] != want {
		t.Fatalf("decision: %+v (token %q)", d, tok)
	}
	if uses != 2 {
		t.Fatalf("grant uses: %d", uses)
	}
	f.b.CancelThread(context.Background(), thread)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if len(fs.revoked) != 1 || fs.revoked[0] != thread {
		t.Fatalf("revoked: %v", fs.revoked)
	}
}

func TestSudoProtectedPathStillDenied(t *testing.T) {
	f := newFixture(t)
	f.b.SetSudo(&fakeSudo{grants: map[int]int{}})
	can := f.b.CanUseTool(SessionInfo{ThreadID: thread, Project: "demo", ProjectDir: osPath("/w/demo"), Protected: []string{osPath("/opt/tgsync/.env")}})
	d := decision(t, bash(can, "sudo cat "+shellPath("/opt/tgsync/.env")))
	if d.Allow || len(f.api.Messages(thread)) != 0 {
		t.Fatalf("decision: %+v", d)
	}
}

func TestWrappedSudoRefusedWithoutPrompt(t *testing.T) {
	f := newFixture(t)
	f.b.SetSudo(&fakeSudo{grants: map[int]int{}})
	can := f.b.CanUseTool(SessionInfo{ThreadID: thread, Project: "demo", ProjectDir: osPath("/w/demo")})
	d := decision(t, bash(can, "env sudo id"))
	if d.Allow || !strings.Contains(d.Message, "прямым вызовом") || len(f.api.Messages(thread)) != 0 {
		t.Fatalf("decision: %+v, messages: %d", d, len(f.api.Messages(thread)))
	}
}

func TestAskPassword(t *testing.T) {
	f := newFixture(t)
	out := make(chan string, 1)
	go func() { pw, _ := f.b.AskPassword(context.Background(), thread); out <- pw }()
	testutil.Eventually(t, "password prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	if f.b.HandlePassword(context.Background(), thread+1, 900, "wrong topic") {
		t.Fatal("other topic must not answer")
	}
	userMsg, _ := f.api.SendMessage(context.Background(), thread, "hunter2", nil, false)
	if !f.b.HandlePassword(context.Background(), thread, userMsg, "hunter2") {
		t.Fatal("password not consumed")
	}
	if pw := <-out; pw != "hunter2" {
		t.Fatalf("pw: %q", pw)
	}
	for _, m := range f.api.Messages(thread) {
		if strings.Contains(m.HTML, "hunter2") {
			t.Fatal("password message must be deleted")
		}
	}
	if f.b.HandlePassword(context.Background(), thread, 1, "next") {
		t.Fatal("only one message is taken as the password")
	}
}

func TestPasswordPromptRules(t *testing.T) {
	f := newFixture(t)
	out := make(chan error, 2)
	go func() { _, err := f.b.AskPassword(context.Background(), thread); out <- err }()
	testutil.Eventually(t, "prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	if _, err := f.b.AskPassword(context.Background(), thread); err == nil {
		t.Fatal("a second prompt in the same topic must be refused")
	}
	if f.b.HandlePassword(context.Background(), thread, 1, "/stop") {
		t.Fatal("commands must not be taken as the password")
	}
	f.b.HandlePassword(context.Background(), thread, 424242, "pw")
	if err := <-out; err != nil {
		t.Fatal(err)
	}
	if m := f.api.Messages(thread)[0]; !strings.Contains(m.HTML, "удали его вручную") {
		t.Fatalf("failed deletion must be reported: %q", m.HTML)
	}
}

func TestCancelThreadWithdrawsPasswordPrompt(t *testing.T) {
	f := newFixture(t)
	out := make(chan error, 1)
	go func() { _, err := f.b.AskPassword(context.Background(), thread); out <- err }()
	testutil.Eventually(t, "password prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	f.b.CancelThread(context.Background(), thread)
	select {
	case err := <-out:
		if err == nil {
			t.Fatal("cancelled prompt must fail")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("password prompt still waits after the turn ended")
	}
	if f.b.HandlePassword(context.Background(), thread, 77, "обычное сообщение") {
		t.Fatal("the next message after cancel must go to the agent, not be taken as the password")
	}
	if m := f.api.Messages(thread)[0]; !strings.Contains(m.HTML, "не получен") {
		t.Fatalf("prompt not updated: %q", m.HTML)
	}
}

func TestRemind(t *testing.T) {
	f := newFixture(t)
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "make deploy"})
	testutil.Eventually(t, "prompt", func() bool { return len(f.api.Messages(thread)) == 1 })
	now := time.Now()
	f.b.Remind(context.Background(), now.Add(time.Hour), 2*time.Hour)
	if len(f.api.Messages(thread)) != 1 {
		t.Fatal("too early to remind")
	}
	f.b.Remind(context.Background(), now.Add(3*time.Hour), 2*time.Hour)
	f.b.Remind(context.Background(), now.Add(3*time.Hour), 2*time.Hour)
	msgs := f.api.Messages(thread)
	if len(msgs) != 2 || !strings.Contains(msgs[1].HTML, "Жду") || msgs[1].Silent {
		t.Fatalf("reminder: %+v", msgs)
	}
	f.b.Remind(context.Background(), now.Add(10*time.Hour), 0)
	if len(f.api.Messages(thread)) != 2 {
		t.Fatal("REMIND_EVERY=0 disables reminders")
	}
	f.press(t, "✅")
	decision(t, ch)
}
