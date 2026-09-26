package permissions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ArthurKrantsevich/tgsync/internal/agent"
	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/render"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/sudo"
	"github.com/ArthurKrantsevich/tgsync/internal/telegram"
)

// SessionInfo tells the broker where a session lives.
type SessionInfo struct {
	ThreadID   int
	Project    string
	ProjectDir string
	Protected  []string
	OnWait     func(waiting bool) // true while the agent waits for the user
}

// Broker turns permission requests and AskUserQuestion calls into Telegram buttons.
type Broker struct {
	api telegram.API
	st  *store.Store

	sudo SudoPolicy

	mu        sync.Mutex
	seq       int
	pending   map[string]*pending
	awaiting  map[int]*pending      // thread → question waiting for a typed answer
	passwords map[int]*passwordWait // thread → sudo password prompt
}

// SudoPolicy grants one-off access to the sudo password (see package sudo).
type SudoPolicy interface {
	Enabled() bool
	Grant(thread int, token string, uses int)
	Revoke(thread int)
}

type pending struct {
	id       string
	threadID int
	msgID    int
	text     string
	project  string
	dir      string
	tool     string
	input    map[string]any
	done     chan agent.PermissionDecision
	finished bool
	sudo     bool
	noAlways bool // no "Always": sudo or a command reaching tgsync's folder
	created  time.Time
	reminded time.Time

	questions []question
	qi        int
	answers   map[string]string
	selected  map[int]bool
}

type question struct {
	Text    string
	Header  string
	Options []string
	Multi   bool
}

func NewBroker(api telegram.API, st *store.Store) *Broker {
	return &Broker{api: api, st: st, pending: map[string]*pending{}, awaiting: map[int]*pending{}, passwords: map[int]*passwordWait{}}
}

// SetSudo enables approving sudo commands.
func (b *Broker) SetSudo(p SudoPolicy) { b.sudo = p }

func deny(msg string) agent.PermissionDecision { return agent.PermissionDecision{Message: msg} }

// CanUseTool returns the permission callback for one session.
func (b *Broker) CanUseTool(si SessionInfo) agent.CanUseToolFunc {
	return func(ctx context.Context, req agent.PermissionRequest) agent.PermissionDecision {
		if req.ToolName == "AskUserQuestion" {
			qs := parseQuestions(req.Input)
			if len(qs) == 0 {
				return agent.PermissionDecision{Allow: true}
			}
			p := b.add(si, req, func(p *pending) { p.questions, p.answers = qs, map[string]string{} })
			if err := b.showQuestion(ctx, p); err != nil {
				b.remove(p)
				return deny("tgsync: " + err.Error())
			}
			return b.wait(ctx, si, p)
		}
		rules, err := b.st.Rules(ctx, si.Project)
		if err != nil {
			return deny("tgsync: " + err.Error())
		}
		in := Input{Tool: req.ToolName, Args: req.Input, ProjectDir: si.ProjectDir, Protected: si.Protected, Rules: rules}
		d, reason := Evaluate(in)
		isSudo := d == Deny && reason == SudoOffReason() && b.sudo != nil && b.sudo.Enabled()
		switch {
		case d == Allow:
			return agent.PermissionDecision{Allow: true}
		case d == Deny && !isSudo:
			return deny(reason)
		}
		// A command that may reach tgsync's folder needs a tap in every mode.
		confirm := d == Confirm || (isSudo && reachesTgsync(in))
		if isSudo {
			// Refuse before asking what could not run after approval.
			cmd, _ := req.Input["command"].(string)
			if _, n, err := sudo.RewriteCommand(cmd, "check"); err != nil || n == 0 {
				return deny(sudoRefusal(err))
			}
		}
		if mode := b.ApproveMode(ctx); !confirm && (mode == ApproveAll || (mode == ApproveNoSudo && !isSudo)) {
			return b.autoAllow(ctx, si, req, isSudo)
		}
		text := permissionText(req, si.ProjectDir)
		if isSudo {
			text = i18n.T("perm.sudo_root") + text
		}
		if confirm {
			text = "⚠️ " + i18n.T("perm.reach") + "\n" + text
		}
		p := b.add(si, req, func(p *pending) { p.sudo, p.noAlways, p.text = isSudo, isSudo || confirm, text })
		kb := telegram.Keyboard{{
			{Text: i18n.T("perm.btn.allow"), Data: "p:" + p.id + ":a"},
			{Text: i18n.T("perm.btn.deny"), Data: "p:" + p.id + ":d"},
		}}
		if r, ok := AlwaysRule(req.ToolName, req.Input, si.ProjectDir); ok && !p.noAlways {
			label := r.Pattern
			switch {
			case fileWrite[r.Tool]:
				label = i18n.T("perm.always_in", r.Tool, clip(r.Pattern, 60))
			case label == "":
				label = r.Tool
			}
			kb = append(kb, []telegram.Button{{Text: i18n.T("perm.btn.always", label), Data: "p:" + p.id + ":A"}})
		}
		id, err := b.api.SendMessage(ctx, si.ThreadID, p.text, kb, false)
		if err != nil {
			b.remove(p)
			return deny("tgsync: " + err.Error())
		}
		b.mu.Lock()
		p.msgID = id
		b.mu.Unlock()
		return b.wait(ctx, si, p)
	}
}

