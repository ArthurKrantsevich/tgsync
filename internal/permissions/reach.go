package permissions

import (
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
// the literal path check misses: ~ and $HOME, relative paths, globs,
// variables, nested sh -c scripts and heredocs.
//
// This is defense in depth, not a boundary: the agent runs as the same OS
// user, so in the «Всё сам» and «Всё, кроме sudo» modes, or through any
// program it writes and runs, it can still read these files. The check only
// makes the obvious spellings need a tap.
type reach struct {
	project string
	home    string   // the user's home for ~ and $HOME
	dirs    []string // folders holding tgsync's files
	names   []string // base names of the top folders, e.g. tgsync
	tails   []string // folder/name of each protected file, e.g. tgsync/.env
	fold    bool
}

// reachesTgsync reports whether a Bash call may reach tgsync's folder.
func reachesTgsync(in Input) bool {
	cmd, _ := in.Args["command"].(string)
	return in.Tool == "Bash" && newReach(in.ProjectDir, in.Home, in.Protected).command(cmd, 0)
}

func newReach(project, home string, protected []string) *reach {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	r := &reach{project: filepath.Clean(project), home: home, fold: files.FoldCase}
	for _, p := range protected {
		if p == "" {
			continue
		}
		d := filepath.Dir(p)
		r.dirs = append(r.dirs, d)
		r.tails = append(r.tails, filepath.Base(d)+"/"+filepath.Base(p))
	}
	for _, d := range r.dirs {
		top := true
		for _, o := range r.dirs {
			if o != d && within(o, d) {
				top = false
			}
		}
		if top {
			r.names = append(r.names, filepath.Base(d))
		}
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
		return false // the text check above is all that is left
	}
	found := false
	syntax.Walk(f, func(n syntax.Node) bool {
		if w, ok := n.(*syntax.Word); ok && !found && r.word(w, depth) {
			found = true
		}
		return !found
	})
	return found
}

// wild marks the part of a word whose value is not known before it runs.
const wild = "\x00"

// word checks one shell word: a path the command may open.
func (r *reach) word(w *syntax.Word, depth int) bool {
	var b strings.Builder
	for i, part := range w.Parts {
		r.part(&b, part, i == 0, false)
	}
	text := b.String()
	// A word with spaces may be a script (sh -c, heredoc, ssh …).
	if depth < 3 && strings.ContainsAny(text, " \t\n") && !strings.Contains(text, wild) && r.command(text, depth+1) {
		return true
	}
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
		if p.Param != nil && p.Param.Value == "HOME" && p.Exp == nil && p.Repl == nil && p.Index == nil &&
			p.Slice == nil && !p.Length && !p.Excl && !p.Short {
			b.WriteString(r.home)
			return
		}
		b.WriteString(wild)
	default:
		// Command substitution, arithmetic and the like; the words inside
		// a substitution are checked on their own by the walk.
		b.WriteString(wild)
	}
}

// tilde expands a leading ~ or ~/; ~user is unknown.
func (r *reach) tilde(s string) string {
	switch {
	case s == "~":
		return r.home
	case strings.HasPrefix(s, "~/"):
		return r.home + s[1:]
	case strings.HasPrefix(s, "~"):
		return wild + s[1:]
	}
	return s
}

// piece checks one path candidate taken from a word.
func (r *reach) piece(p string) bool {
	i := strings.Index(p, wild)
	if i < 0 {
		return r.path(files.Abs(r.project, p), false)
	}
	prefix := p[:i]
	if prefix == "" {
		// $X/tgsync/.env: only the rest of the word is known.
		rest := r.norm(p)
		for _, n := range r.names {
			n = "/" + r.norm(n)
			if strings.Contains(rest, n+"/") || strings.HasSuffix(rest, n) {
				return true
			}
		}
		return false
	}
	return r.path(files.Abs(r.project, prefix), !strings.HasSuffix(prefix, "/"))
}

// path reports whether p names tgsync's folder, something in it or a
// folder above it. partial means p's last element is only the start of a
// name (the word goes on with a glob or a variable).
func (r *reach) path(p string, partial bool) bool {
	for _, d := range r.dirs {
		// Paths in the project are its own business unless tgsync's folder
		// is inside the project.
		if within(r.project, p) && !within(r.project, d) {
			continue
		}
		if within(d, p) || within(p, d) {
			return true
		}
		if partial && strings.HasPrefix(r.norm(d), r.norm(p)) {
			return true
		}
	}
	return false
}
