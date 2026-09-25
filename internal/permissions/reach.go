package permissions

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/files"
	"mvdan.cc/sh/v3/syntax"
)

// reachReason is shown on the prompt of a command that may reach tgsync's
// own folder (.env with the bot token and the sudo password, the database).
const reachReason = "команда может обращаться к папке tgsync (.env, база) — нужна кнопка в любом режиме"

// reach finds shell commands that may name tgsync's folder in a spelling
// the literal path check misses: ~, ~user and $HOME, known variables,
// relative paths (also after cd), links in the project, globs, unknown
// variables before /tgsync/, and scripts run by sh -c, eval or a heredoc fed
// to a shell or an interpreter.
//
// This is defense in depth, not a boundary: the agent runs as the same OS
// user, so in the «Всё сам» and «Всё, кроме sudo» modes, or through any
// program it writes and runs, it can still read these files. The check only
// makes the obvious spellings need a tap.
type reach struct {
	project string
	home    string   // the user's home for ~ and $HOME
	dirs    []string // folders holding tgsync's files, as named and resolved
	names   []string // base names of the top folders, e.g. tgsync
	tails   []string // folder/name of each protected file, e.g. tgsync/.env
	fold    bool
	getenv  func(string) string

	// Filled per command.
	bases     []string // folders cd or pushd may move to
	cdUnknown bool     // a cd to a folder not known before the command runs
	recursive bool     // a command that walks folders (grep -r, rg, find …)
}

// reachesTgsync reports whether a Bash call may reach tgsync's folder.
func reachesTgsync(in Input) bool {
	cmd, _ := in.Args["command"].(string)
	return in.Tool == "Bash" && newReach(in.ProjectDir, in.Home, in.Protected).command(cmd, 0)
}

func newReach(project, home string, protected []string) *reach {
	r := &reach{project: filepath.Clean(project), home: homeDir(home), fold: files.FoldCase, getenv: os.Getenv}
	for _, p := range protected {
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		r.dirs = append(r.dirs, d)
		if real, ok := realPath(d); ok && !files.SamePath(real, d) {
			r.dirs = append(r.dirs, real)
		}
		r.tails = append(r.tails, filepath.Base(d)+"/"+filepath.Base(p))
	}
	for _, d := range tgsyncHomes(protected) {
		r.names = append(r.names, filepath.Base(d))
	}
	return r
}

