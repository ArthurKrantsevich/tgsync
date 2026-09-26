package render

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

var stateEmoji = map[string]string{
	"queued": "⏳", "starting": "🔄", "running": "▶", "waiting": "❓",
	"idle": "💤", "interrupted": "⏸", "failed": "❌", "closed": "✅",
}

// StateEmoji returns the emoji shown for a session state.
func StateEmoji(state string) string {
	if e, ok := stateEmoji[state]; ok {
		return e
	}
	return "•"
}

// ToolLine describes a tool call in one plain-text line.
func ToolLine(name string, in map[string]any, cwd string) string {
	str := func(k string) string { v, _ := in[k].(string); return v }
	rel := func(p string) string {
		if cwd != "" && filepath.IsAbs(p) {
			if r, err := filepath.Rel(cwd, p); err == nil && !strings.HasPrefix(r, "..") {
				return r
			}
		}
		return p
	}
	switch name {
	case "Bash":
		// The agent's own description ("Running the tests") reads better
		// than the command itself.
		if d := strings.TrimSpace(str("description")); d != "" {
			return "▶ " + oneLine(d, 120)
		}
		return "▶ " + oneLine(str("command"), 120)
	case "Edit", "MultiEdit", "Write":
		return "📝 " + name + " " + rel(str("file_path"))
	case "NotebookEdit":
		return "📝 " + name + " " + rel(str("notebook_path"))
	case "Read":
		return "📖 Read " + rel(str("file_path"))
	case "Glob", "Grep":
		return "🔍 " + name + " " + oneLine(str("pattern"), 80)
	case "WebFetch":
		return "🌐 " + oneLine(str("url"), 100)
	case "WebSearch":
		return "🌐 " + oneLine(str("query"), 100)
	case "Task", "Agent":
		d := str("description")
		if d == "" {
			d = str("subagent_type")
		}
		return "🤖 " + oneLine(d, 80)
	case "TodoWrite":
		return i18n.T("render.tool.todo")
	case "AskUserQuestion":
		return i18n.T("render.tool.ask")
	}
	return "🔧 " + name
}

// Status is the live progress of one turn.
type Status struct {
	State     string
	Step      int
	Note      string // the agent's latest remark between tool calls
	Current   string
	Sub       string
	Started   time.Time
	LastEvent time.Time
}

// StatusText renders the status message of a turn.
func StatusText(s Status, now time.Time) string {
	lines := []string{i18n.T("render.status.head",
		StateEmoji(s.State), Duration(now.Sub(s.Started)), s.Step, Duration(now.Sub(s.LastEvent)))}
	if s.Note != "" {
		lines = append(lines, "💬 <i>"+Escape(s.Note)+"</i>")
	}
	if s.Current != "" {
		lines = append(lines, Escape(s.Current))
	}
	if s.Sub != "" {
		lines = append(lines, "↳ "+Escape(s.Sub))
	}
	return strings.Join(lines, "\n")
}

// ResultText renders the end of a turn.
func ResultText(r agent.ResultInfo, steps int, took time.Duration) string {
	if r.IsError {
		msg := r.Subtype
		if r.Text != "" {
			msg += ": " + r.Text
		}
		return i18n.T("render.result.error", Escape(oneLine(msg, 500)))
	}
	parts := []string{i18n.T("render.result.done"), Duration(took), i18n.T("render.result.steps", steps)}
	if r.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", r.CostUSD))
	}
	return strings.Join(parts, " · ")
}

// NoteLine shortens an intermediate answer of the agent for the status message.
// Markdown marks are dropped: the status message shows it as plain text.
func NoteLine(s string) string {
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	return oneLine(strings.TrimLeft(s, "# "), 300)
}

// InterruptedText renders the end of a turn the node interrupted.
func InterruptedText(steps int, took time.Duration) string {
	return i18n.T("render.result.interrupt", Duration(took), steps)
}

// Duration formats d as 12s, 3m05s or 1h05m.
func Duration(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}
