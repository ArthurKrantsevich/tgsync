package render

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
)

func TestDuration(t *testing.T) {
	cases := map[time.Duration]string{
		12 * time.Second:                "12s",
		3*time.Minute + 5*time.Second:   "3m05s",
		65*time.Minute + 20*time.Second: "1h05m",
	}
	for d, want := range cases {
		if got := Duration(d); got != want {
			t.Errorf("Duration(%s) = %q, want %q", d, got, want)
		}
	}
}

func TestToolLine(t *testing.T) {
	cwd := osPath("/w/demo")
	cases := []struct {
		name string
		in   map[string]any
		want string
	}{
		{"Bash", map[string]any{"command": "go test ./...\n"}, "▶ go test ./..."},
		{"Bash", map[string]any{"command": "go test ./...", "description": "Прогоняю тесты\nпакета"}, "▶ Прогоняю тесты пакета"},
		{"Edit", map[string]any{"file_path": osPath("/w/demo/src/auth.go")}, "📝 Edit " + filepath.FromSlash("src/auth.go")},
		{"Write", map[string]any{"file_path": osPath("/etc/hosts")}, "📝 Write " + osPath("/etc/hosts")},
		{"Read", map[string]any{"file_path": osPath("/w/demo/a.go")}, "📖 Read a.go"},
		{"Grep", map[string]any{"pattern": "TODO"}, "🔍 Grep TODO"},
		{"Agent", map[string]any{"description": "review code"}, "🤖 review code"},
		{"mcp__x__y", nil, "🔧 mcp__x__y"},
	}
	for _, c := range cases {
		if got := ToolLine(c.name, c.in, cwd); got != c.want {
			t.Errorf("ToolLine(%s) = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestStatusText(t *testing.T) {
	now := time.Now()
	got := StatusText(Status{State: "running", Step: 4, Current: "▶ a < b", Sub: "📖 Read x", Started: now.Add(-3 * time.Minute), LastEvent: now.Add(-12 * time.Second)}, now)
	for _, want := range []string{"▶ 3m00s", "шаг 4", "12s назад", "▶ a &lt; b", "↳ 📖 Read x"} {
		if !strings.Contains(got, want) {
			t.Errorf("status %q misses %q", got, want)
		}
	}
}

func TestResultText(t *testing.T) {
	ok := ResultText(agent.ResultInfo{Subtype: "success", CostUSD: 0.4211}, 14, 3*time.Minute+12*time.Second)
	if ok != "✅ Ход завершён · 3m12s · шагов: 14 · $0.42" {
		t.Fatalf("ok: %q", ok)
	}
	bad := ResultText(agent.ResultInfo{Subtype: "error_during_execution", IsError: true, Text: "limit <reached>"}, 2, time.Second)
	if !strings.HasPrefix(bad, "⚠️") || !strings.Contains(bad, "limit &lt;reached&gt;") {
		t.Fatalf("error: %q", bad)
	}
}

func TestStateEmoji(t *testing.T) {
	for state, want := range map[string]string{"queued": "⏳", "running": "▶", "waiting": "❓", "idle": "💤", "closed": "✅", "unknown": "•"} {
		if got := StateEmoji(state); got != want {
			t.Errorf("StateEmoji(%q) = %q, want %q", state, got, want)
		}
	}
}
