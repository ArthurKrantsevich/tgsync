package permissions

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ArthurKrantsevich/tgsync/internal/files"
	"github.com/ArthurKrantsevich/tgsync/internal/store"
	"mvdan.cc/sh/v3/syntax"
)

func TestEvaluate(t *testing.T) {
	protected := []string{osPath("/opt/tgsync/.env")}
	rules := []store.Rule{{Tool: "Bash", Pattern: "go test"}, {Tool: "WebFetch"}}
	cases := []struct {
		name string
		tool string
		args map[string]any
		want Decision
	}{
		{"read anywhere", "Read", map[string]any{"file_path": osPath("/etc/hosts")}, Allow},
		{"grep", "Grep", map[string]any{"pattern": "x"}, Allow},
		{"subagent", "Agent", nil, Allow},
		{"read protected", "Read", map[string]any{"file_path": osPath("/opt/tgsync/.env")}, Deny},
		{"edit in project", "Edit", map[string]any{"file_path": osPath("/w/demo/a.go")}, Allow},
		{"edit relative in project", "Write", map[string]any{"file_path": "src/a.go"}, Allow},
		{"edit escaping project", "Edit", map[string]any{"file_path": osPath("/w/demo/../other/a.go")}, Ask},
		{"edit outside", "Write", map[string]any{"file_path": osPath("/etc/hosts")}, Ask},
		{"bash unknown", "Bash", map[string]any{"command": "rm -rf build"}, Ask},
		{"bash rule", "Bash", map[string]any{"command": "go test ./..."}, Allow},
		{"bash rule exact", "Bash", map[string]any{"command": "go test"}, Allow},
		{"bash rule prefix trick", "Bash", map[string]any{"command": "go testx"}, Ask},
		{"bash rule chained", "Bash", map[string]any{"command": "go test ./... && rm -rf build"}, Ask},
		{"bash rule subshell", "Bash", map[string]any{"command": "go test $(rm -rf build)"}, Ask},
		{"sudo", "Bash", map[string]any{"command": "sudo apt install x"}, Deny},
		{"sudo after pipe", "Bash", map[string]any{"command": "echo y | sudo tee /etc/x"}, Deny},
		{"sudo-like word", "Bash", map[string]any{"command": "echo pseudo"}, Ask},
		{"sudo in heredoc", "Bash", map[string]any{"command": "cat <<'EOF' > x\nrun sudo -n here\nEOF"}, Ask},
		{"sudo in quotes", "Bash", map[string]any{"command": `git commit -m "fix sudo"`}, Ask},
		{"bash touches protected", "Bash", map[string]any{"command": "cat " + osPath("/opt/tgsync/.env")}, Deny},
		{"tool rule", "WebFetch", map[string]any{"url": "https://x"}, Allow},
		{"mcp tool", "mcp__db__query", nil, Ask},
		{"send_file tool", "mcp__tgsync__send_file", map[string]any{"path": "docs/a.md"}, Allow},
	}
	for _, c := range cases {
		got, reason := Evaluate(Input{Tool: c.tool, Args: c.args, ProjectDir: osPath("/w/demo"), Protected: protected, Rules: rules})
		if got != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, got, reason, c.want)
		}
		if got == Deny && reason == "" {
			t.Errorf("%s: deny without reason", c.name)
		}
	}
}

func TestAlwaysRule(t *testing.T) {
	cases := []struct {
		cmd  string
		want string
		ok   bool
	}{
		{"go test ./...", "go test", true},
		{"npm run build", "npm run", true},
		{"ls -la", "ls -la", true},
		{"cat ./file", "cat ./file", true},
		{"make", "make", true},
		{"git status", "git status", true},
		{"go test && rm -rf /", "", false},
		{"  ", "", false},
		// Shells, interpreters and destructive commands: once only.
		{"rm build.log", "", false},
		{"/bin/rm -rf build", "", false},
		{"python3 x.py", "", false},
		{"/usr/bin/python3.12 -m http.server", "", false},
		{"bash script.sh", "", false},
		{"sh -c 'make'", "", false},
		{"node x.js", "", false},
		{"perl -e 1", "", false},
		{"ruby x.rb", "", false},
		{"php x.php", "", false},
		{"dd if=/dev/zero of=x", "", false},
		{"chmod +x x", "", false},
		{"chown u x", "", false},
		{"eval make", "", false},
		{"exec make", "", false},
		{"env FOO=1 make", "", false},
		{"xargs rm", "", false},
		{"FOO=1 make", "", false},
		{"git -c core.fsmonitor=x status", "", false},
		{"git -C ../other status", "", false},
	}
	for _, c := range cases {
		r, ok := AlwaysRule("Bash", map[string]any{"command": c.cmd}, osPath("/w/demo"))
		if ok != c.ok || r.Pattern != c.want || (ok && r.Tool != "Bash") {
			t.Errorf("AlwaysRule(%q) = %+v, %v; want %q, %v", c.cmd, r, ok, c.want, c.ok)
		}
	}
	if r, ok := AlwaysRule("WebFetch", nil, osPath("/w/demo")); !ok || r != (store.Rule{Tool: "WebFetch"}) {
		t.Errorf("tool rule: %+v %v", r, ok)
	}
}

