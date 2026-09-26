// Package agent defines how the node talks to Claude Code sessions.
package agent

import (
	"context"
	"time"
)

// EventKind tells which fields of Event are set.
type EventKind int

const (
	EventInit         EventKind = iota + 1 // Init
	EventText                              // Text
	EventToolUse                           // ToolUseID, ToolName, ToolInput
	EventToolResult                        // ToolUseID, IsError, Text
	EventResult                            // Result; the turn is over
	EventError                             // Err
	EventHook                              // Text: a hook message meant for the user
	EventTaskStarted                       // Task: a subagent or background shell started
	EventTaskProgress                      // Task: counters of a running task
	EventTaskDone                          // Task: Status, Summary, OutputFile
	EventRateLimit                         // Limit: the subscription limit state changed
	EventCompacted                         // Text: trigger (auto/manual), Tokens: size before
)

// Event is one thing that happened in a session.
// ParentToolUseID is set for events that come from a subagent.
type Event struct {
	Kind            EventKind
	ParentToolUseID string
	Text            string
	ToolUseID       string
	ToolName        string
	ToolInput       map[string]any
	IsError         bool
	Init            *InitInfo
	Result          *ResultInfo
	Err             error
	Task            *TaskInfo
	Limit           *RateLimit
	Tokens          int
}

// TaskInfo describes a task the CLI runs beside the turn: a subagent,
// a workflow or a background shell.
type TaskInfo struct {
	ID          string
	ToolUseID   string // the tool call that started it
	Type        string // local_agent, local_workflow, local_bash, ...
	Description string
	Status      string // EventTaskDone: completed, failed or stopped
	ToolUses    int
	Tokens      int
	Duration    time.Duration
	LastTool    string
	Summary     string
	OutputFile  string // the task's transcript (JSONL)
}

// IsAgent reports whether the task is a local subagent or workflow, the
// work whose end wakes the agent. Shells and remote agents, which may never
// finish, are left out. An unknown (empty) type counts as an agent.
func (t *TaskInfo) IsAgent() bool {
	return t.Type == "" || t.Type == "local_agent" || t.Type == "local_workflow"
}

// InitInfo comes from the CLI system/init message.
type InitInfo struct {
	SessionID     string
	Model         string
	MCPServers    []MCPServer
	SlashCommands []string
	Plugins       []string
}

// MCPServer is an MCP server and its connection status.
type MCPServer struct{ Name, Status string }

// ResultInfo summarises a finished turn.
type ResultInfo struct {
	Subtype    string
	IsError    bool
	DurationMS int
	NumTurns   int
	CostUSD    float64
	Text       string
	Models     []ModelUsage
}

// RateLimit is the state of one subscription limit window.
type RateLimit struct {
	Window      string  // five_hour, seven_day, seven_day_opus, seven_day_sonnet, overage
	Status      string  // allowed, allowed_warning, rejected
	Utilization float64 // 0–1; -1 when the CLI did not report it
	ResetsAt    time.Time
}

// ModelUsage is what one model used in a turn.
type ModelUsage struct {
	Model         string
	Input         int
	Output        int
	CacheRead     int
	CacheCreate   int
	CostUSD       float64
	ContextWindow int
}

// Category is one part of the context window.
type Category struct {
	Name   string
	Tokens int
}

// ContextInfo is how full a session's context window is.
type ContextInfo struct {
	Model         string
	Total, Max    int
	Percent       float64
	Categories    []Category
	AutoCompact   bool
	AutoCompactAt int // tokens; 0 when unknown
}

// PermissionRequest asks whether a tool call may run.
type PermissionRequest struct {
	ToolName    string
	Input       map[string]any
	ToolUseID   string
	Title       string
	BlockedPath string
}

// PermissionDecision answers a PermissionRequest.
// A nil UpdatedInput keeps the original input. Message explains a deny to the agent.
type PermissionDecision struct {
	Allow        bool
	UpdatedInput map[string]any
	Message      string
}

// CanUseToolFunc decides tool calls. It may block until the user answers;
// ctx is cancelled when the session closes.
type CanUseToolFunc func(ctx context.Context, req PermissionRequest) PermissionDecision

// Tool is a function the agent can call, served to the CLI as an in-process
// MCP server named "tgsync" (the agent sees it as mcp__tgsync__<Name>).
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema of the arguments
	Handler     func(args map[string]any) (string, error)
}

// StartOptions configures a new or resumed session.
type StartOptions struct {
	Cwd            string
	ResumeID       string
	Fork           bool // with ResumeID: continue a copy under a new session id
	PermissionMode string
	SettingSources []string
	Env            map[string]string
	CLIPath        string
	Model          string
	CanUseTool     CanUseToolFunc
	Settings       string // JSON passed as --settings
	HookEvents     bool   // report hook messages as EventHook
	// AppendSystemPrompt is added to Claude Code's own system prompt.
	AppendSystemPrompt string
	Tools              []Tool
}

// Runner starts sessions.
type Runner interface {
	Start(ctx context.Context, opts StartOptions) (Session, error)
}

// Session is a live Claude Code process.
type Session interface {
	// Events is closed when the process exits or the session is closed.
	Events() <-chan Event
	Send(ctx context.Context, text string) error
	Interrupt(ctx context.Context) error
	// StopTask stops one running task (a subagent) without ending the turn.
	StopTask(ctx context.Context, id string) error
	// ContextUsage reports how full the context window is.
	ContextUsage(ctx context.Context) (*ContextInfo, error)
	SetPermissionMode(ctx context.Context, mode string) error
	// Close stops the process and cancels pending CanUseTool calls. It is idempotent.
	Close() error
}
