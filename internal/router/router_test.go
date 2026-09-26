package router

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/group"
	"github.com/ArthurKrantsevich/tgsync/internal/history"
	"github.com/ArthurKrantsevich/tgsync/internal/permissions"
	"github.com/ArthurKrantsevich/tgsync/internal/projects"
	"github.com/ArthurKrantsevich/tgsync/internal/session"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
	"github.com/ArthurKrantsevich/tgsync/internal/topics"
)

const owner = int64(1)

type fx struct {
	r    *Router
	api  *telegram.Fake
	ag   *agent.Fake
	st   *store.Store
	root string
}

func setup(t *testing.T) *fx { t.Helper(); return setupWith(t, nil) }

// gate holds CreateTopic calls while armed, so a test can press a button
// again while the first press is still creating its topic.
type gate struct {
	*telegram.Fake
	mu      sync.Mutex
	hold    chan struct{}
	entered chan struct{}
}

func (g *gate) arm() {
	g.mu.Lock()
	g.hold, g.entered = make(chan struct{}), make(chan struct{}, 8)
	g.mu.Unlock()
}

func (g *gate) release() {
	g.mu.Lock()
	close(g.hold)
	g.hold = nil
	g.mu.Unlock()
}

func (g *gate) CreateTopic(ctx context.Context, name string, color int, icon string) (int, error) {
	g.mu.Lock()
	hold, entered := g.hold, g.entered
	g.mu.Unlock()
	if hold != nil {
		entered <- struct{}{}
		<-hold
	}
	return g.Fake.CreateTopic(ctx, name, color, icon)
}

// setupWith builds the fixture; wrap, if set, wraps the API the topic
// manager uses.
func setupWith(t *testing.T, wrap func(*telegram.Fake) telegram.API) *fx {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	api := telegram.NewFake()
	var tapi telegram.API = api
	if wrap != nil {
		tapi = wrap(api)
	}
	tp := topics.New(tapi, st, "node1")
	_, _ = tp.EnsureControl(ctx)
	br := permissions.NewBroker(api, st)
	ag := &agent.Fake{}
	mgr := session.NewManager(session.Deps{API: api, Store: st, Topics: tp, Broker: br, Runner: ag, MaxParallel: 2})
	t.Cleanup(mgr.Shutdown)
	g := &group.Group{API: api, Store: st, Topics: tp, Forget: mgr.TopicRemoved}
	r := &Router{Allowed: func(id int64) bool { return id == owner }, API: g.ControlAPI(api), ChatID: -1001234567890, ClaudeHome: t.TempDir(),
		Topics: tp, Projects: projects.Registry{Root: root}, Sessions: mgr, Broker: br, Group: g}
	return &fx{r: r, api: api, ag: ag, st: st, root: root}
}

func TestCleanupButtonNothingToClean(t *testing.T) {
	f := setup(t)
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(),
		CallbackID: "cb", CallbackData: "cl:ask"})
	if a := f.api.Answers(); len(a) == 0 || a[len(a)-1] != "Убирать нечего" {
		t.Fatalf("answers: %v", a)
	}
}

func TestControlTopicSweep(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	control := f.r.Topics.Control()
	f.r.Handle(ctx, telegram.Update{UserID: owner, ThreadID: control, MessageID: 500, Text: "/menu"})
	if len(f.api.Messages(control)) != 1 {
		t.Fatalf("menu: %+v", f.api.Messages(control))
	}
	f.r.Handle(ctx, telegram.Update{UserID: owner, ThreadID: control, CallbackID: "cb", CallbackData: "cl:sweep"})
	if a := f.api.Answers(); len(a) == 0 || a[len(a)-1] != "🧽 Удалено сообщений: 1" {
		t.Fatalf("answers: %v", a)
	}
	if msgs := f.api.Messages(control); len(msgs) != 0 {
		t.Fatalf("left: %+v", msgs)
	}
	f.r.Handle(ctx, telegram.Update{UserID: owner, ThreadID: control, CallbackID: "cb2", CallbackData: "cl:sweep"})
	if a := f.api.Answers(); a[len(a)-1] != "Чистить нечего" {
		t.Fatalf("answers: %v", a)
	}
}