func TestAlwaysRuleKeepsFileFolder(t *testing.T) {
	r, ok := AlwaysRule("Write", map[string]any{"file_path": osPath("/etc/app/x.conf")}, osPath("/w/demo"))
	if !ok || r != (store.Rule{Tool: "Write", Pattern: osPath("/etc/app")}) {
		t.Fatalf("rule: %+v %v", r, ok)
	}
}

func TestOldBroadRulesDoNotCoverRiskyCommands(t *testing.T) {
	rules := []store.Rule{{Tool: "Bash", Pattern: "python3"}, {Tool: "Bash", Pattern: "git"}, {Tool: "Bash", Pattern: "rm"}}
	for _, cmd := range []string{"python3 evil.py", "git -c core.fsmonitor=x status", "rm -rf build"} {
		if got, _ := Evaluate(Input{Tool: "Bash", Args: map[string]any{"command": cmd}, ProjectDir: osPath("/w/demo"), Rules: rules}); got != Ask {
			t.Errorf("%q: got %v, want ask", cmd, got)
		}
	}
	if got, _ := Evaluate(Input{Tool: "Bash", Args: map[string]any{"command": "git status"}, ProjectDir: osPath("/w/demo"), Rules: rules}); got != Allow {
		t.Errorf("git status under an old git rule: got %v", got)
	}
}

func TestEvaluateSearchPathsAndSavedWriteRules(t *testing.T) {
	protected := []string{osPath("/opt/tgsync/.env")}
	cases := []struct {
		name  string
		tool  string
		args  map[string]any
		rules []store.Rule
		want  Decision
	}{
		{"grep over bot dir", "Grep", map[string]any{"pattern": ".", "path": osPath("/opt/tgsync")}, nil, Deny},
		{"glob over bot dir", "Glob", map[string]any{"pattern": "*", "path": osPath("/opt")}, nil, Deny},
		{"grep protected file", "Grep", map[string]any{"pattern": ".", "path": osPath("/opt/tgsync/.env")}, nil, Deny},
		{"grep elsewhere", "Grep", map[string]any{"pattern": ".", "path": osPath("/w/demo")}, nil, Allow},
		{"saved edit rule", "Edit", map[string]any{"file_path": osPath("/etc/hosts")}, []store.Rule{{Tool: "Edit", Pattern: osPath("/etc")}}, Allow},
		{"saved rule other tool", "Edit", map[string]any{"file_path": osPath("/etc/hosts")}, []store.Rule{{Tool: "Write", Pattern: osPath("/etc")}}, Ask},
		{"saved rule other folder", "Edit", map[string]any{"file_path": osPath("/etc/hosts")}, []store.Rule{{Tool: "Edit", Pattern: osPath("/srv")}}, Ask},
		{"saved rule subfolder", "Edit", map[string]any{"file_path": osPath("/etc/ssh/sshd_config")}, []store.Rule{{Tool: "Edit", Pattern: osPath("/etc")}}, Ask},
		{"old rule without folder", "Edit", map[string]any{"file_path": osPath("/etc/hosts")}, []store.Rule{{Tool: "Edit"}}, Ask},
	}
	for _, c := range cases {
		got, reason := Evaluate(Input{Tool: c.tool, Args: c.args, ProjectDir: osPath("/w/demo"), Protected: protected, Rules: c.rules})
		if got != c.want {
			t.Errorf("%s: got %v (%s), want %v", c.name, got, reason, c.want)
		}
	}
}

