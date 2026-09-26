package permissions

import (
	"context"
	"strings"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/testutil"
)

func (f *fixture) setMode(t *testing.T, mode string) {
	t.Helper()
	if err := f.b.SetApproveMode(context.Background(), mode); err != nil {
		t.Fatal(err)
	}
}

func TestApproveModeDefaultsToAsk(t *testing.T) {
	f := newFixture(t)
	if m := f.b.ApproveMode(context.Background()); m != ApproveAsk {
		t.Fatalf("mode: %q", m)
	}
	if err := f.b.SetApproveMode(context.Background(), "bogus"); err == nil {
		t.Fatal("unknown mode accepted")
	}
}

func TestApproveAllAllowsCommandWithNote(t *testing.T) {
	f := newFixture(t)
	f.setMode(t, ApproveAll)
	d := decision(t, f.ask(context.Background(), "Bash", map[string]any{"command": "make build"}))
	if !d.Allow {
		t.Fatalf("decision: %+v", d)
	}
	msgs := f.api.Messages(thread)
	if len(msgs) != 1 || !strings.Contains(msgs[0].HTML, "✅ авто: <code>make build</code>") || msgs[0].Keyboard != nil || !msgs[0].Silent {
		t.Fatalf("messages: %+v", msgs)
	}
}

func TestApproveAllGrantsSudo(t *testing.T) {
	f := newFixture(t)
	fs := &fakeSudo{grants: map[int]int{}}
	f.b.SetSudo(fs)
	f.setMode(t, ApproveAll)
	d := decision(t, f.ask(context.Background(), "Bash", map[string]any{"command": "sudo apt update"}))
	fs.mu.Lock()
	uses, tok := fs.grants[thread], fs.tokens[thread]
	fs.mu.Unlock()
	if !d.Allow || tok == "" || uses != 1 || d.UpdatedInput["command"] != "TGSYNC_SUDO_TOKEN="+tok+" sudo -A apt update" {
		t.Fatalf("decision: %+v uses=%d", d, uses)
	}
}

func TestApproveNoSudoAsksForSudoOnly(t *testing.T) {
	f := newFixture(t)
	f.b.SetSudo(&fakeSudo{grants: map[int]int{}})
	f.setMode(t, ApproveNoSudo)
	if d := decision(t, f.ask(context.Background(), "Bash", map[string]any{"command": "make build"})); !d.Allow {
		t.Fatalf("plain command: %+v", d)
	}
	ch := f.ask(context.Background(), "Bash", map[string]any{"command": "sudo apt update"})
	testutil.Eventually(t, "sudo prompt", func() bool {
		_, ok := f.api.Button(thread, "✅ Разрешить")
		return ok
	})
	f.press(t, "❌")
	if d := decision(t, ch); d.Allow {
		t.Fatalf("sudo allowed without a tap: %+v", d)
	}
}

func TestApproveAllKeepsHardDenies(t *testing.T) {
	f := newFixture(t)
	f.setMode(t, ApproveAll)
	can := f.b.CanUseTool(SessionInfo{ThreadID: thread, Project: "demo", ProjectDir: osPath("/w/demo"), Protected: []string{osPath("/opt/tgsync/.env")}})
	d := can(context.Background(), agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": "cat /opt/tgsync/.env"}})
	if d.Allow {
		t.Fatalf("protected file allowed: %+v", d)
	}
	// SUDO_MODE=off: no sudo policy, sudo stays denied.
	d = can(context.Background(), agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": "sudo id"}})
	if d.Allow {
		t.Fatalf("sudo allowed with SUDO_MODE=off: %+v", d)
	}
}

func TestApproveAllStillAsksAboutTgsyncFolder(t *testing.T) {
	f := newFixture(t)
	f.b.SetSudo(&fakeSudo{grants: map[int]int{}})
	f.setMode(t, ApproveAll)
	if err := f.st.AddRule(context.Background(), "demo", store.Rule{Tool: "Bash", Pattern: "cat"}); err != nil {
		t.Fatal(err)
	}
	si := SessionInfo{ThreadID: thread, Project: "demo", ProjectDir: osPath("/w/demo"), Protected: []string{osPath("/opt/tgsync/.env")}}
	for i, cmd := range []string{"cat /opt/tgs*/.env", "sudo cat /opt/tgs*/.env"} {
		out := make(chan agent.PermissionDecision, 1)
		go func() {
			out <- f.b.CanUseTool(si)(context.Background(), agent.PermissionRequest{ToolName: "Bash", Input: map[string]any{"command": cmd}})
		}()
		testutil.Eventually(t, "prompt for "+cmd, func() bool { return len(f.api.Messages(thread)) == i+1 })
		m := f.api.Messages(thread)[i]
		if m.Keyboard == nil || !strings.Contains(m.HTML, "папке tgsync") {
			t.Fatalf("%s: prompt %+v", cmd, m)
		}
		if _, ok := f.api.Button(thread, "♾"); ok {
			t.Fatalf("%s: «Всегда» must not be offered", cmd)
		}
		f.press(t, "❌")
		if d := decision(t, out); d.Allow {
			t.Fatalf("%s: allowed without a tap", cmd)
		}
	}
}

func TestApproveAllStillAsksQuestions(t *testing.T) {
	f := newFixture(t)
	f.setMode(t, ApproveAll)
	ch := f.ask(context.Background(), "AskUserQuestion", map[string]any{"questions": []any{
		map[string]any{"question": "Какой вариант?", "options": []any{map[string]any{"label": "A"}, map[string]any{"label": "B"}}},
	}})
	f.press(t, "B")
	d := decision(t, ch)
	answers, _ := d.UpdatedInput["answers"].(map[string]any)
	if !d.Allow || answers["Какой вариант?"] != "B" {
		t.Fatalf("decision: %+v", d)
	}
}