// longEditErr fails every edit with an error longer than a callback toast.
type longEditErr struct{ *telegram.Fake }

func (longEditErr) EditMessage(context.Context, int, string, telegram.Keyboard) error {
	return fmt.Errorf("%s", strings.Repeat("ошибка ", 60))
}

func TestCleanupErrorToastCut(t *testing.T) {
	f := setup(t)
	f.r.Group.API = longEditErr{f.api}
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(),
		CallbackID: "cb", CallbackData: "cl:no", MessageID: 1})
	a := f.api.Answers()
	if len(a) == 0 || !strings.HasPrefix(a[len(a)-1], "Ошибка: ") || utf8.RuneCountInString(a[len(a)-1]) > 190 {
		t.Fatalf("answers: %q", a)
	}
}

func (f *fx) send(thread int, text string) {
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, Text: text})
}

func (f *fx) lastControl(t *testing.T) string {
	t.Helper()
	msgs := f.api.Messages(f.r.Topics.Control())
	if len(msgs) == 0 {
		t.Fatal("no reply in control topic")
	}
	return msgs[len(msgs)-1].HTML
}

// sessionThread returns the newest topic that is not the control topic.
func (f *fx) sessionThread(t *testing.T) int {
	t.Helper()
	tps := f.api.Topics()
	last := tps[len(tps)-1]
	if last.ID == f.r.Topics.Control() {
		t.Fatal("no session topic")
	}
	return last.ID
}

func TestApproveMenuSwitchesMode(t *testing.T) {
	f := setup(t)
	control := f.r.Topics.Control()
	f.send(control, "/approve")
	if !strings.Contains(f.lastControl(t), "Сейчас: <b>🔴 По запросу</b>") {
		t.Fatalf("menu: %s", f.lastControl(t))
	}
	msgs := f.api.Messages(control)
	menu := msgs[len(msgs)-1]
	btn, _ := f.api.Button(control, "🟡")
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: control, MessageID: menu.ID,
		CallbackID: "cb", CallbackData: btn.Data})
	if m := f.r.Broker.ApproveMode(context.Background()); m != permissions.ApproveNoSudo {
		t.Fatalf("mode: %q", m)
	}
	if !strings.Contains(f.lastControl(t), "Сейчас: <b>🟡 Всё, кроме sudo</b>") {
		t.Fatalf("menu after press: %s", f.lastControl(t))
	}
}

func TestStrangersAreIgnored(t *testing.T) {
	f := setup(t)
	f.r.Handle(context.Background(), telegram.Update{UserID: 999, ThreadID: f.r.Topics.Control(), Text: "/projects"})
	if n := len(f.api.Messages(f.r.Topics.Control())); n != 0 {
		t.Fatalf("stranger got %d replies", n)
	}
}

func TestProjectsList(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/projects")
	if b, ok := f.api.Button(f.r.Topics.Control(), "📁 demo"); !ok || b.Data != "pr:demo" {
		t.Fatalf("button: %+v %v", b, ok)
	}
}

func TestNewSessionWithTask(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new@tgsync_bot demo fix the\nlogin bug")
	testutil.Eventually(t, "agent started", func() bool { return len(f.ag.Sessions()) == 1 })
	s := f.ag.Sessions()[0]
	if s.Opts.Cwd != filepath.Join(f.root, "demo") {
		t.Fatalf("cwd: %q", s.Opts.Cwd)
	}
	if got := s.Sent(); len(got) != 1 || got[0] != "fix the\nlogin bug" {
		t.Fatalf("sent: %q", got)
	}
	if reply := f.lastControl(t); !strings.Contains(reply, "https://t.me/c/1234567890/") {
		t.Fatalf("reply: %q", reply)
	}
}

func TestNewSessionBadProject(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new ../etc hack")
	if reply := f.lastControl(t); !strings.Contains(reply, "имя проекта") {
		t.Fatalf("reply: %q", reply)
	}
	f.send(f.r.Topics.Control(), "/new missing task")
	if reply := f.lastControl(t); !strings.Contains(reply, "не найден") {
		t.Fatalf("reply: %q", reply)
	}
	if n := len(f.api.Topics()); n != 1 {
		t.Fatalf("no topic must be created, got %d topics", n)
	}
}