func TestPathFormsWindows(t *testing.T) {
	got := pathForms(`C:\Users\u\AppData\Roaming\tgsync\.env`, "windows")
	want := []string{
		`C:\Users\u\AppData\Roaming\tgsync\.env`,
		`C:/Users/u/AppData/Roaming/tgsync/.env`,
		`/c/Users/u/AppData/Roaming/tgsync/.env`,
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("pathForms = %q", got)
	}
	if got := pathForms("/home/u/.config/tgsync/.env", "linux"); len(got) != 1 {
		t.Fatalf("linux keeps one form, got %q", got)
	}
}

func TestMentionsProtected(t *testing.T) {
	old := files.FoldCase
	defer func() { files.FoldCase = old }()
	files.FoldCase = true
	if !mentions("cat /OPT/TGSYNC/.ENV", "/opt/tgsync/.env") {
		t.Fatal("case-insensitive systems must match a differently-cased path")
	}
	files.FoldCase = false
	if mentions("cat /OPT/TGSYNC/.ENV", "/opt/tgsync/.env") {
		t.Fatal("case-sensitive systems must not")
	}
}

func TestEvaluateSensitiveWritesInProject(t *testing.T) {
	dir := osPath("/w/demo")
	for _, p := range []string{
		".git/config", ".git/hooks/pre-commit", "sub/.git/config", ".gitattributes", "sub/.gitattributes",
		".gitmodules", ".claude/settings.json", ".claude/settings.local.json", "sub/.claude/settings.json",
		".mcp.json", ".envrc", "sub/.envrc", ".vscode/tasks.json",
	} {
		for _, tool := range []string{"Write", "Edit"} {
			got, _ := Evaluate(Input{Tool: tool, Args: map[string]any{"file_path": p}, ProjectDir: dir})
			if got != Ask {
				t.Errorf("%s %s: got %v, want ask", tool, p, got)
			}
			// A saved folder rule must not open these files either.
			rules := []store.Rule{{Tool: tool, Pattern: filepath.Dir(files.Abs(dir, p))}}
			if got, _ := Evaluate(Input{Tool: tool, Args: map[string]any{"file_path": p}, ProjectDir: dir, Rules: rules}); got != Ask {
				t.Errorf("%s %s with a folder rule: got %v, want ask", tool, p, got)
			}
			if _, ok := AlwaysRule(tool, map[string]any{"file_path": p}, dir); ok {
				t.Errorf("%s %s: «Всегда» must not be offered", tool, p)
			}
		}
	}
	for _, p := range []string{"src/a.go", "gitconfig.md", "docs/.claude.md", "claude/x.go", "sub/.mcp.json"} {
		if got, _ := Evaluate(Input{Tool: "Write", Args: map[string]any{"file_path": p}, ProjectDir: dir}); got != Allow {
			t.Errorf("Write %s: got %v, want allow", p, got)
		}
	}
}

func TestEvaluateWriteThroughSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{proj, outside, filepath.Join(proj, ".git", "hooks")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(outside, "bashrc"), nil, 0o644))
	must(os.Symlink(filepath.Join(outside, "bashrc"), filepath.Join(proj, "link")))
	must(os.Symlink(outside, filepath.Join(proj, "dirlink")))
	must(os.Symlink(filepath.Join(outside, "missing"), filepath.Join(proj, "dangling")))
	must(os.Symlink(filepath.Join(proj, ".git", "hooks"), filepath.Join(proj, "hooks")))
	cases := map[string]Decision{
		"link":             Ask,   // existing file outside
		"dirlink/new.txt":  Ask,   // new file in a linked folder outside
		"dangling":         Ask,   // writing creates the target outside
		"hooks/pre-commit": Ask,   // resolves into .git
		"plain.txt":        Allow, // ordinary new file
	}
	for p, want := range cases {
		if got, _ := Evaluate(Input{Tool: "Write", Args: map[string]any{"file_path": p}, ProjectDir: proj}); got != want {
			t.Errorf("Write %s: got %v, want %v", p, got, want)
		}
	}
	// A project opened through a symlinked path is still the project.
	must(os.Symlink(proj, filepath.Join(root, "projlink")))
	if got, _ := Evaluate(Input{Tool: "Write", Args: map[string]any{"file_path": "plain.txt"}, ProjectDir: filepath.Join(root, "projlink")}); got != Allow {
		t.Errorf("project via symlink: got %v", got)
	}
}