// add registers a request. setup fills the rest of it before other
// goroutines can see it.
func (b *Broker) add(si SessionInfo, req agent.PermissionRequest, setup func(*pending)) *pending {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	p := &pending{id: strconv.Itoa(b.seq), threadID: si.ThreadID, project: si.Project, dir: si.ProjectDir,
		tool: req.ToolName, input: req.Input, done: make(chan agent.PermissionDecision, 1), created: time.Now()}
	setup(p)
	b.pending[p.id] = p
	return p
}

func (b *Broker) remove(p *pending) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, p.id)
	if b.awaiting[p.threadID] == p {
		delete(b.awaiting, p.threadID)
	}
}

func (b *Broker) wait(ctx context.Context, si SessionInfo, p *pending) agent.PermissionDecision {
	if si.OnWait != nil {
		si.OnWait(true)
		defer si.OnWait(false)
	}
	select {
	case d := <-p.done:
		return d
	case <-ctx.Done():
		b.mu.Lock()
		p.finished = true
		text, msgID := p.text, p.msgID
		b.mu.Unlock()
		b.remove(p)
		if msgID != 0 {
			_ = b.api.EditMessage(context.Background(), msgID, text+"\n\n"+i18n.T("perm.session_ended"), nil)
		}
		return deny("session closed")
	}
}

// finish resolves p once; it reports false when p was already resolved.
func (b *Broker) finish(p *pending, d agent.PermissionDecision) bool {
	b.mu.Lock()
	if p.finished {
		b.mu.Unlock()
		return false
	}
	p.finished = true
	b.mu.Unlock()
	b.remove(p)
	p.done <- d
	return true
}

// CancelThread withdraws every open request of a thread, for example when its
// turn ends or its process dies, so stale buttons and typed answers go nowhere.
func (b *Broker) CancelThread(ctx context.Context, thread int) {
	b.mu.Lock()
	var open []*pending
	for _, p := range b.pending {
		if p.threadID == thread {
			open = append(open, p)
		}
	}
	delete(b.awaiting, thread)
	// A password prompt left open would take the user's next message.
	pw := b.passwords[thread]
	delete(b.passwords, thread)
	b.mu.Unlock()
	if pw != nil {
		pw.cancel()
	}
	if b.sudo != nil {
		b.sudo.Revoke(thread)
	}
	for _, p := range open {
		b.mu.Lock()
		text, msgID := p.text, p.msgID
		b.mu.Unlock()
		if b.finish(p, deny("request withdrawn: the turn has ended")) && msgID != 0 {
			_ = b.api.EditMessage(ctx, msgID, text+"\n\n"+i18n.T("perm.withdrawn"), nil)
		}
	}
}