func TestNewProject(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/newproject app")
	if _, err := os.Stat(filepath.Join(f.root, "app", ".git")); err != nil {
		t.Fatalf("project not created: %v", err)
	}
	f.send(f.r.Topics.Control(), "/newproject ../x")
	if reply := f.lastControl(t); !strings.Contains(reply, "имя проекта") {
		t.Fatalf("reply: %q", reply)
	}
}

func TestProjectButtonStartsSession(t *testing.T) {
	f := setup(t)
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(), CallbackID: "cb", CallbackData: "new:demo"})
	thread := f.sessionThread(t)
	f.send(thread, "write docs")
	testutil.Eventually(t, "agent started", func() bool { return len(f.ag.Sessions()) == 1 })
}

func TestSessionCommands(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	testutil.Eventually(t, "agent started", func() bool { return len(f.ag.Sessions()) == 1 })
	s := f.ag.Sessions()[0]

	f.send(thread, "/stop")
	if s.Interrupts() != 1 {
		t.Fatalf("interrupts: %d", s.Interrupts())
	}
	f.send(thread, "/mode plan")
	if s.Mode() != "plan" {
		t.Fatalf("mode: %q", s.Mode())
	}
	f.send(thread, "/mode yolo")
	msgs := f.api.Messages(thread)
	if !strings.Contains(msgs[len(msgs)-1].HTML, "⚠️") {
		t.Fatalf("bad mode must be reported: %q", msgs[len(msgs)-1].HTML)
	}

	s.Emit(agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "success"}})
	testutil.Eventually(t, "turn finished", func() bool {
		rows, _ := f.st.OpenSessions(context.Background())
		for _, r := range rows {
			if r.ThreadID == thread {
				return r.State == store.StateIdle
			}
		}
		return false
	})
	f.send(thread, "/code-review high")
	testutil.Eventually(t, "plugin command forwarded", func() bool {
		sent := s.Sent()
		return len(sent) == 2 && sent[1] == "/code-review high"
	})

	_ = f.st.AddUsage(context.Background(), store.UsageRow{At: time.Now(), ThreadID: thread, Project: "demo", Model: "m"})
	f.send(thread, "/close")
	if !f.api.Topic(thread).Closed || !s.Closed() {
		t.Fatal("session not closed")
	}
}

func TestSessionsList(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/sessions")
	if reply := f.lastControl(t); !strings.Contains(reply, "нет") {
		t.Fatalf("reply: %q", reply)
	}
	f.send(f.r.Topics.Control(), "/new demo task")
	f.send(f.r.Topics.Control(), "/sessions")
	if reply := f.lastControl(t); !strings.Contains(reply, "demo · task") {
		t.Fatalf("reply: %q", reply)
	}
}

func TestForeignTopicIgnored(t *testing.T) {
	f := setup(t)
	f.send(424242, "hello")
	f.send(0, "/projects")
	if n := len(f.api.Messages(424242)) + len(f.api.Messages(0)); n != 0 {
		t.Fatalf("foreign topics got %d replies", n)
	}
}

func TestSessionCommandsWinOverOwnAnswer(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	testutil.Eventually(t, "agent started", func() bool { return len(f.ag.Sessions()) == 1 })
	s := f.ag.Sessions()[0]
	go s.Ask(agent.PermissionRequest{ToolName: "AskUserQuestion", Input: map[string]any{"questions": []any{
		map[string]any{"question": "Color?", "options": []any{map[string]any{"label": "Red"}}},
	}}})
	var btn telegram.Button
	testutil.Eventually(t, "own answer button", func() bool { var ok bool; btn, ok = f.api.Button(thread, "✍"); return ok })
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, CallbackID: "cb", CallbackData: btn.Data})
	f.send(thread, "/stop")
	if s.Interrupts() != 1 {
		t.Fatalf("/stop must interrupt, not answer the question; interrupts=%d", s.Interrupts())
	}
}

