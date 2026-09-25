package agent

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"

	claude "github.com/ProjAnvil/claude-agent-sdk-golang"
)

// SDKRunner starts real Claude Code processes through the Go Agent SDK.
type SDKRunner struct{}

// Start launches claude in o.Cwd. The process lives on its own context, not on ctx,
// so it survives the Telegram request that started it.
func (SDKRunner) Start(_ context.Context, o StartOptions) (Session, error) {
	opts := claude.DefaultOptions()
	opts.MaxBufferSize = 16 << 20
	opts.CWD = o.Cwd
	opts.CLIPath = o.CLIPath
	opts.Env = o.Env
	opts.Resume = o.ResumeID
	opts.ForkSession = o.Fork && o.ResumeID != ""
	opts.Settings = o.Settings
	opts.IncludeHookEvents = o.HookEvents
	opts.Model = o.Model
	if o.PermissionMode != "" {
		opts.PermissionMode = claude.PermissionMode(o.PermissionMode)
	}
	// Without a preset the SDK passes --system-prompt "" and the agent runs
	// without Claude Code's system prompt.
	opts.SystemPromptPreset = &claude.SystemPromptPreset{Type: "preset", Preset: "claude_code", Append: o.AppendSystemPrompt}
	if len(o.Tools) > 0 {
		tools := make([]claude.SdkMcpTool, 0, len(o.Tools))
		for _, t := range o.Tools {
			h := t.Handler
			tools = append(tools, claude.Tool(t.Name, t.Description, t.Schema).Handler(
				func(args map[string]interface{}) (map[string]interface{}, error) {
					text, err := h(args)
					if err != nil {
						return claude.ToolErrorResponse(err.Error()), nil
					}
					return claude.ToolResponse(text), nil
				}))
		}
		opts.MCPServers = map[string]claude.MCPServerConfig{"tgsync": claude.CreateSdkMcpServer("tgsync", "1.0.0", tools)}
	}
	for _, src := range o.SettingSources {
		opts.SettingSources = append(opts.SettingSources, claude.SettingSource(src))
	}

	sctx, cancel := context.WithCancel(context.Background())
	if o.CanUseTool != nil {
		opts.CanUseTool = func(tool string, input map[string]interface{}, pc claude.ToolPermissionContext) (claude.PermissionResult, error) {
			d := o.CanUseTool(sctx, PermissionRequest{
				ToolName: tool, Input: input, ToolUseID: pc.ToolUseID, Title: pc.Title, BlockedPath: pc.BlockedPath,
			})
			if d.Allow {
				in := d.UpdatedInput
				if in == nil {
					in = input
				}
				return &claude.PermissionResultAllow{Behavior: "allow", UpdatedInput: in}, nil
			}
			return &claude.PermissionResultDeny{Behavior: "deny", Message: d.Message}, nil
		}
	}

	client := claude.NewClient(opts)
	if err := client.Connect(sctx); err != nil {
		cancel()
		return nil, fmt.Errorf("start claude: %w", err)
	}
	s := &sdkSession{client: client, cancel: cancel, events: make(chan Event, 256)}
	go s.pump(sctx)
	return s, nil
}

type sdkSession struct {
	client *claude.ClaudeSDKClient
	cancel context.CancelFunc
	events chan Event
}

// pump reads one turn after another. A stream that ends without a result
// means the CLI process is gone, so the events channel is closed.
func (s *sdkSession) pump(ctx context.Context) {
	defer close(s.events)
	defer func() {
		// Anything else that panics here ends only this process: the closed
		// events channel tells the session that claude is gone.
		if r := recover(); r != nil {
			slog.Error("panic reading claude events", "panic", r, "stack", string(debug.Stack()))
			s.cancel()
		}
	}()
	for {
		ch, err := s.client.ReceiveResponse(ctx)
		if err != nil {
			return
		}
		gotResult := false
		for msg := range ch {
			for _, ev := range safeConvert(msg) {
				if ev.Kind == EventResult {
					gotResult = true
				}
				select {
				case s.events <- ev:
				case <-ctx.Done():
					return
				}
			}
		}
		if !gotResult {
			return
		}
	}
}

// safeConvert is convert on pump's goroutine, where a panic would end the
// node: a message that breaks it is logged and becomes an error event.
func safeConvert(msg claude.Message) (evs []Event) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("panic converting a claude message", "type", fmt.Sprintf("%T", msg), "panic", r, "stack", string(debug.Stack()))
			evs = []Event{{Kind: EventError, Err: fmt.Errorf("tgsync: сообщение claude пропущено из-за внутренней ошибки: %v", r)}}
		}
	}()
	return convert(msg)
}

func (s *sdkSession) Events() <-chan Event { return s.events }

func (s *sdkSession) Send(ctx context.Context, text string) error { return s.client.Send(ctx, text) }

func (s *sdkSession) Interrupt(ctx context.Context) error { return s.client.Interrupt(ctx) }

func (s *sdkSession) StopTask(ctx context.Context, id string) error {
	return s.client.StopTask(ctx, id)
}

func (s *sdkSession) SetPermissionMode(ctx context.Context, mode string) error {
	return s.client.SetPermissionMode(ctx, claude.PermissionMode(mode))
}

func (s *sdkSession) Close() error {
	s.cancel()
	return s.client.Close()
}

func (s *sdkSession) ContextUsage(ctx context.Context) (*ContextInfo, error) {
	r, err := s.client.GetContextUsage(ctx)
	if err != nil {
		return nil, err
	}
	info := &ContextInfo{Model: r.Model, Total: r.TotalTokens, Max: r.MaxTokens, Percent: r.Percentage,
		AutoCompact: r.IsAutoCompactEnabled}
	if r.AutoCompactThreshold != nil {
		info.AutoCompactAt = *r.AutoCompactThreshold
	}
	for _, c := range r.Categories {
		info.Categories = append(info.Categories, Category{Name: c.Name, Tokens: c.Tokens})
	}
	return info, nil
}