// tgsyncInput is a Bash call in a project next to tgsync's own folder.
func tgsyncInput(cmd string) Input {
	return Input{Tool: "Bash", Args: map[string]any{"command": cmd}, ProjectDir: osPath("/home/u/proj"), Home: osPath("/home/u"),
		Protected: []string{osPath("/home/u/.config/tgsync/.env"), osPath("/home/u/.config/tgsync/data/tgsync.db")},
		Rules:     []store.Rule{{Tool: "Bash", Pattern: "cat"}, {Tool: "Bash", Pattern: "ls"}}}
}

func TestEvaluateBashReachingTgsyncFolder(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell paths below are Unix ones")
	}
	for _, cmd := range []string{
		"cat ~/.config/tgsync/.env",
		"cat $HOME/.config/tgsync/.env",
		"cat ${HOME}/.config/tgsync/.env",
		`cat "$HOME/.config/tgsync/.env"`,
		"cat ../.config/tgsync/.env",
		"cat ~/.config/tgs*/.env",
		"cat ~/.config/tgsync/*",
		"cat /home/u/.config/tgsync/dat?/tgsync.db",
		"cat /home/u/.co*/tgsync/.env",
		"ls ~/.config/tgsync",
		"ls ~/.config",
		"cd ~/.config && cat tgsync/.env",
		"cat $XDG_CONFIG_HOME/tgsync/.env",
		"ls ${CFG}/tgsync/",
		`sh -c "cat ~/.config/tgsync/.env"`,
		"cat --file=~/.config/tgsync/.env",
		"F=~/.config/tgsync; cat $F/x",
		"python3 <<'EOF'\nprint(open(os.path.expanduser('~/.config/tgsync/.env')).read())\nEOF",
		"cat 'unterminated ~/.config/tgsync/.env",
	} {
		got, reason := Evaluate(tgsyncInput(cmd))
		if got != Confirm || reason == "" {
			t.Errorf("%q: got %v (%s), want confirm", cmd, got, reason)
		}
	}
	for cmd, want := range map[string]Decision{
		"cat README.md":                   Allow,
		"cat src/$NAME":                   Allow,
		"cat ~/notes.txt":                 Allow,
		"ls -la":                          Allow,
		"go build ./cmd/tgsync":           Ask,
		"echo $PATH":                      Ask,
		"ls *.go":                         Allow,
		`git commit -m "tgsync: fix"`:     Ask,
		"cat /home/u/.config/tgsync/.env": Deny,
	} {
		if got, reason := Evaluate(tgsyncInput(cmd)); got != want {
			t.Errorf("%q: got %v (%s), want %v", cmd, got, reason, want)
		}
	}
}

func TestEvaluateBashReachingTgsyncOtherSpellings(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell paths below are Unix ones")
	}
	t.Setenv("XDG_CONFIG_HOME", "/home/u/.config")
	t.Setenv("APPDATA", "/home/u/.config")
	for _, cmd := range []string{
		"ls $HOME/.config",
		`ls "$HOME/.config"`,
		`sh -c "cat $HOME/.config/tg*/.env"`,
		`bash -lc 'cat ~/.config/tg*/.env'`,
		"sh <<EOF\ncat ~/.config/tg*/.env\nEOF",
		"bash <<< 'cat ~/.config/tg*/.env'",
		`eval "cat ~/.config/tg*/.env"`,
		`python3 -c "print(open('/home/u/.config/tg' + 'sync/.env').read())"`,
		"ls ~u/.config",
		"ls ~root/.config",
		"cd ~/.config && cat tg*/.env",
		"cd ~/.config; ls tgsync",
		"pushd ~/.config && ls tgsync",
		"cd && ls .config/tgsync",
		"cd ~ && ls .config",
		"cd $HOME; ls .config",
		"ls $XDG_CONFIG_HOME",
		"ls $APPDATA",
		"cat $PWD/../.config/tg*/.env",
		"grep -r token ..",
		"rg token ..",
		"cd .. && grep -rn token .",
		"ls /",
		"du -sh ~/*",
	} {
		if got, reason := Evaluate(tgsyncInput(cmd)); got != Confirm {
			t.Errorf("%q: got %v (%s), want confirm", cmd, got, reason)
		}
	}
	for cmd, want := range map[string]Decision{
		`git commit -m "fix: a / b"`:                         Ask,
		`git commit -m "expand ~ in paths"`:                  Ask,
		"cat > notes.md <<'EOF'\nsplit a / b and ~ too\nEOF": Ask,
		"go build -o $PWD/bin/tgsync ./cmd/tgsync":           Ask,
		"ls ..":                      Allow,
		"cd .. && ls":                Ask,
		"ls ../other":                Allow,
		"cat ~/notes.txt":            Allow,
		`echo "a b" | sh -c 'wc -l'`: Ask,
	} {
		if got, reason := Evaluate(tgsyncInput(cmd)); got != want {
			t.Errorf("%q: got %v (%s), want %v", cmd, got, reason, want)
		}
	}
}