func TestHelp(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/help")
	reply := f.lastControl(t)
	for _, want := range []string{"/menu", "/newproject", "/stop", "/ls", "/control"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("help misses %q", want)
		}
	}
	for _, cmd := range []string{"/start", "привет"} {
		f.send(f.r.Topics.Control(), cmd)
		if _, ok := f.api.Button(f.r.Topics.Control(), "❓ Справка"); !ok {
			t.Fatalf("%s must open the menu", cmd)
		}
	}
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	f.send(thread, "/help")
	msgs := f.api.Messages(thread)
	if last := msgs[len(msgs)-1].HTML; !strings.Contains(last, "/mode") {
		t.Fatalf("session help: %q", last)
	}
}

func TestControlCommandInGeneral(t *testing.T) {
	f := setup(t)
	f.send(0, "/control@ExampleNodeBot")
	msgs := f.api.Messages(0)
	if len(msgs) != 1 || !strings.Contains(msgs[0].HTML, fmt.Sprintf("/%d", f.r.Topics.Control())) {
		t.Fatalf("general reply: %+v", msgs)
	}
	old := f.r.Topics.Control()
	f.api.DeleteTopic(old)
	f.send(0, "/control")
	if f.r.Topics.Control() == old {
		t.Fatal("deleted control topic must be recreated")
	}
	f.send(f.r.Topics.Control(), "/projects")
	if _, ok := f.api.Button(f.r.Topics.Control(), "📁 demo"); !ok {
		t.Fatal("new control topic must accept commands")
	}
	f.send(0, "hello")
	if n := len(f.api.Messages(0)); n != 2 {
		t.Fatalf("plain text in General must be ignored, got %d messages", n)
	}
}

func (f *fx) transcript(t *testing.T, id, prompt string, age time.Duration) {
	t.Helper()
	dir := history.Dir(f.r.ClaudeHome, filepath.Join(f.root, "demo"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	line := fmt.Sprintf(`{"type":"user","message":{"content":%q},"entrypoint":"claude-desktop"}`+"\n", prompt)
	if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-age)
	_ = os.Chtimes(p, ts, ts)
}

func (f *fx) press(t *testing.T, thread int, prefix string) {
	t.Helper()
	var btn telegram.Button
	testutil.Eventually(t, "button "+prefix, func() bool { var ok bool; btn, ok = f.api.Button(thread, prefix); return ok })
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, CallbackID: "cb", CallbackData: btn.Data})
}

func TestHistoryResumesTerminalSession(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/history")
	if reply := f.lastControl(t); !strings.Contains(reply, "не найдено") {
		t.Fatalf("empty history: %q", reply)
	}
	f.transcript(t, "sid-old", "Fix the login bug", 3*time.Hour)
	f.send(f.r.Topics.Control(), "/history demo")
	if reply := f.lastControl(t); !strings.Contains(reply, "Fix the login bug") || !strings.Contains(reply, "demo") {
		t.Fatalf("history: %q", reply)
	}
	f.press(t, f.r.Topics.Control(), "1 ")
	thread := f.sessionThread(t)
	msgs := f.api.Messages(thread)
	if len(msgs) == 0 || !strings.Contains(msgs[0].HTML, "Подключено") || strings.Contains(msgs[0].HTML, "копия") {
		t.Fatalf("attach message: %+v", msgs)
	}
	f.send(thread, "continue please")
	testutil.Eventually(t, "resumed", func() bool { return len(f.ag.Sessions()) == 1 })
	if s := f.ag.Sessions()[0]; s.Opts.ResumeID != "sid-old" || s.Opts.Fork {
		t.Fatalf("opts: %+v", s.Opts)
	}
	topicsBefore := len(f.api.Topics())
	f.send(f.r.Topics.Control(), "/history demo")
	f.press(t, f.r.Topics.Control(), "1 ")
	if n := len(f.api.Topics()); n != topicsBefore {
		t.Fatal("an open session must not get a second topic")
	}
	if reply := f.lastControl(t); !strings.Contains(reply, "уже открыта") {
		t.Fatalf("reply: %q", reply)
	}
}

