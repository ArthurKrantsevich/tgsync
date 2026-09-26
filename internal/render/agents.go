package render

import (
	"fmt"
	"strings"
	"time"
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
	verb, ready := "работают", "готово"
	if running == 1 {
		verb = "работает"
	}
	if done == 1 {
		ready = "готов"
	}
	lines := []string{fmt.Sprintf("🤖 <b>Агенты</b> · %d %s · %d %s", running, verb, done, ready)}
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
		lines = append(lines, fmt.Sprintf("… ещё %d %s", hidden.Running, plural(hidden.Running, "работает", "работают")))
	}
	if hidden.Done > 0 {
		lines = append(lines, fmt.Sprintf("… ещё %d %s", hidden.Done, plural(hidden.Done, "готовый", "готовых")))
	}
	return strings.Join(lines, "\n")
}

// plural picks the Russian form for a count: one for 1, 21, 31…, many otherwise.
func plural(n int, one, many string) string {
	if n%10 == 1 && n%100 != 11 {
		return one
	}
	return many
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

var finishedWord = map[string]string{"completed": "Готово", "failed": "Ошибка", "stopped": "Остановлен"}

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
		caller = "основной агент"
	}
	lines := []string{title, "Вызвал: " + Escape(caller)}
	tools := ""
	if c.ToolUses > 0 {
		tools = fmt.Sprintf(" · %d инстр.", c.ToolUses)
	}
	if c.State == "running" {
		lines = append(lines, "Работает "+Duration(now.Sub(c.Started))+tools)
		if c.Action != "" {
			lines = append(lines, "Сейчас: "+Escape(oneLine(c.Action, 200)))
		}
		return strings.Join(lines, "\n")
	}
	word := finishedWord[c.State]
	if word == "" {
		word = "Завершён"
	}
	lines = append(lines, word+" за "+Duration(c.Took)+tools)
	if s := strings.TrimSpace(c.Summary); s != "" {
		if r := []rune(s); len(r) > maxCardSummary {
			s = string(r[:maxCardSummary-1]) + "…"
		}
		lines = append(lines, "\n<i>"+Escape(s)+"</i>")
	}
	return strings.Join(lines, "\n")
}
