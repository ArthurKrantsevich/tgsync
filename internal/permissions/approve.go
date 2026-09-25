package permissions

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/sudo"
)

// Approve modes: which requests the broker allows without asking. Hard
// denies (tgsync's own files, sudo when SUDO_MODE=off) and questions of the
// agent are never affected.
const (
	ApproveAsk    = "ask"    // a button for every request
	ApproveNoSudo = "nosudo" // everything except sudo is allowed
	ApproveAll    = "all"    // everything is allowed, sudo included
)

// ApproveModes lists the modes in menu order.
var ApproveModes = []string{ApproveAll, ApproveNoSudo, ApproveAsk}

// ApproveLabel is the button text of a mode.
func ApproveLabel(mode string) string {
	switch mode {
	case ApproveAll:
		return "🟢 Всё сам"
	case ApproveNoSudo:
		return "🟡 Всё, кроме sudo"
	}
	return "🔴 По запросу"
}

// ApproveMode returns the node's approve mode; ApproveAsk when unset or
// unreadable.
func (b *Broker) ApproveMode(ctx context.Context) string {
	m, err := b.st.ApproveMode(ctx)
	if err != nil {
		slog.Warn("read approve mode", "err", err)
		return ApproveAsk
	}
	switch m {
	case ApproveAll, ApproveNoSudo:
		return m
	}
	return ApproveAsk
}

// SetApproveMode stores the node's approve mode.
func (b *Broker) SetApproveMode(ctx context.Context, mode string) error {
	switch mode {
	case ApproveAll, ApproveNoSudo, ApproveAsk:
		return b.st.SetApproveMode(ctx, mode)
	}
	return fmt.Errorf("неизвестный режим: %s", mode)
}

// autoAllow allows a request without asking and leaves a silent note in
// the topic, so the user still sees what ran.
func (b *Broker) autoAllow(ctx context.Context, si SessionInfo, req agent.PermissionRequest, isSudo bool) agent.PermissionDecision {
	d := agent.PermissionDecision{Allow: true}
	if isSudo {
		var ok bool
		if d, ok = b.grantSudo(si.ThreadID, req.Input); !ok {
			return d
		}
	}
	_, _ = b.api.SendMessage(ctx, si.ThreadID, "✅ авто: "+autoSummary(req, si.ProjectDir), nil, true)
	return d
}

func autoSummary(req agent.PermissionRequest, dir string) string {
	str := func(k string) string { v, _ := req.Input[k].(string); return v }
	if cmd := strings.TrimSpace(str("command")); cmd != "" {
		return "<code>" + render.Escape(clip(cmd, 200)) + "</code>"
	}
	s := render.Escape(req.ToolName)
	if p := str("file_path"); p != "" {
		s += " " + render.Escape(relPath(dir, p))
	}
	return s
}

// grantSudo rewrites a sudo command to use a one-off token and grants the
// token to the session. It reports false when the command cannot be rewritten.
func (b *Broker) grantSudo(thread int, input map[string]any) (agent.PermissionDecision, bool) {
	cmd, _ := input["command"].(string)
	token := sudo.NewToken()
	rewritten, uses, err := sudo.RewriteCommand(cmd, token)
	if err != nil || uses == 0 {
		return deny(sudoRefusal(err)), false
	}
	in := make(map[string]any, len(input))
	for k, v := range input {
		in[k] = v
	}
	in["command"] = rewritten
	b.sudo.Grant(thread, token, uses)
	return agent.PermissionDecision{Allow: true, UpdatedInput: in}, true
}