func TestHistoryForksActiveSession(t *testing.T) {
	f := setup(t)
	f.transcript(t, "sid-live", "Work in progress", 10*time.Second)
	f.send(f.r.Topics.Control(), "/history")
	if reply := f.lastControl(t); !strings.Contains(reply, "активна") {
		t.Fatalf("history: %q", reply)
	}
	f.press(t, f.r.Topics.Control(), "1 ")
	thread := f.sessionThread(t)
	if msgs := f.api.Messages(thread); len(msgs) == 0 || !strings.Contains(msgs[0].HTML, "копия") {
		t.Fatalf("fork note missing: %+v", msgs)
	}
	f.send(thread, "go")
	testutil.Eventually(t, "forked", func() bool { return len(f.ag.Sessions()) == 1 })
	if s := f.ag.Sessions()[0]; s.Opts.ResumeID != "sid-live" || !s.Opts.Fork {
		t.Fatalf("opts: %+v", s.Opts)
	}
}

func TestHistorySkipsFoldersWithBadNames(t *testing.T) {
	f := setup(t)
	for _, d := range []string{"My App", "проект"} {
		if err := os.Mkdir(filepath.Join(f.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.transcript(t, "sid-1", "Fix the login bug", time.Hour)
	f.send(f.r.Topics.Control(), "/history")
	if reply := f.lastControl(t); !strings.Contains(reply, "Fix the login bug") {
		t.Fatalf("history: %q", reply)
	}
	f.send(f.r.Topics.Control(), "/projects")
	if _, ok := f.api.Button(f.r.Topics.Control(), "📁 My App"); ok {
		t.Fatal("a folder Dir rejects must not get a button")
	}
	// "aaa" sorts first and its transcripts cannot be read.
	if err := os.Mkdir(filepath.Join(f.root, "aaa"), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := history.Dir(f.r.ClaudeHome, filepath.Join(f.root, "aaa"))
	_ = os.MkdirAll(filepath.Dir(broken), 0o700)
	if err := os.WriteFile(broken, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f.send(f.r.Topics.Control(), "/history")
	if reply := f.lastControl(t); !strings.Contains(reply, "Fix the login bug") {
		t.Fatalf("one broken project must not hide the others: %q", reply)
	}
}

// doubleTap presses a button of message msgID twice, the second time while
// the first press is still creating its topic. It reports whether the
// second press started a topic of its own, and the callback answers seen
// while the first press was creating its topic.
func (f *fx) doubleTap(t *testing.T, g *gate, msgID int, data string) (second bool, early []string) {
	t.Helper()
	press := func(id string) chan struct{} {
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(),
				MessageID: msgID, CallbackID: id, CallbackData: data})
		}()
		return done
	}
	g.arm()
	done1 := press("cb1")
	select {
	case <-g.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("first press did not create a topic")
	}
	early = f.api.Answers()
	done2 := press("cb2")
	select {
	case <-g.entered:
		second = true
	case <-done2:
	case <-time.After(3 * time.Second):
		t.Fatal("second press hangs")
	}
	g.release()
	<-done1
	<-done2
	return second, early
}

func TestHistoryDoubleTapAttachesOnce(t *testing.T) {
	var g *gate
	f := setupWith(t, func(api *telegram.Fake) telegram.API { g = &gate{Fake: api}; return g })
	f.transcript(t, "sid-1", "Fix the login bug", 3*time.Hour)
	f.send(f.r.Topics.Control(), "/history")
	btn, ok := f.api.Button(f.r.Topics.Control(), "1 ")
	if !ok {
		t.Fatal("no history button")
	}
	if second, _ := f.doubleTap(t, g, 0, btn.Data); second {
		t.Fatal("a double tap must not attach the session twice")
	}
	if n := len(f.api.Topics()); n != 2 {
		t.Fatalf("topics: %d, want control + 1", n)
	}
}

func TestNewSessionButtonDoubleTap(t *testing.T) {
	var g *gate
	f := setupWith(t, func(api *telegram.Fake) telegram.API { g = &gate{Fake: api}; return g })
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(), CallbackID: "cb", CallbackData: "pr:demo"})
	msgs := f.api.Messages(f.r.Topics.Control())
	menu := msgs[len(msgs)-1]
	before := len(f.api.Answers())
	second, early := f.doubleTap(t, g, menu.ID, "new:demo")
	if second {
		t.Fatal("a double tap must not create two sessions")
	}
	if len(early) != before+1 {
		t.Fatalf("the press must be answered before the topic is created: answers %v", early)
	}
	if n := len(f.api.Topics()); n != 2 {
		t.Fatalf("topics: %d, want control + 1", n)
	}
}