// HandleCallback processes broker buttons. It returns false for foreign callback data.
func (b *Broker) HandleCallback(ctx context.Context, u telegram.Update) bool {
	parts := strings.Split(u.CallbackData, ":")
	if len(parts) < 3 || (parts[0] != "p" && parts[0] != "q") {
		return false
	}
	b.mu.Lock()
	p := b.pending[parts[1]]
	b.mu.Unlock()
	if p == nil || p.threadID != u.ThreadID {
		_ = b.api.AnswerCallback(ctx, u.CallbackID, i18n.T("perm.stale"))
		return true
	}
	alert := ""
	if parts[0] == "p" {
		b.onPermission(ctx, p, parts[2])
	} else {
		alert = b.onQuestion(ctx, p, parts[2:])
	}
	_ = b.api.AnswerCallback(ctx, u.CallbackID, alert)
	return true
}

func (b *Broker) onPermission(ctx context.Context, p *pending, action string) {
	var d agent.PermissionDecision
	var note string
	switch action {
	case "a":
		d, note = agent.PermissionDecision{Allow: true}, i18n.T("perm.allowed")
		if p.sudo {
			var ok bool
			if d, ok = b.grantSudo(p.threadID, p.input); !ok {
				note = i18n.T("perm.unparsed")
			}
		}
	case "A":
		if p.noAlways {
			return
		}
		if r, ok := AlwaysRule(p.tool, p.input, p.dir); ok {
			if err := b.st.AddRule(ctx, p.project, r); err != nil {
				note = i18n.T("perm.rule_not_saved", render.Escape(err.Error()))
			}
		}
		d, note = agent.PermissionDecision{Allow: true}, note+i18n.T("perm.allowed_always")
	case "d":
		d = deny(i18n.T("perm.agent.denied"))
		note = i18n.T("perm.denied")
	default:
		return
	}
	b.mu.Lock()
	text, msgID := p.text, p.msgID
	b.mu.Unlock()
	if b.finish(p, d) {
		_ = b.api.EditMessage(ctx, msgID, text+"\n\n"+note, nil)
	}
}

// onQuestion handles q:<id>:<qi>:<action>[:<i>] and returns an alert for the user.
func (b *Broker) onQuestion(ctx context.Context, p *pending, args []string) string {
	if len(args) < 2 {
		return ""
	}
	qi, err := strconv.Atoi(args[0])
	b.mu.Lock()
	if err != nil || p.finished || qi != p.qi || qi >= len(p.questions) {
		b.mu.Unlock()
		return i18n.T("perm.stale")
	}
	q := p.questions[qi]
	action := args[1]
	switch action {
	case "o", "t":
		i := -1
		if len(args) > 2 {
			i, _ = strconv.Atoi(args[2])
		}
		if i < 0 || i >= len(q.Options) {
			b.mu.Unlock()
			return ""
		}
		if action == "o" {
			b.mu.Unlock()
			b.answer(ctx, p, qi, q.Options[i])
			return ""
		}
		p.selected[i] = !p.selected[i]
		text, kb := questionView(p)
		msgID := p.msgID
		b.mu.Unlock()
		_ = b.api.EditMessage(ctx, msgID, text, kb)
		return ""
	case "ok":
		var chosen []string
		for i, o := range q.Options {
			if p.selected[i] {
				chosen = append(chosen, o)
			}
		}
		b.mu.Unlock()
		if len(chosen) == 0 {
			return i18n.T("perm.pick_one")
		}
		b.answer(ctx, p, qi, strings.Join(chosen, ", "))
		return ""
	case "w":
		b.awaiting[p.threadID] = p
		text, msgID := p.text, p.msgID
		b.mu.Unlock()
		_ = b.api.EditMessage(ctx, msgID, text+"\n\n"+i18n.T("perm.awaiting_answer"), nil)
		return ""
	}
	b.mu.Unlock()
	return ""
}

