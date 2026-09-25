package files

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestTracker(t *testing.T) {
	tr := NewTracker(osPath("/w/demo"))
	tr.Note("Write", map[string]any{"file_path": osPath("/w/demo/docs/spec.md")})
	tr.Note("Edit", map[string]any{"file_path": "main.go"})
	tr.Note("NotebookEdit", map[string]any{"notebook_path": osPath("/w/demo/n.ipynb")})
	tr.Note("Write", map[string]any{"file_path": osPath("/etc/hosts")})
	tr.Note("Read", map[string]any{"file_path": osPath("/w/demo/read.go")})
	tr.Note("Edit", map[string]any{"file_path": osPath("/w/demo/main.go")})
	if got := strings.Join(tr.Changed(), ","); got != "docs/spec.md,main.go,n.ipynb" {
		t.Fatalf("changed: %s", got)
	}
	text := "Spec written to `docs/spec.md`, please review. Also see " + osPath("/w/demo/main.go") + " and README.md."
	if got := strings.Join(tr.Mentioned(text), ","); got != "docs/spec.md,main.go" {
		t.Fatalf("mentioned: %s", got)
	}
	if got := tr.Mentioned(`see docs\spec.md`); runtime.GOOS == "windows" && strings.Join(got, ",") != "docs/spec.md" {
		t.Fatalf("a Windows spelling must be found: %v", got)
	}
	tr.ResetTurn()
	if len(tr.Changed()) != 0 {
		t.Fatal("ResetTurn must clear the turn list")
	}
	if got := tr.Mentioned("see docs/spec.md"); len(got) != 1 {
		t.Fatalf("session-wide mentions must survive ResetTurn: %v", got)
	}
	if tr.WasSent("a.md", "h1") {
		t.Fatal("nothing sent yet")
	}
	tr.MarkSent("a.md", "h1")
	if !tr.WasSent("a.md", "h1") || tr.WasSent("a.md", "h2") || !tr.IsSent("a.md") {
		t.Fatal("dedup by content hash is wrong")
	}
	if got := tr.Mentioned("see ./docs/spec.md and /w/demo/main.go.bak"); strings.Join(got, ",") != "docs/spec.md" {
		t.Fatalf("./ prefix or abs boundary: %v", got)
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "docs"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "docs", "a.md"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=1"), 0o600)
	big := filepath.Join(dir, "big.bin")
	f, _ := os.Create(big)
	_ = f.Truncate(51 << 20)
	f.Close()
	abs, rel, err := Resolve(dir, "docs/a.md", nil)
	if err != nil || rel != "docs/a.md" || abs != filepath.Join(dir, "docs", "a.md") {
		t.Fatalf("relative: %q %q %v", abs, rel, err)
	}
	if _, rel, err := Resolve(dir, filepath.Join(dir, "docs/a.md"), nil); err != nil || rel != "docs/a.md" {
		t.Fatalf("absolute: %q %v", rel, err)
	}
	bad := map[string]string{
		"outside":   "/etc/hosts",
		"escape":    "../x.md",
		"missing":   "nope.md",
		"directory": "docs",
		"protected": ".env",
		"too big":   "big.bin",
	}
	secret := filepath.Join(t.TempDir(), "id_rsa")
	_ = os.WriteFile(secret, []byte("KEY"), 0o600)
	_ = os.Symlink(secret, filepath.Join(dir, "link.txt"))
	_ = os.Symlink(filepath.Join(dir, ".env"), filepath.Join(dir, "env-link"))
	bad["symlink outside"] = "link.txt"
	bad["symlink to protected"] = "env-link"
	for name, p := range bad {
		if _, _, err := Resolve(dir, p, []string{filepath.Join(dir, ".env")}); err == nil {
			t.Errorf("%s: %s must be refused", name, p)
		}
	}
}

func TestMatch(t *testing.T) {
	cases := []struct {
		globs []string
		rel   string
		want  bool
	}{
		{[]string{"**/*.md"}, "docs/x.md", true},
		{[]string{"**/*.md"}, "x.md", true},
		{[]string{"*.pdf"}, "a.pdf", true},
		{[]string{"*.pdf"}, "d/a.pdf", false},
		{[]string{"docs/*.md"}, "docs/x.md", true},
		{nil, "x.md", false},
	}
	for _, c := range cases {
		if got := Match(c.globs, c.rel); got != c.want {
			t.Errorf("Match(%v, %q) = %v", c.globs, c.rel, got)
		}
	}
}

func TestIsDocument(t *testing.T) {
	for name, want := range map[string]bool{"docs/plan.md": true, "r.PDF": true, "a.png": true, "main.go": false, "x": false} {
		if IsDocument(name) != want {
			t.Errorf("IsDocument(%q) != %v", name, want)
		}
	}
}

func TestResolveDir(t *testing.T) {
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, "docs", "sub"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644)
	outside := t.TempDir()
	_ = os.Symlink(outside, filepath.Join(dir, "out"))
	for p, want := range map[string]string{"": "", ".": "", "docs": "docs", "docs/sub/": "docs/sub", "/": ""} {
		p := p
		if p == "/" {
			p = dir
		}
		if _, rel, err := ResolveDir(dir, p); err != nil || rel != want {
			t.Errorf("ResolveDir(%q) = %q, %v; want %q", p, rel, err, want)
		}
	}
	for _, p := range []string{"..", "../x", "/etc", "a.txt", "missing", "out"} {
		if _, _, err := ResolveDir(dir, p); err == nil {
			t.Errorf("ResolveDir(%q) must fail", p)
		}
	}
}

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{"app.log": "app.log", "../../etc/passwd": "passwd", "мой отчёт.pdf": "мой_отчёт.pdf", "": "file", "a;rm -rf.sh": "a_rm_-rf.sh"} {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContainsPathBackslash(t *testing.T) {
	if !containsPath(`changed src\a.go today`, `src\a.go`) {
		t.Fatal(`src\a.go must match as a whole path`)
	}
	if containsPath(`see lib\src\a.go`, `src\a.go`) {
		t.Fatal(`src\a.go inside lib\src\a.go is part of a longer path`)
	}
}

func TestResolveProtectedCase(t *testing.T) {
	old := FoldCase
	defer func() { FoldCase = old }()
	FoldCase = true
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".ENV"), []byte("T"), 0o600)
	_, _, err := Resolve(dir, ".ENV", []string{filepath.Join(dir, ".env")})
	if err == nil || !strings.Contains(err.Error(), "служебный") {
		t.Fatalf("a differently-cased protected file must be refused as protected, got %v", err)
	}
}