func TestNewProjectLongNameKeepsButtonData(t *testing.T) {
	f := setup(t)
	name := strings.Repeat("a", 64)
	f.send(f.r.Topics.Control(), "/newproject "+name)
	if !strings.Contains(f.lastControl(t), "Создан проект") {
		t.Fatalf("reply: %q", f.lastControl(t))
	}
	for _, m := range f.api.Messages(f.r.Topics.Control()) {
		for _, row := range m.Keyboard {
			for _, b := range row {
				if len(b.Data) > 64 {
					t.Fatalf("button data over 64 bytes, Telegram rejects the message: %q", b.Data)
				}
			}
		}
	}
}

func TestHistoryPicksBounded(t *testing.T) {
	f := setup(t)
	f.transcript(t, "sid-1", "Fix the login bug", time.Hour)
	for i := 0; i < maxPicks+10; i++ {
		f.send(f.r.Topics.Control(), "/history")
	}
	f.r.mu.Lock()
	n := len(f.r.picks)
	f.r.mu.Unlock()
	if n > maxPicks {
		t.Fatalf("picks: %d, want at most %d", n, maxPicks)
	}
	f.press(t, f.r.Topics.Control(), "1 ") // the newest list still works
	if msgs := f.api.Messages(f.sessionThread(t)); len(msgs) == 0 {
		t.Fatal("newest pick must attach")
	}
}

func TestHistoryBadProjectAndStaleButton(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/history nope")
	if reply := f.lastControl(t); !strings.Contains(reply, "не найден") {
		t.Fatalf("reply: %q", reply)
	}
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(), CallbackID: "cb", CallbackData: "h:99"})
	if a := f.api.Answers(); len(a) == 0 || !strings.Contains(a[len(a)-1], "устарел") {
		t.Fatalf("answers: %v", a)
	}
}

func TestFileCommandAndButtons(t *testing.T) {
	f := setup(t)
	_ = os.WriteFile(filepath.Join(f.root, "demo", "notes.md"), []byte("# Notes"), 0o644)
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	testutil.Eventually(t, "agent started", func() bool { return len(f.ag.Sessions()) == 1 })
	f.send(thread, "/file notes.md")
	if docs := f.api.Documents(thread); len(docs) != 1 || docs[0].Name != "notes.md" {
		t.Fatalf("docs: %+v", docs)
	}
	f.send(thread, "/file ../../etc/passwd")
	msgs := f.api.Messages(thread)
	if !strings.Contains(msgs[len(msgs)-1].HTML, "⚠️") {
		t.Fatalf("refusal missing: %q", msgs[len(msgs)-1].HTML)
	}
	s := f.ag.Sessions()[0]
	s.Emit(agent.Event{Kind: agent.EventToolUse, ToolName: "Write", ToolInput: map[string]any{"file_path": filepath.Join(f.root, "demo", "notes.md")}})
	s.Emit(agent.Event{Kind: agent.EventResult, Result: &agent.ResultInfo{Subtype: "success"}})
	f.send(thread, "/file notes.md")
	testutil.Eventually(t, "file resent on request", func() bool { return len(f.api.Documents(thread)) == 2 })
	f.send(thread, "/help")
	if msgs := f.api.Messages(thread); !strings.Contains(msgs[len(msgs)-1].HTML, "/file") {
		t.Fatal("session help must mention /file")
	}
}

func TestPasswordMessageIsConsumedAndDeleted(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	out := make(chan string, 1)
	go func() { pw, _ := f.r.Broker.AskPassword(context.Background(), thread); out <- pw }()
	testutil.Eventually(t, "password prompt", func() bool {
		for _, m := range f.api.Messages(thread) {
			if strings.Contains(m.HTML, "пароль") {
				return true
			}
		}
		return false
	})
	// /stop would end the turn and withdraw the prompt, so use /help.
	before := len(f.api.Messages(thread))
	f.send(thread, "/help")
	if msgs := f.api.Messages(thread); len(msgs) == before || !strings.Contains(msgs[len(msgs)-1].HTML, "/file") {
		t.Fatal("a command stays a command while a password is expected")
	}
	id, _ := f.api.SendMessage(context.Background(), thread, "hunter2", nil, false)
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, MessageID: id, Text: "hunter2"})
	if pw := <-out; pw != "hunter2" {
		t.Fatalf("pw: %q", pw)
	}
	for _, m := range f.api.Messages(thread) {
		if m.HTML == "hunter2" {
			t.Fatal("password message must be deleted")
		}
	}
}

