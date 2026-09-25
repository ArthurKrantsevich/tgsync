// Package sudo lets the agent run sudo commands the user approved in
// Telegram. On approval the command's sudo calls get a one-time token
// (SUDO_ASKPASS=<this binary> TGSYNC_SUDO_TOKEN=… sudo -A …). sudo runs
// this binary as askpass;
// it sends the token over a unix socket, and the node answers with the
// password only for a valid token and only to a process started by sudo.
// The password never enters the agent's environment or context.
package sudo

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// Modes of SUDO_MODE.
const (
	ModeOff      = "off"
	ModeEnv      = "env"
	ModeTelegram = "telegram"
)

// GrantTTL is how long an approval stays usable.
const GrantTTL = 5 * time.Minute

// TokenVar carries the one-time token from the approved sudo call to askpass.
const TokenVar = "TGSYNC_SUDO_TOKEN"

// LoopUses is how many password requests a sudo call inside a loop or a
// function may make: it can run more than once, and its count is not known
// before the command runs.
const LoopUses = 5

// askpassExe is the askpass program put into every approved sudo call: this
// binary, the same one the node runs as.
var askpassExe = os.Executable

// NewToken returns a random one-time token for an approval.
func NewToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func isSudo(w *syntax.Word) bool {
	lit := w.Lit()
	return lit == "sudo" || strings.HasSuffix(lit, "/sudo")
}

// isSudoArg reports whether an argument of another command (env sudo,
// xargs /usr/bin/sudo) names sudo. A relative path such as ./internal/sudo
// is a file or folder argument, not the sudo binary.
func isSudoArg(w *syntax.Word) bool {
	lit := w.Lit()
	return lit == "sudo" || strings.HasPrefix(lit, "/")
}

// sudoCall is one sudo call; repeated means it sits in a loop or a
// function body, so it may run more than once.
type sudoCall struct {
	*syntax.CallExpr
	repeated bool
}

// sudoCalls parses cmd and returns the sudo calls in it (not the word
// "sudo" inside strings or arguments).
func sudoCalls(cmd string) (*syntax.File, []sudoCall, error) {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil, nil, err
	}
	var loops [][2]uint
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.ForClause:
			loops = append(loops, [2]uint{n.DoPos.Offset(), n.DonePos.Offset()})
		case *syntax.WhileClause:
			loops = append(loops, [2]uint{n.DoPos.Offset(), n.DonePos.Offset()})
		case *syntax.FuncDecl:
			loops = append(loops, [2]uint{n.Body.Pos().Offset(), n.Body.End().Offset()})
		}
		return true
	})
	var calls []sudoCall
	syntax.Walk(f, func(n syntax.Node) bool {
		if ce, ok := n.(*syntax.CallExpr); ok && len(ce.Args) > 0 && isSudo(ce.Args[0]) {
			at := ce.Pos().Offset()
			rep := false
			for _, l := range loops {
				rep = rep || (at >= l[0] && at < l[1])
			}
			calls = append(calls, sudoCall{ce, rep})
		}
		return true
	})
	return f, calls, nil
}

// systemSudo lists where a system sudo lives; an absolute path elsewhere
// may be a program of the agent's that passes the token on.
var systemSudo = map[string]bool{"/usr/bin/sudo": true, "/bin/sudo": true, "/usr/sbin/sudo": true, "/sbin/sudo": true,
	"/usr/local/bin/sudo": true, "/run/wrappers/bin/sudo": true}

// pathAssignRe finds assignments to PATH, which choose what "sudo" runs.
var pathAssignRe = regexp.MustCompile(`(^|[^\w$])PATH\+?=`)

// errAskpass refuses commands that could choose the program sudo runs as
// askpass, or the sudo it runs: that program would get the one-time token
// and, from the node, the password.
var errAskpass = errors.New("tgsync: в sudo-команде нельзя задавать SUDO_ASKPASS или PATH, " +
	"объявлять функцию или alias с именем sudo и вызывать sudo не из системной папки (./sudo, /tmp/…/sudo): " +
	"tgsync сам подставляет свою программу-askpass. Перепиши команду.")

