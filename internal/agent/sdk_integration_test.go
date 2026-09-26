//go:build integration

package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testModel() string {
	if m := os.Getenv("TGSYNC_TEST_MODEL"); m != "" {
		return m
	}
	return "haiku"
}

func allowAll(context.Context, PermissionRequest) PermissionDecision {
	return PermissionDecision{Allow: true}
}

func startTest(t *testing.T, dir string, can CanUseToolFunc, resume string) Session {
	t.Helper()
	s, err := SDKRunner{}.Start(context.Background(), StartOptions{
		Cwd: dir, Model: testModel(), ResumeID: resume,
		SettingSources: []string{"user", "project", "local"}, CanUseTool: can,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// turn sends text and collects events until the turn result.
func turn(t *testing.T, s Session, text string, timeout time.Duration) []Event {
	t.Helper()
	if err := s.Send(context.Background(), text); err != nil {
		t.Fatal(err)
	}
	var evs []Event
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatalf("events closed before result; got %d events", len(evs))
			}
			evs = append(evs, ev)
			if ev.Kind == EventResult {
				return evs
			}
		case <-deadline:
			t.Fatalf("no result within %s", timeout)
		}
	}
}

func find(evs []Event, k EventKind) *Event {
	for i := range evs {
		if evs[i].Kind == k {
			return &evs[i]
		}
	}
	return nil
}

func allText(evs []Event) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Kind == EventText {
			b.WriteString(e.Text)
		}
	}
	return b.String()
}

func enabledPlugins(t *testing.T) int {
	home, _ := os.UserHomeDir()
	raw, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		return 0
	}
	var s struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	_ = json.Unmarshal(raw, &s)
	n := 0
	for _, on := range s.EnabledPlugins {
		if on {
			n++
		}
	}
	return n
}

func TestIntegrationTextTurnAndPlugins(t *testing.T) {
	s := startTest(t, t.TempDir(), allowAll, "")
	evs := turn(t, s, "Reply with exactly the word pong and nothing else.", 3*time.Minute)
	init := find(evs, EventInit)
	if init == nil || init.Init.SessionID == "" {
		t.Fatal("no init event with session id")
	}
	if !strings.Contains(strings.ToLower(allText(evs)), "pong") {
		t.Fatalf("text: %q", allText(evs))
	}
	t.Logf("plugins=%v mcp=%v commands=%d", init.Init.Plugins, init.Init.MCPServers, len(init.Init.SlashCommands))
	if n := enabledPlugins(t); n > 0 && len(init.Init.Plugins) == 0 {
		t.Fatalf("%d plugins enabled in ~/.claude/settings.json, but init reports none: setting sources not applied", n)
	}
}

func TestIntegrationCanUseToolAllowAndDeny(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var seen []string
	s := startTest(t, dir, func(ctx context.Context, r PermissionRequest) PermissionDecision {
		mu.Lock()
		seen = append(seen, r.ToolName)
		mu.Unlock()
		if p, _ := r.Input["file_path"].(string); strings.HasSuffix(p, "deny.txt") {
			return PermissionDecision{Message: "denied by test"}
		}
		return PermissionDecision{Allow: true}
	}, "")
	turn(t, s, "Use the Write tool to create allow.txt containing hi. Then use the Write tool to create deny.txt containing hi. Do not use Bash.", 3*time.Minute)
	mu.Lock()
	defer mu.Unlock()
	if _, err := os.Stat(filepath.Join(dir, "allow.txt")); err != nil {
		t.Fatalf("allow.txt missing: %v (seen %v)", err, seen)
	}
	if _, err := os.Stat(filepath.Join(dir, "deny.txt")); err == nil {
		t.Fatalf("deny.txt must not exist (seen %v)", seen)
	}
}

func TestIntegrationAskUserQuestion(t *testing.T) {
	var mu sync.Mutex
	var asked bool
	s := startTest(t, t.TempDir(), func(ctx context.Context, r PermissionRequest) PermissionDecision {
		if r.ToolName != "AskUserQuestion" {
			return PermissionDecision{Allow: true}
		}
		qs, _ := r.Input["questions"].([]interface{})
		q, _ := qs[0].(map[string]interface{})
		text, _ := q["question"].(string)
		in := map[string]any{}
		for k, v := range r.Input {
			in[k] = v
		}
		in["answers"] = map[string]any{text: "Blue"}
		mu.Lock()
		asked = true
		mu.Unlock()
		return PermissionDecision{Allow: true, UpdatedInput: in}
	}, "")
	evs := turn(t, s, "Use the AskUserQuestion tool to ask me which color I prefer, with options Red and Blue. After I answer, reply with only the chosen color.", 3*time.Minute)
	mu.Lock()
	defer mu.Unlock()
	if !asked {
		t.Fatal("AskUserQuestion was not routed to CanUseTool")
	}
	if !strings.Contains(allText(evs), "Blue") {
		t.Fatalf("answer not delivered; text: %q", allText(evs))
	}
}