func TestStopWithdrawsPasswordPrompt(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	out := make(chan error, 1)
	go func() { _, err := f.r.Broker.AskPassword(context.Background(), thread); out <- err }()
	testutil.Eventually(t, "password prompt", func() bool {
		for _, m := range f.api.Messages(thread) {
			if strings.Contains(m.HTML, "пароль") {
				return true
			}
		}
		return false
	})
	f.send(thread, "/stop")
	if err := <-out; err == nil {
		t.Fatal("/stop must withdraw the password prompt")
	}
	id, _ := f.api.SendMessage(context.Background(), thread, "следующая задача", nil, false)
	if f.r.Broker.HandlePassword(context.Background(), thread, id, "следующая задача") {
		t.Fatal("the next message after /stop must not be taken as the password")
	}
}

func TestParseNewArgs(t *testing.T) {
	cases := map[string][3]string{
		"demo fix it":                       {"demo", "", "fix it"},
		"demo --profile lean fix it":        {"demo", "lean", "fix it"},
		"demo --profile=lean":               {"demo", "lean", ""},
		"demo\n--profile lean\nmulti\nline": {"demo", "lean", "multi\nline"},
		"demo":                              {"demo", "", ""},
	}
	for in, want := range cases {
		p, prof, task, ok := parseNewArgs(in)
		if !ok || p != want[0] || prof != want[1] || task != want[2] {
			t.Errorf("parseNewArgs(%q) = %q %q %q %v", in, p, prof, task, ok)
		}
	}
	if _, _, _, ok := parseNewArgs(""); ok {
		t.Error("empty args must fail")
	}
}

func TestProfilesAndSkillsCommands(t *testing.T) {
	f := setup(t)
	f.r.ProfileNames = func() []string { return []string{"full", "lean"} }
	f.send(f.r.Topics.Control(), "/profiles")
	if reply := f.lastControl(t); !strings.Contains(reply, "lean") || !strings.Contains(reply, "--profile") {
		t.Fatalf("profiles: %q", reply)
	}
	f.send(f.r.Topics.Control(), "/new demo task")
	thread := f.sessionThread(t)
	f.send(thread, "/skills")
	if msgs := f.api.Messages(thread); !strings.Contains(msgs[len(msgs)-1].HTML, "после первого ответа") {
		t.Fatalf("skills: %q", msgs[len(msgs)-1].HTML)
	}
}

func TestUserSendsFiles(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new demo")
	thread := f.sessionThread(t)
	f.api.AddFile("doc1", []byte("error: boom"))
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, File: &telegram.File{ID: "doc1", Name: "app.log", Size: 11}})
	testutil.Eventually(t, "file stored", func() bool {
		m, _ := filepath.Glob(filepath.Join(f.root, "demo", ".tgsync", "inbox", "*app.log"))
		return len(m) == 1
	})
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: thread, File: &telegram.File{ID: "big", Name: "huge.iso", Size: 30 << 20}})
	msgs := f.api.Messages(thread)
	if last := msgs[len(msgs)-1].HTML; !strings.Contains(last, "20 МБ") {
		t.Fatalf("too big file: %q", last)
	}
	f.send(thread, "что в логе?")
	testutil.Eventually(t, "agent got the file", func() bool {
		ss := f.ag.Sessions()
		return len(ss) == 1 && len(ss[0].Sent()) == 1 && strings.Contains(ss[0].Sent()[0], "app.log")
	})
	f.r.Handle(context.Background(), telegram.Update{UserID: owner, ThreadID: f.r.Topics.Control(), File: &telegram.File{ID: "doc1", Name: "x"}})
	if n := len(f.api.Messages(f.r.Topics.Control())); n == 0 {
		t.Fatal("files in the control topic need a hint")
	}
}