// norm makes shell text comparable with paths: one kind of slash on
// Windows, one case where the file system ignores it.
func (r *reach) norm(s string) string {
	if runtime.GOOS == "windows" {
		s = strings.ReplaceAll(s, `\`, "/")
	}
	if r.fold {
		s = strings.ToLower(s)
	}
	return s
}

// parseFailureConfirms reports whether a command the parser cannot read
// needs a button: yes when it uses something the parser does not support,
// no for a syntax error, which bash rejects too.
func parseFailureConfirms(err error) bool {
	var pe syntax.ParseError
	return !errors.As(err, &pe)
}

// shellNames run their -c argument or a heredoc as a shell script.
var shellNames = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "mksh": true, "ash": true, "busybox": true}

// interpreters run their -c/-e argument or a heredoc as code in another
// language; paths in it are found as plain tokens.
func interpreter(name string) bool {
	switch name {
	case "node", "nodejs", "deno", "bun", "perl", "ruby", "php", "lua", "osascript", "pwsh", "powershell":
		return true
	}
	return strings.HasPrefix(name, "python")
}

// walkers read whole folder trees.
var walkers = map[string]bool{"rg": true, "ag": true, "ack": true, "find": true, "fd": true, "tar": true, "zip": true,
	"7z": true, "rsync": true, "du": true, "tree": true, "scp": true}

func cmdName(ce *syntax.CallExpr) string {
	if len(ce.Args) == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(filepath.Base(unquote(ce.Args[0].Lit()))), ".exe")
}

// command reports whether cmd may reach tgsync's folder.
func (r *reach) command(cmd string, depth int) bool {
	if len(r.dirs) == 0 {
		return false
	}
	text := r.norm(cmd)
	for _, t := range r.tails {
		if strings.Contains(text, r.norm(t)) {
			return true
		}
	}
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return parseFailureConfirms(err)
	}
	r.scan(f)
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *syntax.Word:
			found = r.word(n)
		case *syntax.CallExpr:
			found = r.scripts(n, depth)
		case *syntax.Stmt:
			found = r.heredocs(n, depth)
		}
		return !found
	})
	return found
}

// scan notes where cd and pushd may move to and whether any command walks
// folder trees.
func (r *reach) scan(f *syntax.File) {
	syntax.Walk(f, func(n syntax.Node) bool {
		ce, ok := n.(*syntax.CallExpr)
		if !ok || len(ce.Args) == 0 {
			return true
		}
		name := cmdName(ce)
		if walkers[name] {
			r.recursive = true
		}
		for _, a := range ce.Args[1:] {
			l := a.Lit()
			if l == "--recursive" || (strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && strings.ContainsAny(l, "rR")) {
				r.recursive = true
			}
		}
		if name != "cd" && name != "pushd" {
			return true
		}
		var target *syntax.Word
		for _, a := range ce.Args[1:] {
			if l := a.Lit(); l == "--" || !strings.HasPrefix(l, "-") || l == "-" {
				target = a
				break
			}
		}
		if target == nil {
			r.bases = append(r.bases, r.home)
			return true
		}
		v := r.value(target)
		if v == "-" || strings.Contains(v, wild) {
			r.cdUnknown = true
			return true
		}
		if isAbs(v) {
			r.bases = append(r.bases, files.Abs(r.project, v))
			return true
		}
		for _, b := range append([]string{r.project}, r.bases...) {
			r.bases = append(r.bases, filepath.Join(b, v))
		}
		return true
	})
}

// scripts checks the script a call runs from an argument: sh -c, eval,
// python -c, node -e and the like.
func (r *reach) scripts(ce *syntax.CallExpr, depth int) bool {
	name := cmdName(ce)
	shell := shellNames[name]
	switch {
	case name == "eval":
		var parts []string
		for _, a := range ce.Args[1:] {
			parts = append(parts, r.value(a))
		}
		return r.script(strings.Join(parts, " "), true, depth)
	case shell || interpreter(name):
		for i := 1; i+1 < len(ce.Args); i++ {
			l := ce.Args[i].Lit()
			if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "--") && strings.ContainsAny(l[1:], "ce") {
				if r.script(r.value(ce.Args[i+1]), shell, depth) {
					return true
				}
			}
		}
	}
	return false
}

// heredocs checks a heredoc or here-string fed to a shell or an
// interpreter. Text fed to other commands (cat > notes.md) is only data.
func (r *reach) heredocs(st *syntax.Stmt, depth int) bool {
	ce, ok := st.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	name := cmdName(ce)
	shell := shellNames[name] || name == "eval"
	if !shell && !interpreter(name) {
		return false
	}
	for _, rd := range st.Redirs {
		w := rd.Hdoc
		if rd.Op == syntax.WordHdoc {
			w = rd.Word
		}
		if w != nil && r.script(r.value(w), shell, depth) {
			return true
		}
	}
	return false
}

// script checks code run by a shell (parsed again) or an interpreter
// (paths found as tokens). Unknown parts count as wildcards.
func (r *reach) script(code string, shell bool, depth int) bool {
	code = strings.ReplaceAll(code, wild, "*")
	if shell && depth < 3 {
		sub := &reach{project: r.project, home: r.home, dirs: r.dirs, names: r.names, tails: r.tails, fold: r.fold,
			getenv: r.getenv, bases: r.bases, cdUnknown: r.cdUnknown, recursive: r.recursive}
		if _, err := syntax.NewParser().Parse(strings.NewReader(code), ""); err == nil {
			return sub.command(code, depth+1)
		}
	}
	return r.tokens(code)
}

// tokens finds path-like tokens in code of any language: absolute, ~ or ..
// paths. Each counts as the start of a path, since code can join strings.
func (r *reach) tokens(code string) bool {
	text := r.norm(code)
	for _, t := range r.tails {
		if strings.Contains(text, r.norm(t)) {
			return true
		}
	}
	for _, tok := range strings.FieldsFunc(code, func(c rune) bool { return strings.ContainsRune(" \t\n\"'`()[]{},;+=:|&<>", c) }) {
		if len(tok) < 2 || !(strings.HasPrefix(tok, "/") || strings.HasPrefix(tok, "~") || strings.Contains(tok, "..")) {
			continue
		}
		tok = r.tilde(tok)
		if i := strings.IndexAny(tok, "*?$"); i >= 0 {
			tok = tok[:i]
		}
		if tok != "" && r.path(files.Abs(r.project, tok), true, false) {
			return true
		}
	}
	return false
}

// wild marks the part of a word whose value is not known before it runs.
const wild = "\x00"

// value returns a word as the shell would expand it, with wild for what
// is unknown.
func (r *reach) value(w *syntax.Word) string {
	var b strings.Builder
	for i, part := range w.Parts {
		r.part(&b, part, i == 0, false)
	}
	return b.String()
}

// word checks one shell word: a path the command may open.
func (r *reach) word(w *syntax.Word) bool {
	text := r.value(w)
	pieces := []string{text}
	// --file=~/x and VAR=~/x: the path follows "=".
	for i := strings.IndexByte(text, '='); i >= 0; {
		pieces = append(pieces, r.tilde(text[i+1:]))
		j := strings.IndexByte(text[i+1:], '=')
		if j < 0 {
			break
		}
		i += j + 1
	}
	for _, p := range pieces {
		if r.piece(p) {
			return true
		}
	}
	return false
}

// knownVars are variables whose value the check can tell.
func (r *reach) knownVar(name string) (string, bool) {
	switch name {
	case "HOME":
		return r.home, true
	case "PWD":
		return r.project, true
	case "XDG_CONFIG_HOME":
		if v := r.getenv(name); v != "" {
			return v, true
		}
		return filepath.Join(r.home, ".config"), true
	case "APPDATA", "LOCALAPPDATA", "USERPROFILE", "TGSYNC_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME":
		if v := r.getenv(name); v != "" {
			return v, true
		}
	}
	return "", false
}

// part appends the value of a word part, with wild for what is unknown.
func (r *reach) part(b *strings.Builder, part syntax.WordPart, first, quoted bool) {
	switch p := part.(type) {
	case *syntax.Lit:
		v := p.Value
		if first && !quoted {
			v = r.tilde(v)
		}
		if !quoted && strings.ContainsAny(v, "*?[") {
			v = strings.NewReplacer("*", wild, "?", wild, "[", wild).Replace(v)
		}
		b.WriteString(v)
	case *syntax.SglQuoted:
		b.WriteString(p.Value)
	case *syntax.DblQuoted:
		for i, q := range p.Parts {
			r.part(b, q, first && i == 0, true)
		}
	case *syntax.ParamExp:
		if p.Param != nil && p.Exp == nil && p.Repl == nil && p.Index == nil && p.Slice == nil && !p.Length && !p.Excl {
			if v, ok := r.knownVar(p.Param.Value); ok {
				b.WriteString(v)
				return
			}
		}
		b.WriteString(wild)
	default:
		// Command substitution, arithmetic and the like; the words inside
		// a substitution are checked on their own by the walk.
		b.WriteString(wild)
	}
}

// tilde expands a leading ~, ~/ or ~user. Another user's home is taken as
// the user's own: it is unknown, and on one-user machines it is the same.
func (r *reach) tilde(s string) string {
	if !strings.HasPrefix(s, "~") {
		return s
	}
	if i := strings.IndexByte(s, '/'); i >= 0 {
		return r.home + s[i:]
	}
	return r.home
}

func isAbs(p string) bool {
	return filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\`)
}

// piece checks one path candidate taken from a word.
func (r *reach) piece(p string) bool {
	i := strings.Index(p, wild)
	if i == 0 {
		// $X/tgsync/.env: only the rest of the word is known.
		return r.named(p)
	}
	full, partial := p, false
	if i > 0 {
		full, partial = p[:i], !strings.HasSuffix(p[:i], "/")
	}
	if isAbs(full) {
		return r.path(files.Abs(r.project, full), partial, false)
	}
	if r.path(files.Abs(r.project, full), partial, true) {
		return true
	}
	for _, b := range r.bases {
		if r.path(filepath.Join(b, full), partial, true) {
			return true
		}
	}
	return r.cdUnknown && r.named("/"+p)
}

// named reports whether a path whose start is unknown goes through a
// folder named like tgsync's.
func (r *reach) named(p string) bool {
	rest := r.norm(p)
	for _, n := range r.names {
		n = "/" + r.norm(n)
		if strings.Contains(rest, n+"/") || strings.HasSuffix(rest, n) {
			return true
		}
	}
	return false
}

// path reports whether p names tgsync's folder, something in it or a
// folder above it, as written or once symlinks are followed. partial means
// p's last element is only the start of a name (the word goes on with a
// glob or a variable). relative means the word was a relative path: the
// project's own parent folders (ls .., cd ..) are left alone unless the
// command walks folder trees.
func (r *reach) path(p string, partial, relative bool) bool {
	cands := []string{p}
	if real, ok := realPath(p); ok && !files.SamePath(real, p) {
		cands = append(cands, real)
	}
	for _, c := range cands {
		for _, d := range r.dirs {
			// Paths in the project are its own business unless tgsync's
			// folder is inside the project.
			if within(r.project, c) && !within(r.project, d) {
				continue
			}
			if within(d, c) {
				return true
			}
			if within(c, d) {
				if relative && !partial && !r.recursive && within(c, r.project) {
					continue
				}
				return true
			}
			if partial && strings.HasPrefix(r.norm(d), r.norm(c)) {
				return true
			}
		}
	}
	return false
}