func TestEvaluateBashReachingTgsyncThroughProjectLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need extra rights on Windows")
	}
	home, proj, protected := tgsyncTree(t)
	for _, cmd := range []string{"cat cfg/.env", "ls cfgparent", "grep -r x cfg", "cat ./cfg/data/*"} {
		in := Input{Tool: "Bash", Args: map[string]any{"command": cmd}, ProjectDir: proj, Home: home, Protected: protected}
		if got, reason := Evaluate(in); got != Confirm {
			t.Errorf("%q: got %v (%s), want confirm", cmd, got, reason)
		}
	}
}

func TestParseFailureConfirms(t *testing.T) {
	if !parseFailureConfirms(syntax.LangError{}) {
		t.Error("a construct the parser does not support must need a button")
	}
	_, err := syntax.NewParser().Parse(strings.NewReader("cat )"), "")
	if err == nil || parseFailureConfirms(err) {
		t.Errorf("a syntax error bash rejects too needs no button: %v", err)
	}
}

func TestSudoOffReasonWindows(t *testing.T) {
	if !strings.Contains(sudoOffReason("windows"), "Windows") {
		t.Fatal("the Windows deny reason must say sudo is unavailable on Windows")
	}
}

func TestMentionsWindowsSpellings(t *testing.T) {
	p := `C:\Users\u\AppData\Roaming\tgsync\.env`
	for _, cmd := range []string{
		`cat C:\\Users\\u\\AppData\\Roaming\\tgsync\\.env`,
		`cat C:\Users/u/AppData/Roaming/tgsync/.env`,
		`cat /c/users/u/appdata/roaming/tgsync/.ENV`,
	} {
		if !mentionsOS(cmd, p, "windows", true) {
			t.Errorf("%s must be seen as touching %s", cmd, p)
		}
	}
	if mentionsOS("cat /c/Users/u/notes.txt", p, "windows", true) {
		t.Error("another file must not match")
	}
}

