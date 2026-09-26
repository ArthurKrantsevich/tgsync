package render

import (
	"strings"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
)

// AgentEntry is one line of the agents list. Depth is the nesting under
// the agent that called it (0 for agents of the main agent).
type AgentEntry struct {
	Name        string
	Description string
	State       string // running, completed, failed or stopped
	Depth       int
}

// Hidden counts the agents collapsed out of the list.
type Hidden struct{ Running, Done int }

var agentEmoji = map[string]string{"running": "▶", "completed": "✅", "failed": "❌", "stopped": "⏹"}

// AgentEmoji returns the emoji of an agent state.
func AgentEmoji(state string) string {
	if e, ok := agentEmoji[state]; ok {
		return e
	}
	return "•"
}

// AgentsList renders the compact agents panel: who runs, on what, called by whom.
func AgentsList(entries []AgentEntry, hidden Hidden) string {
	running, done := hidden.Running, hidden.Done
	for _, e := range entries {
		if e.State == "running" {
			running++
		} else {
			done++
		}
	}
	lines := []string{i18n.T("render.agents.head", running, i18n.N("render.agents.n.running", running),
		done, i18n.N("render.agents.n.done", done))}
	for _, e := range entries {
		line := strings.Repeat("   ", e.Depth)
		if e.Depth > 0 {
			line += "↳ "
		}
		line += AgentEmoji(e.State) + " <b>" + Escape(oneLine(e.Name, 40)) + "</b>"
		if e.Description != "" {
			line += " — " + Escape(oneLine(e.Description, 100))
		}
		lines = append(lines, line)
	}
	if hidden.Running > 0 {
		lines = append(lines, i18n.N("render.agents.n.more_running", hidden.Running, hidden.Running))
	}
	if hidden.Done > 0 {
		lines = append(lines, i18n.N("render.agents.n.more_done", hidden.Done, hidden.Done))
	}
	return strings.Join(lines, "\n")
}

// AgentCardInfo is what the card of one agent shows.
type AgentCardInfo struct {
	Name        string
	Description string
	State       string
	Caller      string // name of the calling agent; empty for the main agent
	Action      string // last tool call line while running
	Summary     string
	Started     time.Time
	Took        time.Duration
	ToolUses    int
}

// finishedKey holds the catalog keys of the "done in" line per final state.
var finishedKey = map[string]string{
	"completed": "render.agents.took.completed", "failed": "render.agents.took.failed",
	"stopped": "render.agents.took.stopped",
}

// maxCardSummary keeps the card well within one Telegram message.
const maxCardSummary = 1000

// AgentCard renders the details of one agent.
func AgentCard(c AgentCardInfo, now time.Time) string {
	title := AgentEmoji(c.State) + " <b>" + Escape(oneLine(c.Name, 40)) + "</b>"
	if c.Description != "" {
		title += " — " + Escape(oneLine(c.Description, 200))
	}
	caller := c.Caller
	if caller == "" {
		caller = i18n.T("render.agents.main")
	}
	lines := []string{title, i18n.T("render.agents.caller", Escape(caller))}
	tools := ""
	if c.ToolUses > 0 {
		tools = i18n.N("render.agents.n.tools", c.ToolUses, c.ToolUses)
	}
	if c.State == "running" {
		lines = append(lines, i18n.T("render.agents.running_for", Duration(now.Sub(c.Started)))+tools)
		if c.Action != "" {
			lines = append(lines, i18n.T("render.agents.now", Escape(oneLine(c.Action, 200))))
		}
		return strings.Join(lines, "\n")
	}
	key := finishedKey[c.State]
	if key == "" {
		key = "render.agents.took.other"
	}
	lines = append(lines, i18n.T(key, Duration(c.Took))+tools)
	if s := strings.TrimSpace(c.Summary); s != "" {
		if r := []rune(s); len(r) > maxCardSummary {
			s = string(r[:maxCardSummary-1]) + "…"
		}
		lines = append(lines, "\n<i>"+Escape(s)+"</i>")
	}
	return strings.Join(lines, "\n")
}
