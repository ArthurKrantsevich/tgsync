package permissions

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
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
		return i18n.T("perm.mode.all")
	case ApproveNoSudo:
		return i18n.T("perm.mode.no_sudo")
	}
	return i18n.T("perm.mode.ask")
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
	return errors.New(i18n.T("perm.mode.unknown", mode))
}

// autoAllow allows a request without asking. Destructive commands and sudo
// leave a silent note in the topic, so the user still sees what ran; every
// other call shows up only in the turn's status line, as the agent's
// description of it.
func (b *Broker) autoAllow(ctx context.Context, si SessionInfo, req agent.PermissionRequest, isSudo bool) agent.PermissionDecision {
	d := agent.PermissionDecision{Allow: true}
	if isSudo {
		var ok bool
		if d, ok = b.grantSudo(si.ThreadID, req.Input); !ok {
			return d
		}
	}
	if cmd, _ := req.Input["command"].(string); isSudo || (req.ToolName == "Bash" && destructive(cmd)) {
		_, _ = b.api.SendMessage(ctx, si.ThreadID, i18n.T("perm.auto", autoSummary(req, si.ProjectDir)), nil, true)
	}
	return d
}

func autoSummary(req agent.PermissionRequest, dir string) string {
	str := func(k string) string { v, _ := req.Input[k].(string); return v }
	if cmd := strings.TrimSpace(str("command")); cmd != "" {
		code := "<code>" + render.Escape(clip(cmd, 200)) + "</code>"
		if desc := strings.TrimSpace(str("description")); desc != "" {
			return render.Escape(clip(desc, 200)) + "\n" + code
		}
		return code
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
