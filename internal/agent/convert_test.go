package agent

import (
	"testing"
	"time"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

func TestConvertInit(t *testing.T) {
	evs := convert(&claude.SystemMessage{Subtype: "init", Data: map[string]interface{}{
		"session_id":     "sid-1",
		"model":          "claude-haiku",
		"mcp_servers":    []interface{}{map[string]interface{}{"name": "context7", "status": "needs-auth"}},
		"slash_commands": []interface{}{"compact", "code-review"},
		"plugins":        []interface{}{map[string]interface{}{"name": "superpowers", "path": "/x"}, "caveman"},
	}})
	if len(evs) != 1 || evs[0].Kind != EventInit {
		t.Fatalf("events: %+v", evs)
	}
	in := evs[0].Init
	if in.SessionID != "sid-1" || in.Model != "claude-haiku" {
		t.Fatalf("init: %+v", in)
	}
	if len(in.MCPServers) != 1 || in.MCPServers[0] != (MCPServer{Name: "context7", Status: "needs-auth"}) {
		t.Fatalf("mcp: %+v", in.MCPServers)
	}
	if len(in.SlashCommands) != 2 || len(in.Plugins) != 2 || in.Plugins[0] != "superpowers" || in.Plugins[1] != "caveman" {
		t.Fatalf("commands=%v plugins=%v", in.SlashCommands, in.Plugins)
	}
}

func TestConvertIgnoresOtherSystemMessages(t *testing.T) {
	if evs := convert(&claude.SystemMessage{Subtype: "status"}); len(evs) != 0 {
		t.Fatalf("events: %+v", evs)
	}
}

func TestConvertAssistant(t *testing.T) {
	evs := convert(&claude.AssistantMessage{ParentToolUseID: "p1", Content: []claude.ContentBlock{
		&claude.TextBlock{Text: "hello"},
		&claude.ToolUseBlock{ID: "t1", Name: "Bash", Input: map[string]interface{}{"command": "ls"}},
		&claude.TextBlock{Text: ""},
	}})
	if len(evs) != 2 {
		t.Fatalf("events: %+v", evs)
	}
	if evs[0].Kind != EventText || evs[0].Text != "hello" || evs[0].ParentToolUseID != "p1" {
		t.Fatalf("text: %+v", evs[0])
	}
	if evs[1].Kind != EventToolUse || evs[1].ToolName != "Bash" || evs[1].ToolUseID != "t1" || evs[1].ToolInput["command"] != "ls" {
		t.Fatalf("tool: %+v", evs[1])
	}
}

func TestConvertAssistantError(t *testing.T) {
	evs := convert(&claude.AssistantMessage{Error: "rate_limit"})
	if len(evs) != 1 || evs[0].Kind != EventError || evs[0].Err == nil {
		t.Fatalf("events: %+v", evs)
	}
}

// TestSafeConvertRecoversPanic: pump converts messages on its own goroutine;
// a panic there would end the node. A message that breaks convert becomes
// an error event instead.
func TestSafeConvertRecoversPanic(t *testing.T) {
	evs := safeConvert((*claude.SystemMessage)(nil))
	if len(evs) != 1 || evs[0].Kind != EventError || evs[0].Err == nil {
		t.Fatalf("events: %+v", evs)
	}
}

func TestConvertToolResult(t *testing.T) {
	evs := convert(&claude.UserMessage{Content: []claude.ContentBlock{
		&claude.ToolResultBlock{ToolUseID: "t1", IsError: true},
	}})
	if len(evs) != 1 || evs[0].Kind != EventToolResult || evs[0].ToolUseID != "t1" || !evs[0].IsError {
		t.Fatalf("events: %+v", evs)
	}
	if evs := convert(&claude.UserMessage{Content: "plain text"}); len(evs) != 0 {
		t.Fatalf("string content must be ignored: %+v", evs)
	}
}

func TestConvertResult(t *testing.T) {
	evs := convert(&claude.ResultMessage{Subtype: "success", DurationMS: 1500, NumTurns: 3, TotalCostUSD: 0.25, Result: "done"})
	if len(evs) != 1 || evs[0].Kind != EventResult {
		t.Fatalf("events: %+v", evs)
	}
	r := evs[0].Result
	if r.Subtype != "success" || r.DurationMS != 1500 || r.NumTurns != 3 || r.CostUSD != 0.25 || r.Text != "done" || r.IsError {
		t.Fatalf("result: %+v", r)
	}
}

func TestConvertHookEvents(t *testing.T) {
	msg := func(data map[string]interface{}) *claude.HookEventMessage {
		data["subtype"] = "hook_response"
		return &claude.HookEventMessage{Subtype: "hook_response", HookEventName: "PreToolUse", Data: data}
	}
	evs := convert(msg(map[string]interface{}{"outcome": "success", "output": `{"systemMessage":"⚠ big diff ahead"}`}))
	if len(evs) != 1 || evs[0].Kind != EventHook || evs[0].Text != "⚠ big diff ahead" {
		t.Fatalf("systemMessage: %+v", evs)
	}
	if evs := convert(msg(map[string]interface{}{"outcome": "success", "output": "lots of injected context"})); len(evs) != 0 {
		t.Fatalf("plain context must be hidden: %+v", evs)
	}
	evs = convert(msg(map[string]interface{}{"outcome": "error", "hook_name": "PreToolUse:Bash", "stderr": "gate says no"}))
	if len(evs) != 1 || evs[0].Kind != EventHook || evs[0].Text == "" {
		t.Fatalf("failed hook: %+v", evs)
	}
	if evs := convert(&claude.HookEventMessage{Subtype: "hook_started"}); len(evs) != 0 {
		t.Fatalf("hook_started: %+v", evs)
	}
}

func TestConvertTaskEvents(t *testing.T) {
	started := convert(&claude.TaskStartedMessage{TaskID: "t1", ToolUseID: "tu1", TaskType: "local_agent", Description: "find sudo"})
	if len(started) != 1 || started[0].Kind != EventTaskStarted || started[0].Task.ID != "t1" ||
		started[0].Task.ToolUseID != "tu1" || !started[0].Task.IsAgent() || started[0].Task.Description != "find sudo" {
		t.Fatalf("started: %+v", started)
	}
	prog := convert(&claude.TaskProgressMessage{TaskID: "t1", LastToolName: "Grep",
		Usage: claude.TaskUsage{TotalTokens: 900, ToolUses: 4, DurationMS: 65000}})
	if len(prog) != 1 || prog[0].Kind != EventTaskProgress || prog[0].Task.ToolUses != 4 ||
		prog[0].Task.Duration != 65*time.Second || prog[0].Task.LastTool != "Grep" {
		t.Fatalf("progress: %+v", prog[0].Task)
	}
	done := convert(&claude.TaskNotificationMessage{TaskID: "t1", Status: claude.TaskNotificationStatusCompleted,
		Summary: "found 3", OutputFile: "/tmp/t1.output", Usage: &claude.TaskUsage{ToolUses: 7, DurationMS: 1000}})
	if len(done) != 1 || done[0].Kind != EventTaskDone || done[0].Task.Status != "completed" ||
		done[0].Task.Summary != "found 3" || done[0].Task.OutputFile != "/tmp/t1.output" || done[0].Task.ToolUses != 7 {
		t.Fatalf("done: %+v", done[0].Task)
	}
	for _, typ := range []string{"local_bash", "remote_agent"} {
		other := convert(&claude.TaskStartedMessage{TaskID: "t2", TaskType: typ, Description: "x"})
		if other[0].Task.IsAgent() {
			t.Fatalf("%s must not be tracked as an agent", typ)
		}
	}
}

func TestConvertTaskUpdatedTerminal(t *testing.T) {
	if ev := convert(&claude.TaskUpdatedMessage{TaskID: "t1", Status: "running"}); len(ev) != 0 {
		t.Fatalf("non-terminal update must be ignored: %+v", ev)
	}
	for _, st := range []claude.TaskUpdatedStatus{"killed", "stopped"} {
		ev := convert(&claude.TaskUpdatedMessage{TaskID: "t1", Status: st})
		if len(ev) != 1 || ev[0].Kind != EventTaskDone || ev[0].Task.Status != "stopped" {
			t.Fatalf("%s: %+v", st, ev)
		}
	}
}

func TestConvertToolResultText(t *testing.T) {
	msg := &claude.UserMessage{Content: []claude.ContentBlock{
		&claude.ToolResultBlock{ToolUseID: "tu1", Content: []interface{}{
			map[string]interface{}{"type": "text", "text": "part one"},
			map[string]interface{}{"type": "text", "text": "part two"},
		}},
		&claude.ToolResultBlock{ToolUseID: "tu2", Content: "plain"},
	}}
	ev := convert(msg)
	if len(ev) != 2 || ev[0].Text != "part one\n\npart two" || ev[1].Text != "plain" {
		t.Fatalf("results: %+v", ev)
	}
}

func TestConvertRateLimitAndCompaction(t *testing.T) {
	util, resets := 0.85, int64(1790300000)
	ev := convert(&claude.RateLimitEvent{RateLimitInfo: claude.RateLimitInfo{Status: claude.RateLimitStatusAllowedWarning,
		RateLimitType: claude.RateLimitTypeFiveHour, Utilization: &util, ResetsAt: &resets}})
	if len(ev) != 1 || ev[0].Kind != EventRateLimit || ev[0].Limit.Window != "five_hour" || ev[0].Limit.Status != "allowed_warning" ||
		ev[0].Limit.Utilization != 0.85 || !ev[0].Limit.ResetsAt.Equal(time.Unix(resets, 0)) {
		t.Fatalf("rate limit: %+v", ev)
	}
	ev = convert(&claude.SystemMessage{Subtype: "compact_boundary", Data: map[string]interface{}{
		"compact_metadata": map[string]interface{}{"trigger": "auto", "pre_tokens": float64(182000)}}})
	if len(ev) != 1 || ev[0].Kind != EventCompacted || ev[0].Text != "auto" || ev[0].Tokens != 182000 {
		t.Fatalf("compaction: %+v", ev)
	}
}

func TestConvertResultModels(t *testing.T) {
	ev := convert(&claude.ResultMessage{Subtype: "success", ModelUsage: map[string]claude.ModelUsage{
		"b-model": {InputTokens: 10, OutputTokens: 20, CacheReadInputTokens: 300, CacheCreationInputTokens: 40, CostUSD: 0.5, ContextWindow: 200000},
		"a-model": {InputTokens: 1},
	}})
	ms := ev[0].Result.Models
	if len(ms) != 2 || ms[0].Model != "a-model" || ms[1].Input != 10 || ms[1].Output != 20 || ms[1].CacheRead != 300 ||
		ms[1].CacheCreate != 40 || ms[1].CostUSD != 0.5 || ms[1].ContextWindow != 200000 {
		t.Fatalf("models: %+v", ms)
	}
}

func TestConvertRateLimitWithoutUtilization(t *testing.T) {
	ev := convert(&claude.RateLimitEvent{RateLimitInfo: claude.RateLimitInfo{Status: claude.RateLimitStatusAllowed}})
	if ev[0].Limit.Utilization != -1 || ev[0].Limit.Window != "" {
		t.Fatalf("missing utilization is -1: %+v", ev[0].Limit)
	}
}
