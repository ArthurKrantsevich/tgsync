// Package permissions decides tool calls and asks the user when needed.
package permissions

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/ArthurKrantsevich/tgsync/internal/files"
	"github.com/ArthurKrantsevich/tgsync/internal/i18n"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"github.com/ArthurKrantsevich/tgsync/internal/sudo"
)

// Decision is the result of the automatic rules.
type Decision int

const (
	Ask Decision = iota
	Allow
	Deny
	// Confirm needs a button in every approve mode, without "Always", and
	// saved rules do not apply to it.
	Confirm
)

func (d Decision) String() string { return [...]string{"ask", "allow", "deny", "confirm"}[d] }

// Input is one tool call with its context.
type Input struct {
	Tool       string
	Args       map[string]any
	ProjectDir string
	Protected  []string // absolute paths the agent must never touch
	Home       string   // the user's home for ~ and $HOME; empty means os.UserHomeDir
	Rules      []store.Rule
}

var (
	readOnly = map[string]bool{"Read": true, "Glob": true, "Grep": true, "LS": true, "NotebookRead": true, "TodoWrite": true, "Task": true, "Agent": true, "Skill": true,
		// tgsync's own tool; the session checks the path before sending.
		"mcp__tgsync__send_file": true}
	fileWrite = map[string]bool{"Edit": true, "MultiEdit": true, "Write": true, "NotebookEdit": true}
	chainRe   = regexp.MustCompile("[;&|`\n<>]|\\$\\(")
)

// SudoOffReason is the deny message for sudo commands; the broker replaces
// it with an approval prompt when sudo is enabled.
func SudoOffReason() string { return sudoOffReason(runtime.GOOS) }

func sudoOffReason(goos string) string {
	if goos == "windows" {
		return i18n.T("perm.agent.sudo_windows")
	}
	return i18n.T("perm.agent.sudo_off")
}

// sudoWrappedReason refuses sudo that the broker cannot hand the password to.
func sudoWrappedReason() string { return i18n.T("perm.agent.sudo_wrapped") }

// sudoRefusal is the deny message for a sudo command sudo.RewriteCommand
// refused (err) or found no direct sudo call in.
func sudoRefusal(err error) string {
	if err != nil && strings.HasPrefix(err.Error(), "tgsync:") {
		return err.Error()
	}
	return sudoWrappedReason()
}

// Evaluate applies the automatic rules of docs/en/spec.md §5.3. Commands with
// sudo are denied here; the broker turns that into an approval prompt when
// SUDO_MODE is on.
func Evaluate(in Input) (Decision, string) {
	cmd, _ := in.Args["command"].(string)
	if in.Tool == "Bash" {
		for _, p := range in.Protected {
			if p != "" && mentions(cmd, p) {
				return Deny, i18n.T("perm.agent.protected")
			}
		}
		// Parsed, not matched: the word sudo in a heredoc or a commit
		// message is not a sudo call. The broker asks about sudo commands
		// that may reach tgsync's folder in every mode (see reachesTgsync).
		if sudo.Uses(cmd) {
			return Deny, SudoOffReason()
		}
		if reachesTgsync(in) {
			return Confirm, reachReason()
		}
	}
	// Path arguments of file tools: ~ expanded, links followed.
	path := filePath(in.Args)
	if path != "" {
		path = toolPath(in, path)
		if touchesTgsync(in.Protected, path) {
			return Deny, i18n.T("perm.agent.protected")
		}
	}
	if sp, _ := in.Args["path"].(string); sp != "" && touchesTgsync(in.Protected, toolPath(in, sp)) {
		return Deny, i18n.T("perm.agent.protected")
	}
	if in.Tool == "Glob" {
		if base := globBase(in.Args); base != "" && touchesTgsync(in.Protected, toolPath(in, base)) {
			return Deny, i18n.T("perm.agent.protected")
		}
	}
	if readOnly[in.Tool] {
		return Allow, ""
	}
	if fileWrite[in.Tool] {
		// Files git, Claude Code or direnv run commands from are asked
		// about even inside the project, and saved rules do not open them.
		if path != "" && sensitiveWrite(in.ProjectDir, path) {
			return Ask, ""
		}
		if path != "" && insideProject(in.ProjectDir, path) {
			return Allow, ""
		}
		for _, r := range in.Rules {
			if r.Tool == in.Tool && path != "" && inRuleFolder(r.Pattern, path) {
				return Allow, ""
			}
		}
		return Ask, ""
	}
	for _, r := range in.Rules {
		if matchRule(r, in.Tool, cmd, in.ProjectDir) {
			return Allow, ""
		}
	}
	return Ask, ""
}