// HandleText takes a typed answer after the own-answer button.
func (b *Broker) HandleText(ctx context.Context, threadID int, text string) bool {
	b.mu.Lock()
	p := b.awaiting[threadID]
	qi := 0
	if p != nil {
		qi = p.qi
	}
	b.mu.Unlock()
	if p == nil {
		return false
	}
	b.answer(ctx, p, qi, text)
	return true
}

func (b *Broker) answer(ctx context.Context, p *pending, qi int, ans string) {
	b.mu.Lock()
	if p.finished || p.qi != qi {
		b.mu.Unlock()
		return
	}
	p.answers[p.questions[qi].Text] = ans
	if b.awaiting[p.threadID] == p {
		delete(b.awaiting, p.threadID)
	}
	text, msgID := p.text, p.msgID
	p.qi++
	more := p.qi < len(p.questions)
	answers := make(map[string]any, len(p.answers))
	for k, v := range p.answers {
		answers[k] = v
	}
	b.mu.Unlock()

	_ = b.api.EditMessage(ctx, msgID, text+"\n\n💬 "+render.Escape(ans), nil)
	if more {
		if err := b.showQuestion(ctx, p); err != nil {
			b.finish(p, deny("tgsync: "+err.Error()))
		}
		return
	}
	in := make(map[string]any, len(p.input)+1)
	for k, v := range p.input {
		in[k] = v
	}
	in["answers"] = answers
	b.finish(p, agent.PermissionDecision{Allow: true, UpdatedInput: in})
}

func (b *Broker) showQuestion(ctx context.Context, p *pending) error {
	b.mu.Lock()
	p.selected = map[int]bool{}
	text, kb := questionView(p)
	p.text = text
	b.mu.Unlock()
	id, err := b.api.SendMessage(ctx, p.threadID, text, kb, false)
	if err != nil {
		return err
	}
	b.mu.Lock()
	p.msgID = id
	b.mu.Unlock()
	return nil
}

// questionView renders the current question. Callers hold b.mu.
func questionView(p *pending) (string, telegram.Keyboard) {
	q := p.questions[p.qi]
	text := "❓ "
	if q.Header != "" {
		text += "<b>" + render.Escape(q.Header) + "</b>\n"
	}
	text += render.Escape(q.Text)
	if len(p.questions) > 1 {
		text += i18n.T("perm.question_of", p.qi+1, len(p.questions))
	}
	prefix := fmt.Sprintf("q:%s:%d:", p.id, p.qi)
	var kb telegram.Keyboard
	for i, o := range q.Options {
		if q.Multi {
			mark := "☐ "
			if p.selected[i] {
				mark = "☑ "
			}
			kb = append(kb, []telegram.Button{{Text: mark + o, Data: fmt.Sprintf("%st:%d", prefix, i)}})
		} else {
			kb = append(kb, []telegram.Button{{Text: o, Data: fmt.Sprintf("%so:%d", prefix, i)}})
		}
	}
	last := []telegram.Button{{Text: i18n.T("perm.btn.own_answer"), Data: prefix + "w"}}
	if q.Multi {
		last = append(last, telegram.Button{Text: i18n.T("perm.btn.done"), Data: prefix + "ok"})
	}
	return text, append(kb, last)
}

func parseQuestions(input map[string]any) []question {
	raw, _ := input["questions"].([]any)
	var out []question
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		var q question
		q.Text, _ = m["question"].(string)
		q.Header, _ = m["header"].(string)
		q.Multi, _ = m["multiSelect"].(bool)
		opts, _ := m["options"].([]any)
		for _, o := range opts {
			if om, ok := o.(map[string]any); ok {
				if l, _ := om["label"].(string); l != "" {
					q.Options = append(q.Options, l)
				}
			}
		}
		if q.Text != "" {
			out = append(out, q)
		}
	}
	return out
}