// overridesAskpass reports whether cmd could change what an approved sudo
// call runs or which askpass it uses.
func overridesAskpass(cmd string, f *syntax.File, calls []sudoCall) bool {
	if strings.Contains(cmd, "SUDO_ASKPASS") || pathAssignRe.MatchString(cmd) {
		return true
	}
	for _, c := range calls {
		if lit := c.Args[0].Lit(); lit != "sudo" && !systemSudo[lit] {
			return true
		}
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.FuncDecl:
			if n.Name.Value == "sudo" || strings.HasSuffix(n.Name.Value, "/sudo") {
				found = true
			}
		case *syntax.CallExpr:
			if len(n.Args) > 1 && n.Args[0].Lit() == "alias" {
				for _, a := range n.Args[1:] {
					if t, ok := wordText(a); !ok || strings.HasPrefix(strings.TrimSpace(t), "sudo") {
						found = true
					}
				}
			}
		}
		return !found
	})
	return found
}

var sudoRe = regexp.MustCompile(`(^|[\s;&|(\x60])(?:\S*/)?sudo(\s|$)`)

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

// Uses reports whether a shell command runs sudo: called directly, passed
// as a word to another command (env sudo, xargs sudo, find -exec sudo) or
// inside a shell's -c script. Unparsable commands are checked with a
// pattern, erring on the side of "yes".
func Uses(cmd string) bool {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return sudoRe.MatchString(cmd)
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		ce, ok := n.(*syntax.CallExpr)
		if !ok || found {
			return !found
		}
		for i, w := range ce.Args {
			if isSudo(w) && (i == 0 || isSudoArg(w)) {
				found = true
				return false
			}
			if i > 0 && ce.Args[i-1].Lit() == "-c" && shells[filepath.Base(ce.Args[0].Lit())] {
				if script, ok := wordText(w); ok && Uses(script) {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// wordText returns the value of a word made of literal and quoted text.
func wordText(w *syntax.Word) (string, bool) {
	var b strings.Builder
	for _, p := range w.Parts {
		switch p := p.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, q := range p.Parts {
				lit, ok := q.(*syntax.Lit)
				if !ok {
					return "", false
				}
				b.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return b.String(), true
}

// blockingFlags keep sudo from asking askpass: -n fails instead of asking,
// -S reads a password from stdin, which the agent does not have.
var blockingFlags = map[string]bool{"-n": true, "--non-interactive": true, "-S": true, "--stdin": true}

// valueFlags are sudo short options that take a value, either the rest of
// the word (-uroot) or the next word (-u root).
const valueFlags = "ugpCDhrRtTU"

// shortFlags drops n and S from a cluster of short sudo flags (without the
// dash). Letters after a value flag are its value and are kept as they are.
// takesNext reports that the cluster ends with a value flag.
func shortFlags(cluster string) (kept string, takesNext bool) {
	var b strings.Builder
	for i, c := range cluster {
		if strings.ContainsRune(valueFlags, c) {
			b.WriteString(cluster[i:])
			return b.String(), i == len(cluster)-1
		}
		if c != 'n' && c != 'S' {
			b.WriteRune(c)
		}
	}
	return b.String(), false
}

// RewriteCommand prefixes every sudo call with tgsync's askpass program and
// the one-time token and makes it use askpass (-A), dropping flags that
// would bypass askpass. It returns the rewritten command and the number of
// password requests to allow: one per call, LoopUses for a call in a loop
// or a function. Commands that could swap the askpass program or the sudo
// binary are refused.
func RewriteCommand(cmd, token string) (string, int, error) {
	f, calls, err := sudoCalls(cmd)
	if err != nil {
		return "", 0, fmt.Errorf("не удалось разобрать команду: %v", err)
	}
	if overridesAskpass(cmd, f, calls) {
		return "", 0, errAskpass
	}
	exe, err := askpassExe()
	if err != nil {
		return "", 0, fmt.Errorf("tgsync: не найден свой исполняемый файл для askpass: %v", err)
	}
	qexe, err := syntax.Quote(exe, syntax.LangBash)
	if err != nil {
		return "", 0, fmt.Errorf("tgsync: путь askpass не записать в команду: %v", err)
	}
	// After the user's own VAR=value words, right before sudo, so these win.
	prefix := "SUDO_ASKPASS=" + qexe + " " + TokenVar + "=" + token + " "
	type edit struct {
		at, end int // replaces cmd[at:end]
		text    string
	}
	var edits []edit
	uses := 0
	for _, c := range calls {
		ce := c.CallExpr
		if c.repeated {
			uses += LoopUses
		} else {
			uses++
		}
		w := ce.Args[0]
		edits = append(edits, edit{int(w.Pos().Offset()), int(w.Pos().Offset()), prefix})
		hasA := false
		for i := 1; i < len(ce.Args); i++ {
			lit := ce.Args[i].Lit()
			if !strings.HasPrefix(lit, "-") || lit == "--" {
				break
			}
			if blockingFlags[lit] {
				// Remove the flag with the space before it.
				edits = append(edits, edit{int(ce.Args[i-1].End().Offset()), int(ce.Args[i].End().Offset()), ""})
				continue
			}
			if strings.HasPrefix(lit, "--") {
				continue
			}
			// A cluster of short flags such as -nA or -Su root.
			kept, takesNext := shortFlags(lit[1:])
			hasA = hasA || strings.Contains(kept, "A")
			switch kept {
			case lit[1:]:
			case "":
				edits = append(edits, edit{int(ce.Args[i-1].End().Offset()), int(ce.Args[i].End().Offset()), ""})
			default:
				edits = append(edits, edit{int(ce.Args[i].Pos().Offset()), int(ce.Args[i].End().Offset()), "-" + kept})
			}
			if takesNext {
				i++
			}
		}
		if !hasA {
			edits = append(edits, edit{int(w.End().Offset()), int(w.End().Offset()), " -A"})
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].at > edits[j].at })
	out := cmd
	for _, e := range edits {
		out = out[:e.at] + e.text + out[e.end:]
	}
	return out, uses, nil
}

// AskFunc asks the user of a session for the password (telegram mode).
type AskFunc func(ctx context.Context, thread int) (string, error)

// Server hands out the sudo password to approved askpass calls.
type Server struct {
	sock     string
	mode     string
	password string
	ask      AskFunc
	now      func() time.Time

	// peerCheck verifies the caller of the socket and returns the pid of
	// the sudo process that started it (0 when unknown); nil disables it
	// (tests).
	peerCheck func(net.Conn) (int, error)
	listener  net.Listener

	mu     sync.Mutex
	grants map[string]*grant // token → grant
}

// MaxAttempts is how many times one sudo call may ask for the password:
// sudo asks again after a mistyped one.
const MaxAttempts = 3

type grant struct {
	thread int
	uses   int // sudo calls the approved command may make
	until  time.Time
	calls  map[int]int // sudo pid → password attempts

	// ask serializes password prompts of one command, so that concurrent
	// sudo calls (sudo a | sudo b) share one answer.
	ask      sync.Mutex
	password string // last answer, reused by the command's next sudo call
	answered bool
}

func NewServer(sock, mode, password string, ask AskFunc) *Server {
	if !peerCheckAvailable && mode != ModeOff && mode != "" {
		slog.Warn("sudo: askpass caller check is not available on this OS; relying on the one-time token and socket permissions")
	}
	return &Server{sock: sock, mode: mode, password: password, ask: ask, now: time.Now,
		peerCheck: peerIsSudo, grants: map[string]*grant{}}
}

// Enabled reports whether sudo commands may be approved at all.
func (s *Server) Enabled() bool { return s != nil && s.mode != ModeOff && s.mode != "" }

// Socket is the unix socket path.
func (s *Server) Socket() string { return s.sock }

// Grant lets the sudo calls of one approved command (identified by token)
// receive the password: up to uses sudo calls within GrantTTL, each with up
// to MaxAttempts tries.
func (s *Server) Grant(thread int, token string, uses int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grants[token] = &grant{thread: thread, uses: uses, until: s.now().Add(GrantTTL), calls: map[int]int{}}
}

// Revoke drops the grants of a session, for example when its turn ends.
func (s *Server) Revoke(thread int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for tok, g := range s.grants {
		if g.thread == thread {
			delete(s.grants, tok)
		}
	}
}

// take counts one password request of sudo process pid (0 when unknown)
// against the grant of token. retry reports that this sudo process asked
// before, so its last answer was wrong.
func (s *Server) take(token string, pid int) (g *grant, retry, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok = s.grants[token]
	if !ok || s.now().After(g.until) {
		delete(s.grants, token)
		return nil, false, false
	}
	n, seen := g.calls[pid]
	switch {
	case seen && pid != 0:
		// The same sudo asks again: the password was mistyped.
		if n >= MaxAttempts {
			return nil, false, false
		}
		g.calls[pid] = n + 1
		return g, true, true
	case g.uses <= 0:
		return nil, false, false
	}
	// A new sudo call. Without its pid every request counts as one.
	g.uses--
	g.calls[pid] = 1
	return g, false, true
}

// Env is added to the agent's environment so sudo -A finds the askpass bridge.
func (s *Server) Env(thread int, exe string) map[string]string {
	if !s.Enabled() {
		return nil
	}
	return map[string]string{
		"SUDO_ASKPASS":        exe,
		"TGSYNC_ASKPASS":      "1",
		"TGSYNC_ASKPASS_SOCK": s.sock,
	}
}

// Listen creates the socket (0600). Serve calls it when needed; calling it
// first lets startup fail loudly instead of approvals failing later.
func (s *Server) Listen() error {
	_ = os.Remove(s.sock)
	l, err := net.Listen("unix", s.sock)
	if err != nil {
		return err
	}
	if err := os.Chmod(s.sock, 0o600); err != nil {
		l.Close()
		return err
	}
	s.listener = l
	return nil
}

// Serve answers askpass calls until ctx is done.
func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}
	l := s.listener
	go func() {
		<-ctx.Done()
		l.Close()
		_ = os.Remove(s.sock)
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.handle(ctx, conn)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(GrantTTL + time.Minute))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	reply := func(ok bool, text string) {
		word := "deny"
		if ok {
			word = "ok"
		}
		fmt.Fprintf(conn, "%s %s\n", word, text)
	}
	pid := 0
	if s.peerCheck != nil {
		var err error
		if pid, err = s.peerCheck(conn); err != nil {
			reply(false, "отказано: запрос не от sudo")
			return
		}
	}
	g, retry, ok := s.take(strings.TrimSpace(strings.TrimPrefix(line, "token ")), pid)
	if !ok {
		reply(false, "нет одобренной sudo-команды с таким токеном или попытки кончились")
		return
	}
	switch s.mode {
	case ModeEnv:
		reply(true, s.password)
	case ModeTelegram:
		// One prompt at a time per command; a later sudo call of the same
		// command reuses the answer unless its own previous one was wrong.
		g.ask.Lock()
		defer g.ask.Unlock()
		if g.answered && !retry {
			reply(true, g.password)
			return
		}
		actx, cancel := context.WithTimeout(ctx, GrantTTL)
		defer cancel()
		pw, err := s.ask(actx, g.thread)
		if err != nil {
			reply(false, err.Error())
			return
		}
		g.password, g.answered = pw, true
		reply(true, pw)
	default:
		reply(false, "sudo выключен")
	}
}

// Askpass is the client side, run by sudo: it prints the password to w.
func Askpass(sock, token string, w io.Writer) error {
	conn, err := net.DialTimeout("unix", sock, 5*time.Second)
	if err != nil {
		return fmt.Errorf("tgsync askpass: нода недоступна: %v", err)
	}
	defer conn.Close()
	if _, err := fmt.Fprintf(conn, "token %s\n", token); err != nil {
		return err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return fmt.Errorf("tgsync askpass: %v", err)
	}
	line = strings.TrimSuffix(line, "\n")
	if pw, ok := strings.CutPrefix(line, "ok "); ok {
		_, err := fmt.Fprintln(w, pw)
		return err
	}
	return errors.New("tgsync askpass: " + strings.TrimPrefix(line, "deny "))
}

// Ping checks that the socket accepts connections.
func Ping(sock string) error {
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return err
	}
	return conn.Close()
}