func TestIntegrationResume(t *testing.T) {
	dir := t.TempDir()
	s1 := startTest(t, dir, allowAll, "")
	evs := turn(t, s1, "Remember the code word kiwi. Reply with ok.", 3*time.Minute)
	id := find(evs, EventInit).Init.SessionID
	s1.Close()
	s2 := startTest(t, dir, allowAll, id)
	evs = turn(t, s2, "What was the code word? Reply with the word only.", 3*time.Minute)
	if !strings.Contains(strings.ToLower(allText(evs)), "kiwi") {
		t.Fatalf("resume lost context; text: %q", allText(evs))
	}
}

func TestIntegrationInterrupt(t *testing.T) {
	s := startTest(t, t.TempDir(), allowAll, "")
	if err := s.Send(context.Background(), "Run the Bash command `sleep 120`, then say done."); err != nil {
		t.Fatal(err)
	}
	var interruptedAt time.Time
	deadline := time.After(4 * time.Minute)
	for {
		select {
		case ev, ok := <-s.Events():
			if !ok {
				t.Fatal("events closed")
			}
			if ev.Kind == EventToolUse && interruptedAt.IsZero() {
				time.Sleep(2 * time.Second)
				if err := s.Interrupt(context.Background()); err != nil {
					t.Fatal(err)
				}
				interruptedAt = time.Now()
			}
			if ev.Kind == EventResult {
				if interruptedAt.IsZero() {
					t.Fatal("result before any tool use")
				}
				if took := time.Since(interruptedAt); took > 30*time.Second {
					t.Fatalf("turn ended %s after interrupt; interrupt did not stop it", took)
				}
				return
			}
		case <-deadline:
			t.Fatal("no result")
		}
	}
}

func TestIntegrationLongPermissionWait(t *testing.T) {
	if os.Getenv("TGSYNC_LONG") == "" {
		t.Skip("set TGSYNC_LONG=1 to check a 5 minute permission wait")
	}
	dir := t.TempDir()
	s := startTest(t, dir, func(ctx context.Context, r PermissionRequest) PermissionDecision {
		time.Sleep(5 * time.Minute)
		return PermissionDecision{Allow: true}
	}, "")
	turn(t, s, "Use the Write tool to create late.txt containing hi.", 10*time.Minute)
	if _, err := os.Stat(filepath.Join(dir, "late.txt")); err != nil {
		t.Fatalf("late.txt missing after long wait: %v", err)
	}
}

func TestIntegrationFork(t *testing.T) {
	dir := t.TempDir()
	s1 := startTest(t, dir, allowAll, "")
	evs := turn(t, s1, "Remember the code word mango. Reply with ok.", 3*time.Minute)
	orig := find(evs, EventInit).Init.SessionID
	s2, err := SDKRunner{}.Start(context.Background(), StartOptions{
		Cwd: dir, Model: testModel(), ResumeID: orig, Fork: true,
		SettingSources: []string{"user", "project", "local"}, CanUseTool: allowAll,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s2.Close() })
	evs = turn(t, s2, "What was the code word? Reply with the word only.", 3*time.Minute)
	if id := find(evs, EventInit).Init.SessionID; id == "" || id == orig {
		t.Fatalf("fork must get a new session id, got %q (orig %q)", id, orig)
	}
	if !strings.Contains(strings.ToLower(allText(evs)), "mango") {
		t.Fatalf("fork lost context; text: %q", allText(evs))
	}
}

func TestIntegrationCustomToolAndPrompt(t *testing.T) {
	dir := t.TempDir()
	var mu sync.Mutex
	var sent []string
	s, err := SDKRunner{}.Start(context.Background(), StartOptions{
		Cwd: dir, Model: testModel(), SettingSources: []string{"user", "project", "local"}, CanUseTool: allowAll,
		AppendSystemPrompt: "When the user must review a file, call the send_file tool with its path.",
		Tools: []Tool{{
			Name: "send_file", Description: "Send a project file to the user for review.",
			Schema: map[string]any{"type": "object", "properties": map[string]any{
				"path": map[string]any{"type": "string"}, "caption": map[string]any{"type": "string"},
			}, "required": []any{"path"}},
			Handler: func(args map[string]any) (string, error) {
				p, _ := args["path"].(string)
				mu.Lock()
				sent = append(sent, p)
				mu.Unlock()
				return "sent", nil
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	evs := turn(t, s, "Create notes.md containing hi with the Write tool, then send it to me for review.", 3*time.Minute)
	mu.Lock()
	defer mu.Unlock()
	if len(sent) == 0 || !strings.HasSuffix(sent[0], "notes.md") {
		t.Fatalf("send_file not called; sent=%v text=%q", sent, allText(evs))
	}
	init := find(evs, EventInit)
	found := false
	for _, n := range init.Init.SlashCommands {
		if n == "compact" {
			found = true
		}
	}
	t.Logf("tool calls=%v, mcp=%v, compact command present=%v", sent, init.Init.MCPServers, found)
}
