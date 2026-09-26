package agent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

// convert maps one SDK message to zero or more events.
func convert(msg claude.Message) []Event {
	switch m := msg.(type) {
	case *claude.SystemMessage:
		switch m.Subtype {
		case "init":
			return []Event{{Kind: EventInit, Init: parseInit(m.Data)}}
		case "compact_boundary":
			meta, _ := m.Data["compact_metadata"].(map[string]interface{})
			trigger, _ := meta["trigger"].(string)
			pre, _ := meta["pre_tokens"].(float64)
			return []Event{{Kind: EventCompacted, Text: trigger, Tokens: int(pre)}}
		}
		return nil
	case *claude.RateLimitEvent:
		i := m.RateLimitInfo
		rl := &RateLimit{Window: string(i.RateLimitType), Status: string(i.Status), Utilization: -1}
		if i.Utilization != nil {
			rl.Utilization = *i.Utilization
		}
		if i.ResetsAt != nil {
			rl.ResetsAt = time.Unix(*i.ResetsAt, 0)
		}
		return []Event{{Kind: EventRateLimit, Limit: rl}}
	case *claude.TaskStartedMessage:
		return []Event{{Kind: EventTaskStarted, Task: &TaskInfo{ID: m.TaskID, ToolUseID: m.ToolUseID,
			Type: m.TaskType, Description: m.Description}}}
	case *claude.TaskProgressMessage:
		t := &TaskInfo{ID: m.TaskID, ToolUseID: m.ToolUseID, Description: m.Description, LastTool: m.LastToolName}
		t.setUsage(&m.Usage)
		return []Event{{Kind: EventTaskProgress, Task: t}}
	case *claude.TaskNotificationMessage:
		t := &TaskInfo{ID: m.TaskID, ToolUseID: m.ToolUseID, Status: string(m.Status),
			Summary: m.Summary, OutputFile: m.OutputFile}
		t.setUsage(m.Usage)
		return []Event{{Kind: EventTaskDone, Task: t}}
	case *claude.TaskUpdatedMessage:
		// Not every finished task sends a notification; a terminal update ends it too.
		status := map[string]string{"completed": "completed", "failed": "failed", "killed": "stopped", "stopped": "stopped"}[string(m.Status)]
		if status == "" {
			return nil
		}
		return []Event{{Kind: EventTaskDone, Task: &TaskInfo{ID: m.TaskID, Status: status}}}
	case *claude.AssistantMessage:
		var out []Event
		if m.Error != "" {
			out = append(out, Event{Kind: EventError, ParentToolUseID: m.ParentToolUseID, Err: fmt.Errorf("claude: %s", m.Error)})
		}
		for _, b := range m.Content {
			switch blk := b.(type) {
			case *claude.TextBlock:
				if blk.Text != "" {
					out = append(out, Event{Kind: EventText, ParentToolUseID: m.ParentToolUseID, Text: blk.Text})
				}
			case *claude.ToolUseBlock:
				out = append(out, Event{Kind: EventToolUse, ParentToolUseID: m.ParentToolUseID,
					ToolUseID: blk.ID, ToolName: blk.Name, ToolInput: blk.Input})
			}
		}
		return out
	case *claude.UserMessage:
		blocks, _ := m.Content.([]claude.ContentBlock)
		var out []Event
		for _, b := range blocks {
			if r, ok := b.(*claude.ToolResultBlock); ok {
				out = append(out, Event{Kind: EventToolResult, ParentToolUseID: m.ParentToolUseID,
					ToolUseID: r.ToolUseID, IsError: r.IsError, Text: resultText(r.Content)})
			}
		}
		return out
	case *claude.HookEventMessage:
		if text := hookText(m); text != "" {
			return []Event{{Kind: EventHook, Text: text}}
		}
		return nil
	case *claude.ResultMessage:
		r := &ResultInfo{Subtype: m.Subtype, IsError: m.IsError, DurationMS: m.DurationMS,
			NumTurns: m.NumTurns, CostUSD: m.TotalCostUSD, Text: m.Result}
		for name, u := range m.ModelUsage {
			r.Models = append(r.Models, ModelUsage{Model: name, Input: u.InputTokens, Output: u.OutputTokens,
				CacheRead: u.CacheReadInputTokens, CacheCreate: u.CacheCreationInputTokens,
				CostUSD: u.CostUSD, ContextWindow: u.ContextWindow})
		}
		sort.Slice(r.Models, func(i, j int) bool { return r.Models[i].Model < r.Models[j].Model })
		return []Event{{Kind: EventResult, Result: r}}
	}
	return nil
}

func parseInit(d map[string]interface{}) *InitInfo {
	in := &InitInfo{}
	in.SessionID, _ = d["session_id"].(string)
	in.Model, _ = d["model"].(string)
	for _, v := range list(d["mcp_servers"]) {
		if m, ok := v.(map[string]interface{}); ok {
			name, _ := m["name"].(string)
			status, _ := m["status"].(string)
			in.MCPServers = append(in.MCPServers, MCPServer{Name: name, Status: status})
		}
	}
	in.SlashCommands = names(d["slash_commands"])
	in.Plugins = names(d["plugins"])
	return in
}

func list(v interface{}) []interface{} {
	l, _ := v.([]interface{})
	return l
}

// names accepts both ["a", "b"] and [{"name": "a"}, ...].
func names(v interface{}) []string {
	var out []string
	for _, item := range list(v) {
		switch x := item.(type) {
		case string:
			out = append(out, x)
		case map[string]interface{}:
			if n, ok := x["name"].(string); ok {
				out = append(out, n)
			}
		}
	}
	return out
}

// hookText returns what a hook wants the user to see: its systemMessage, or
// a short note when the hook failed. Injected context stays hidden.
func hookText(m *claude.HookEventMessage) string {
	if m.Subtype != "hook_response" {
		return ""
	}
	output, _ := m.Data["output"].(string)
	var out struct {
		SystemMessage string `json:"systemMessage"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &out) == nil && out.SystemMessage != "" {
		return out.SystemMessage
	}
	if outcome, _ := m.Data["outcome"].(string); outcome != "" && outcome != "success" {
		name, _ := m.Data["hook_name"].(string)
		if name == "" {
			name = m.HookEventName
		}
		detail, _ := m.Data["stderr"].(string)
		detail = strings.TrimSpace(detail)
		if r := []rune(detail); len(r) > 300 {
			detail = string(r[:299]) + "…"
		}
		return fmt.Sprintf("hook %s: %s. %s", name, outcome, detail)
	}
	return ""
}

func (t *TaskInfo) setUsage(u *claude.TaskUsage) {
	if u == nil {
		return
	}
	t.ToolUses, t.Tokens = u.ToolUses, u.TotalTokens
	t.Duration = time.Duration(u.DurationMS) * time.Millisecond
}

// resultText returns the text of a tool result: a string, or the text
// blocks of a content list joined by blank lines.
func resultText(content interface{}) string {
	switch c := content.(type) {
	case string:
		return c
	case []interface{}:
		var parts []string
		for _, item := range c {
			if m, ok := item.(map[string]interface{}); ok && m["type"] == "text" {
				if s, _ := m["text"].(string); s != "" {
					parts = append(parts, s)
				}
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return ""
}