// tgsyncTree makes home/.config/tgsync with its files and a project next to
// it, with links from the project to tgsync's folder and its parent.
func tgsyncTree(t *testing.T) (home, proj string, protected []string) {
	t.Helper()
	root := t.TempDir()
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	home = filepath.Join(root, "home")
	tg := filepath.Join(home, ".config", "tgsync")
	proj = filepath.Join(home, "proj")
	for _, d := range []string{filepath.Join(tg, "data"), proj} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	protected = []string{filepath.Join(tg, ".env"), filepath.Join(tg, "data", "tgsync.db")}
	for _, p := range protected {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if runtime.GOOS != "windows" {
		for link, to := range map[string]string{"cfg": tg, "cfgparent": filepath.Join(home, ".config"), "dot": filepath.Join(tg, ".env")} {
			if err := os.Symlink(to, filepath.Join(proj, link)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return home, proj, protected
}

func TestEvaluateToolPathsFollowLinksAndTilde(t *testing.T) {
	home, proj, protected := tgsyncTree(t)
	eval := func(tool string, args map[string]any) Decision {
		d, _ := Evaluate(Input{Tool: tool, Args: args, ProjectDir: proj, Home: home, Protected: protected})
		return d
	}
	type call struct {
		tool string
		args map[string]any
	}
	deny := []call{
		{"Grep", map[string]any{"pattern": ".", "path": "~/.config"}},
		{"Grep", map[string]any{"pattern": ".", "path": "~"}},
		{"Glob", map[string]any{"pattern": "*", "path": "~/.config/tgsync"}},
		{"LS", map[string]any{"path": "~/.config/tgsync"}},
		{"Read", map[string]any{"file_path": "~/.config/tgsync/.env"}},
		{"Read", map[string]any{"file_path": "~/.config/tgsync"}},
		{"Write", map[string]any{"file_path": "~/.config/tgsync/.env"}},
		{"Glob", map[string]any{"pattern": filepath.Join(home, ".config", "tgsync", "*")}},
		{"Glob", map[string]any{"pattern": "~/.config/tgs*/.env"}},
		{"Glob", map[string]any{"pattern": "../.config/**"}},
	}
	if runtime.GOOS != "windows" {
		deny = append(deny,
			call{"Grep", map[string]any{"pattern": ".", "path": "cfg"}},
			call{"Grep", map[string]any{"pattern": ".", "path": filepath.Join(proj, "cfg")}},
			call{"Glob", map[string]any{"pattern": "*", "path": "cfgparent"}},
			call{"LS", map[string]any{"path": "cfg"}},
			call{"Read", map[string]any{"file_path": "cfg/.env"}},
			call{"Read", map[string]any{"file_path": "dot"}},
			call{"Edit", map[string]any{"file_path": "cfg/.env"}},
			call{"MultiEdit", map[string]any{"file_path": "dot"}},
			call{"NotebookEdit", map[string]any{"notebook_path": "cfg/data/tgsync.db"}},
			call{"Glob", map[string]any{"pattern": "cfg/*"}},
		)
	}
	for _, c := range deny {
		if got := eval(c.tool, c.args); got != Deny {
			t.Errorf("%s %v: got %v, want deny", c.tool, c.args, got)
		}
	}
	for _, c := range []call{
		{"Grep", map[string]any{"pattern": ".", "path": "src"}},
		{"Read", map[string]any{"file_path": "~/notes.txt"}},
		{"Glob", map[string]any{"pattern": "**/*.go"}},
		{"LS", map[string]any{"path": "~/proj"}},
	} {
		if got := eval(c.tool, c.args); got != Allow {
			t.Errorf("%s %v: got %v, want allow", c.tool, c.args, got)
		}
	}
}

// Saved rules must not cover commands that write where git or Claude Code
// run commands from, or change git's configuration.
func TestSavedRulesSkipSensitiveCommands(t *testing.T) {
	proj := t.TempDir()
	if r, err := filepath.EvalSymlinks(proj); err == nil {
		proj = r
	}
	if err := os.MkdirAll(filepath.Join(proj, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	links := runtime.GOOS != "windows"
	if links {
		if err := os.Symlink(filepath.Join(proj, ".git", "hooks"), filepath.Join(proj, "hooks")); err != nil {
			t.Fatal(err)
		}
	}
	rules := []store.Rule{{Tool: "Bash", Pattern: "git log"}, {Tool: "Bash", Pattern: "git config"}, {Tool: "Bash", Pattern: "cp -r"},
		{Tool: "Bash", Pattern: "go build"}, {Tool: "Bash", Pattern: `\rm -rf`}, {Tool: "Bash", Pattern: `"rm" -rf`}, {Tool: "Bash", Pattern: "tee"}}
	once := []string{
		"git log -1 --format=%B --output=.git/hooks/pre-commit",
		"git log -1 --output .git/hooks/pre-commit",
		"git config filter.x.clean evil",
		"git config core.fsmonitor evil",
		"git config core.hooksPath /tmp",
		"git config --get user.name",
		"cp -r evil .git/hooks",
		"cp -r evil ./.git/hooks/",
		"cp -r evil sub/.claude",
		"go build -o bin/x ./cmd/x",
		"tee .envrc",
		`\rm -rf build`,
		`"rm" -rf build`,
		`'rm' -rf build`,
	}
	if links {
		once = append(once, "cp -r evil hooks")
	}
	for _, cmd := range once {
		if got, _ := Evaluate(Input{Tool: "Bash", Args: map[string]any{"command": cmd}, ProjectDir: proj, Rules: rules}); got != Ask {
			t.Errorf("%q: got %v, want ask", cmd, got)
		}
		if r, ok := AlwaysRule("Bash", map[string]any{"command": cmd}, proj); ok {
			t.Errorf("%q: «Всегда» offered (%q)", cmd, r.Pattern)
		}
	}
	for _, cmd := range []string{"git log --oneline", "cp -r a b", "go build ./..."} {
		if got, _ := Evaluate(Input{Tool: "Bash", Args: map[string]any{"command": cmd}, ProjectDir: proj, Rules: rules}); got != Allow {
			t.Errorf("%q: got %v, want allow", cmd, got)
		}
	}
}