func permissionText(req agent.PermissionRequest, dir string) string {
	var b strings.Builder
	b.WriteString(i18n.T("perm.request", render.Escape(req.ToolName)))
	if req.Title != "" {
		b.WriteString("\n" + render.Escape(req.Title))
	}
	str := func(k string) string { v, _ := req.Input[k].(string); return v }
	switch req.ToolName {
	case "Bash":
		if d := strings.TrimSpace(str("description")); d != "" {
			b.WriteString("\n💬 <i>" + render.Escape(clip(d, 300)) + "</i>")
		}
		b.WriteString("\n<pre>" + render.Escape(clip(str("command"), 700)) + "</pre>")
	case "Edit":
		b.WriteString("\n" + render.Escape(relPath(dir, str("file_path"))))
		b.WriteString("\n<pre>- " + render.Escape(clip(str("old_string"), 350)) + "\n+ " + render.Escape(clip(str("new_string"), 350)) + "</pre>")
	case "Write":
		b.WriteString("\n" + render.Escape(relPath(dir, str("file_path"))))
		b.WriteString("\n<pre>" + render.Escape(clip(str("content"), 700)) + "</pre>")
	default:
		raw, _ := json.MarshalIndent(req.Input, "", "  ")
		b.WriteString("\n<pre>" + render.Escape(clip(string(raw), 700)) + "</pre>")
	}
	return b.String()
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "\n…"
}

func relPath(dir, p string) string {
	if r, err := filepath.Rel(dir, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}

// AskPassword asks the user of a session for the sudo password and waits for
// the next message in the topic, which is deleted right away.
func (b *Broker) AskPassword(ctx context.Context, thread int) (string, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &passwordWait{ch: make(chan typedPassword, 1), cancel: cancel}
	b.mu.Lock()
	if _, busy := b.passwords[thread]; busy {
		b.mu.Unlock()
		return "", errors.New(i18n.T("perm.pw.busy"))
	}
	b.passwords[thread] = w
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.passwords[thread] == w {
			delete(b.passwords, thread)
		}
		b.mu.Unlock()
	}()
	ch := w.ch
	msgID, err := b.api.SendMessage(ctx, thread, i18n.T("perm.pw.ask"), nil, false)
	if err != nil {
		return "", err
	}
	select {
	case got := <-ch:
		note := i18n.T("perm.pw.got")
		if !got.deleted {
			note = i18n.T("perm.pw.got_undeleted")
		}
		_ = b.api.EditMessage(ctx, msgID, note, nil)
		return got.password, nil
	case <-ctx.Done():
		_ = b.api.EditMessage(context.Background(), msgID, i18n.T("perm.pw.timeout_note"), nil)
		return "", errors.New(i18n.T("perm.pw.timeout"))
	}
}

// HandlePassword takes a message as the sudo password when one was asked for
// in this topic, and deletes it from the chat.
// Commands (text starting with "/") are never taken as the password.
func (b *Broker) HandlePassword(ctx context.Context, thread, msgID int, text string) bool {
	if strings.HasPrefix(strings.TrimSpace(text), "/") {
		return false
	}
	b.mu.Lock()
	w, ok := b.passwords[thread]
	delete(b.passwords, thread)
	b.mu.Unlock()
	if !ok {
		return false
	}
	err := b.api.DeleteMessage(ctx, msgID)
	w.ch <- typedPassword{password: text, deleted: err == nil}
	return true
}

// passwordWait is a password prompt; cancel withdraws it when the turn ends.
type passwordWait struct {
	ch     chan typedPassword
	cancel context.CancelFunc
}

type typedPassword struct {
	password string
	deleted  bool
}

// Remind nudges the user about prompts unanswered for longer than every.
// every <= 0 disables reminders.
func (b *Broker) Remind(ctx context.Context, now time.Time, every time.Duration) {
	if every <= 0 {
		return
	}
	b.mu.Lock()
	var due []int
	for _, p := range b.pending {
		last := p.created
		if p.reminded.After(last) {
			last = p.reminded
		}
		if !p.finished && p.msgID != 0 && now.Sub(last) >= every {
			p.reminded = now
			due = append(due, p.threadID)
		}
	}
	b.mu.Unlock()
	for _, thread := range due {
		_, _ = b.api.SendMessage(ctx, thread, i18n.T("perm.remind"), nil, false)
	}
}