func filePath(args map[string]any) string {
	for _, k := range []string{"file_path", "notebook_path"} {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

// homeDir is the user's home for ~: in.Home, else the OS one.
func homeDir(home string) string {
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	return home
}

// expandTilde expands a leading ~ the way the agent's tools do. ~user is
// taken as the user's own home too: another user's home is unknown, and
// on a single-user machine it is the same folder.
func expandTilde(p, home string) string {
	if !strings.HasPrefix(p, "~") || home == "" {
		return p
	}
	i := strings.IndexAny(p, `/\`)
	if i < 0 {
		return home
	}
	return home + p[i:]
}

// toolPath turns a path argument of a file tool into an absolute path.
// files.Abs also folds the other spellings Windows opens as the same file
// (no drive, Git Bash /c/x, trailing dots, :streams).
func toolPath(in Input, p string) string {
	return files.Abs(in.ProjectDir, expandTilde(p, homeDir(in.Home)))
}

// globBase returns the folder a Glob pattern starts from when the pattern
// itself names one (an absolute, ~ or ../ pattern, or a folder before the
// first wildcard), or "" when it starts from the path argument.
func globBase(args map[string]any) string {
	pat, _ := args["pattern"].(string)
	i := strings.IndexAny(pat, "*?[{")
	if i < 0 {
		i = len(pat)
	}
	prefix := pat[:i]
	j := strings.LastIndexAny(prefix, `/\`)
	if j < 0 {
		return ""
	}
	prefix = prefix[:j+1]
	if dir, _ := args["path"].(string); dir != "" && !strings.HasPrefix(prefix, "~") && !filepath.IsAbs(prefix) && !strings.HasPrefix(prefix, "/") {
		return filepath.Join(dir, prefix)
	}
	return prefix
}

// tgsyncHomes returns the folders holding tgsync's files, without those
// inside another one: ~/.config/tgsync for .env and data/tgsync.db.
func tgsyncHomes(protected []string) []string {
	var dirs, top []string
	for _, p := range protected {
		if p != "" {
			dirs = append(dirs, filepath.Dir(p))
		}
	}
	for _, d := range dirs {
		inner := false
		for _, o := range dirs {
			if !files.SamePath(o, d) && within(o, d) {
				inner = true
			}
		}
		if !inner {
			top = append(top, d)
		}
	}
	return top
}

// touchesTgsync reports whether a file tool pointed at abs reaches tgsync's
// files: abs is one of them, or a folder holding one or tgsync's folder,
// by name or once symlinks are followed.
func touchesTgsync(protected []string, abs string) bool {
	cands := []string{abs}
	if r, ok := realPath(abs); ok && !files.SamePath(r, abs) {
		cands = append(cands, r)
	}
	homes := tgsyncHomes(protected)
	for _, h := range homes {
		if r, ok := realPath(h); ok && !files.SamePath(r, h) {
			homes = append(homes, r)
		}
	}
	for _, c := range cands {
		for _, p := range protected {
			if p == "" {
				continue
			}
			if within(c, p) || files.SameFile(c, p) {
				return true
			}
			if rp, ok := realPath(p); ok && within(c, rp) {
				return true
			}
		}
		for _, h := range homes {
			if within(c, h) {
				return true
			}
		}
	}
	return false
}

func within(dir, path string) bool {
	_, ok := files.Within(dir, path)
	return ok
}

// realPath returns p with every symlink resolved, the way a write to p
// lands. For a file that does not exist yet its nearest existing parent is
// resolved. ok is false when the target cannot be told, for example for a
// dangling link, whose write would create the file it points to.
func realPath(p string) (string, bool) {
	rest, cur := "", p
	for {
		if _, err := os.Lstat(cur); err == nil {
			r, err := filepath.EvalSymlinks(cur)
			if err != nil {
				return "", false
			}
			return filepath.Join(r, rest), true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return p, true
		}
		rest = filepath.Join(filepath.Base(cur), rest)
		cur = parent
	}
}

// insideProject reports whether a write to path lands inside dir once
// symlinks are followed: a link in the project may point anywhere.
func insideProject(dir, path string) bool {
	real, ok := realPath(path)
	if !ok {
		return false
	}
	root, ok := realPath(dir)
	return ok && within(root, real)
}

// inRuleFolder reports whether a file rule saved for folder covers path:
// the file must sit right in that folder, by name and once symlinks are
// followed. Rules saved before rules had a folder (empty) cover nothing.
func inRuleFolder(folder, path string) bool {
	if folder == "" || !files.SamePath(folder, filepath.Dir(path)) {
		return false
	}
	real, ok := realPath(path)
	if !ok {
		return false
	}
	realFolder, ok := realPath(folder)
	return ok && files.SamePath(realFolder, filepath.Dir(real))
}

// sensitiveWrite reports whether path, by name or by where its symlinks
// lead, is a project file that makes git, Claude Code or the shell run
// commands: git config and hooks (tgsync itself runs git add for turn
// snapshots, which runs filters), .gitattributes, Claude Code settings and
// hooks, MCP servers, direnv and VS Code tasks.
func sensitiveWrite(dir, path string) bool {
	if rel, ok := files.Within(dir, path); ok && sensitiveRel(rel) {
		return true
	}
	real, ok := realPath(path)
	if !ok {
		return false
	}
	root, _ := realPath(dir)
	rel, ok := files.Within(root, real)
	return ok && sensitiveRel(rel)
}

func sensitiveRel(rel string) bool {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	is := func(a, b string) bool {
		if files.FoldCase {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	for _, part := range parts {
		if is(part, ".git") || is(part, ".claude") {
			return true
		}
	}
	last := parts[len(parts)-1]
	for _, name := range []string{".gitattributes", ".gitmodules", ".envrc"} {
		if is(last, name) {
			return true
		}
	}
	switch len(parts) {
	case 1:
		return is(last, ".mcp.json")
	case 2:
		return is(parts[0], ".vscode") && is(last, "tasks.json")
	}
	return false
}

// mentions reports whether a shell command names protected path p, in any
// spelling the agent's shell accepts.
func mentions(cmd, p string) bool {
	return mentionsOS(cmd, p, runtime.GOOS, files.FoldCase)
}

func mentionsOS(cmd, p, goos string, fold bool) bool {
	// On Windows the shell takes \, / and doubled \\ alike, so compare
	// both sides with single forward slashes.
	norm := func(s string) string {
		if goos == "windows" {
			s = strings.ReplaceAll(s, `\`, "/")
			for strings.Contains(s, "//") {
				s = strings.ReplaceAll(s, "//", "/")
			}
		}
		if fold {
			s = strings.ToLower(s)
		}
		return s
	}
	cmd = norm(cmd)
	for _, f := range pathForms(p, goos) {
		if strings.Contains(cmd, norm(f)) {
			return true
		}
	}
	return false
}

// pathForms lists how a shell may spell p. On Windows claude's Bash tool is
// Git Bash, which also takes C:/x and /c/x for C:\x.
func pathForms(p, goos string) []string {
	forms := []string{p}
	if goos != "windows" {
		return forms
	}
	slash := strings.ReplaceAll(p, `\`, "/")
	forms = append(forms, slash)
	if len(p) >= 2 && p[1] == ':' {
		forms = append(forms, "/"+strings.ToLower(p[:1])+slash[2:])
	}
	return forms
}

func matchRule(r store.Rule, tool, cmd, dir string) bool {
	if r.Tool != tool {
		return false
	}
	if tool != "Bash" {
		return true
	}
	if chainRe.MatchString(cmd) {
		return false
	}
	c := strings.TrimSpace(cmd)
	// Rules saved before "Always" refused these (a bare "python3" or "rm")
	// no longer cover them.
	if onceOnly(strings.Fields(c), dir) {
		return false
	}
	return c == r.Pattern || strings.HasPrefix(c, r.Pattern+" ")
}

// onceOnlyCmds run arbitrary code or destroy data whatever their first
// argument is, so a saved prefix would cover far more than was approved.
var onceOnlyCmds = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true, "csh": true, "tcsh": true, "busybox": true,
	"node": true, "nodejs": true, "deno": true, "bun": true, "ruby": true, "perl": true, "php": true, "lua": true,
	"awk": true, "gawk": true, "pwsh": true, "powershell": true, "cmd": true, "osascript": true,
	"rm": true, "rmdir": true, "dd": true, "shred": true, "chmod": true, "chown": true, "chgrp": true, "find": true,
	"eval": true, "exec": true, "source": true, ".": true, "env": true, "xargs": true, "nohup": true, "timeout": true,
	"nice": true, "setsid": true, "command": true, "builtin": true, "ssh": true, "sudo": true, "doas": true, "su": true,
}

// unquote drops the shell quoting of a word: \rm, "rm" and 'rm' all run rm.
func unquote(w string) string {
	w = strings.ReplaceAll(w, "$'", "'")
	return strings.NewReplacer(`\`, "", `"`, "", "'", "").Replace(w)
}

// onceOnly reports whether a command in project dir may only be approved
// once: shells, interpreters, destructive commands, wrappers that run
// another command, VAR=value prefixes, a command name built from
// variables, git with global options (git -c runs config) and git config,
// output flags (git log --output=.git/hooks/x) and arguments that point
// into .git, .claude or other files git and Claude Code run commands from.
func onceOnly(f []string, dir string) bool {
	if len(f) == 0 {
		return true
	}
	first := unquote(f[0])
	if strings.ContainsAny(first, "=$`") {
		return true
	}
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(first)), ".exe")
	switch {
	case onceOnlyCmds[name], strings.HasPrefix(name, "python"):
		return true
	case name == "git" && len(f) > 1:
		if a := unquote(f[1]); strings.HasPrefix(a, "-") || a == "config" {
			return true
		}
	}
	for _, a := range f[1:] {
		a = unquote(a)
		if a == "-o" || (strings.HasPrefix(a, "-o") && !strings.HasPrefix(a, "--")) || strings.HasPrefix(a, "--output") {
			return true
		}
		if sensitiveWord(dir, a) {
			return true
		}
	}
	return false
}

// sensitiveWord reports whether a command argument, or the value after its
// "=", names a sensitive project file (see sensitiveRel) by its text or
// once symlinks in the project are followed.
func sensitiveWord(dir, a string) bool {
	vals := []string{a}
	if i := strings.LastIndexByte(a, '='); i >= 0 {
		vals = append(vals, a[i+1:])
	}
	for _, v := range vals {
		if v == "" || strings.HasPrefix(v, "-") {
			continue
		}
		if sensitiveRel(filepath.Clean(filepath.FromSlash(strings.TrimPrefix(v, "~")))) {
			return true
		}
		if dir != "" && sensitiveWrite(dir, files.Abs(dir, v)) {
			return true
		}
	}
	return false
}

// AlwaysRule builds the rule saved by the "Always" button. For Bash it
// keeps the command and its first argument, for example "go test"; for file
// tools, the folder of the file.
func AlwaysRule(tool string, args map[string]any, dir string) (store.Rule, bool) {
	if fileWrite[tool] {
		p := filePath(args)
		if p == "" {
			return store.Rule{}, false
		}
		p = files.Abs(dir, p)
		if sensitiveWrite(dir, p) {
			return store.Rule{}, false
		}
		return store.Rule{Tool: tool, Pattern: filepath.Dir(p)}, true
	}
	if tool != "Bash" {
		return store.Rule{Tool: tool}, true
	}
	cmd, _ := args["command"].(string)
	cmd = strings.TrimSpace(cmd)
	if cmd == "" || chainRe.MatchString(cmd) {
		return store.Rule{}, false
	}
	f := strings.Fields(cmd)
	if onceOnly(f, dir) {
		return store.Rule{}, false
	}
	pat := f[0]
	if len(f) > 1 {
		pat += " " + f[1]
	}
	return store.Rule{Tool: "Bash", Pattern: pat}, true
}