func TestLsCommand(t *testing.T) {
	f := setup(t)
	_ = os.MkdirAll(filepath.Join(f.root, "demo", "docs"), 0o755)
	f.send(f.r.Topics.Control(), "/new demo")
	thread := f.sessionThread(t)
	f.send(thread, "/ls")
	if _, ok := f.api.Button(thread, "📁 docs"); !ok {
		t.Fatal("/ls must list folders")
	}
	f.send(thread, "/ls ../..")
	msgs := f.api.Messages(thread)
	if !strings.Contains(msgs[len(msgs)-1].HTML, "⚠️") {
		t.Fatal("escape must be refused")
	}
	f.press(t, thread, "📁 docs")
	testutil.Eventually(t, "folder opened", func() bool {
		for _, m := range f.api.Messages(thread) {
			if strings.Contains(m.HTML, "/docs") {
				return true
			}
		}
		return false
	})
}

func TestCommandList(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range Commands() {
		if len(c.Name) > 32 || c.Name != strings.ToLower(c.Name) || c.Description == "" || len(c.Description) > 256 {
			t.Errorf("bad command %+v", c)
		}
		seen[c.Name] = true
	}
	for _, want := range []string{"menu", "projects", "new", "history", "sessions", "stop", "ls", "file", "close", "help"} {
		if !seen[want] {
			t.Errorf("command %s missing", want)
		}
	}
}

func TestMenuNavigation(t *testing.T) {
	f := setup(t)
	control := f.r.Topics.Control()
	f.send(control, "привет")
	if _, ok := f.api.Button(control, "📁 Проекты"); !ok {
		t.Fatal("plain text in the control topic must open the menu")
	}
	f.press(t, control, "📁 Проекты")
	f.press(t, control, "📁 demo")
	if _, ok := f.api.Button(control, "▶ Новая сессия"); !ok {
		t.Fatal("project menu missing")
	}
	if _, ok := f.api.Button(control, "🕘 История"); !ok {
		t.Fatal("project history button missing")
	}
	f.press(t, control, "▶ Новая сессия")
	testutil.Eventually(t, "session topic", func() bool { return len(f.api.Topics()) == 2 })
}

func TestNewProjectByButton(t *testing.T) {
	f := setup(t)
	control := f.r.Topics.Control()
	f.send(control, "/menu")
	f.press(t, control, "➕ Новый проект")
	if !strings.Contains(f.lastControl(t), "Как назвать") {
		t.Fatalf("prompt: %q", f.lastControl(t))
	}
	f.send(control, "../bad")
	if !strings.Contains(f.lastControl(t), "имя проекта") {
		t.Fatalf("bad name: %q", f.lastControl(t))
	}
	f.send(control, "shiny-app")
	if _, err := os.Stat(filepath.Join(f.root, "shiny-app", ".git")); err != nil {
		t.Fatalf("project not created: %v", err)
	}
	if _, ok := f.api.Button(control, "▶ Новая сессия"); !ok {
		t.Fatal("after creating a project offer to start a session")
	}
	f.send(control, "second")
	if _, err := os.Stat(filepath.Join(f.root, "second")); err == nil {
		t.Fatal("the name prompt must answer only once")
	}
	f.press(t, control, "➕ Новый проект")
	f.send(control, "/cancel")
	f.send(control, "third")
	if _, err := os.Stat(filepath.Join(f.root, "third")); err == nil {
		t.Fatal("/cancel must drop the name prompt")
	}
}

func TestNewWithoutArgsListsProjects(t *testing.T) {
	f := setup(t)
	f.send(f.r.Topics.Control(), "/new")
	if _, ok := f.api.Button(f.r.Topics.Control(), "📁 demo"); !ok {
		t.Fatal("/new without a project must offer projects")
	}
	f.send(f.r.Topics.Control(), "/new missing task")
	if _, ok := f.api.Button(f.r.Topics.Control(), "📁 Проекты"); !ok {
		t.Fatal("errors must offer a way forward")
	}
}

func TestSessionHelpMentionsAgents(t *testing.T) {
	if !strings.Contains(helpSession, "/agents") {
		t.Fatal("session help must mention /agents")
	}
}

func TestHelpMentionsUsage(t *testing.T) {
	if !strings.Contains(helpSession, "/context") || !strings.Contains(helpSession, "/usage") || !strings.Contains(helpControl, "/usage") {
		t.Fatal("help must mention /context and /usage")
	}
}
