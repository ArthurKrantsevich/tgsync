package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// repo creates a git repo with the files committed.
func repo(t *testing.T, fs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	// Byte-exact contents: Windows runners default to core.autocrlf=true.
	gitT(t, dir, "config", "core.autocrlf", "false")
	for name, body := range fs {
		writeT(t, dir, name, body)
	}
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-qm", "init", "--allow-empty")
	return dir
}

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return string(out)
}

func writeT(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotOutsideGit(t *testing.T) {
	if tree, err := Snapshot(context.Background(), t.TempDir()); tree != "" || err != nil {
		t.Fatalf("%q %v", tree, err)
	}
}

func TestSnapshotEmptyRepo(t *testing.T) {
	dir := t.TempDir()
	gitT(t, dir, "init", "-q")
	writeT(t, dir, "a.txt", "a\n")
	if tree, err := Snapshot(context.Background(), dir); err != nil || len(tree) < 40 {
		t.Fatalf("%q %v", tree, err)
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != "?? a.txt\n" {
		t.Fatalf("status: %q", st)
	}
}

func TestSnapshotLeavesIndex(t *testing.T) {
	dir := repo(t, map[string]string{"a.go": "a\n", ".gitignore": "*.log\n"})
	writeT(t, dir, "a.go", "changed\n")
	writeT(t, dir, "new.go", "n\n")
	writeT(t, dir, "x.log", "ignored\n")
	before := gitT(t, dir, "status", "--porcelain")
	idx, _ := os.ReadFile(filepath.Join(dir, ".git", "index"))
	if _, err := Snapshot(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	idx2, _ := os.ReadFile(filepath.Join(dir, ".git", "index")) // before `git status` may refresh it
	after := gitT(t, dir, "status", "--porcelain")
	if before != after || string(idx) != string(idx2) {
		t.Fatalf("status %q -> %q, index changed: %v", before, after, string(idx) != string(idx2))
	}
}

func TestTurnStats(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "one\ntwo\n", "gone.go": "g\n", "img.bin": "\x00\x01"})
	writeT(t, dir, "mine.go", "user work\n") // untracked before the turn
	base, err := Snapshot(ctx, dir)
	if err != nil || base == "" {
		t.Fatal(base, err)
	}
	writeT(t, dir, "a.go", "one\n2\nthree\n")
	writeT(t, dir, "docs/план 1.md", "x\ny\n")
	writeT(t, dir, "mine.go", "user work\nagent\n")
	writeT(t, dir, "img.bin", "\x00\x02")
	_ = os.Remove(filepath.Join(dir, "gone.go"))
	end, _ := Snapshot(ctx, dir)
	writeT(t, dir, "a.go", "edited after the turn\n") // not part of the turn
	rels := []string{"a.go", "docs/план 1.md", "gone.go", "img.bin", "mine.go"}
	got, err := TurnStats(ctx, dir, base, end, rels)
	if err != nil {
		t.Fatal(err)
	}
	want := []Stat{
		{Rel: "a.go", Added: 2, Deleted: 1},
		{Rel: "docs/план 1.md", Added: 2, New: true},
		{Rel: "gone.go", Deleted: 1, Gone: true},
		{Rel: "img.bin", Binary: true},
		{Rel: "mine.go", Added: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestTurnDiff(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "old\n"})
	writeT(t, dir, "a.go", "mine\n") // uncommitted before the turn: not part of the turn diff
	base, _ := Snapshot(ctx, dir)
	writeT(t, dir, "a.go", "agent\n")
	writeT(t, dir, "b.go", "fresh\n")
	end, _ := Snapshot(ctx, dir)
	writeT(t, dir, "b.go", "later\n")
	d, err := TurnDiff(ctx, dir, base, end, []string{"a.go", "b.go"})
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"-mine", "+agent", "+fresh"} {
		if !strings.Contains(d, s) {
			t.Fatalf("diff lacks %q:\n%s", s, d)
		}
	}
	if strings.Contains(d, "old") {
		t.Fatalf("diff must start from the snapshot, not HEAD:\n%s", d)
	}
	if strings.Contains(d, "later") {
		t.Fatalf("diff must end at the turn end:\n%s", d)
	}
	if _, err := TurnDiff(ctx, dir, base, end, []string{"missing.go"}); err == nil {
		t.Fatal("no changes must be an error")
	}
}

func TestRestore(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "old\n", "del.go": "d\n", ".gitignore": "*.log\n"})
	writeT(t, dir, "a.go", "mine\n")
	writeT(t, dir, "u.go", "untracked\n")
	base, _ := Snapshot(ctx, dir)
	writeT(t, dir, "a.go", "agent\n")
	writeT(t, dir, "u.go", "agent\n")
	writeT(t, dir, "new.go", "n\n")
	writeT(t, dir, "x.log", "log\n")
	_ = os.Remove(filepath.Join(dir, "del.go"))
	end, _ := Snapshot(ctx, dir)
	res, err := Restore(ctx, dir, base, end, []string{"a.go", "del.go", "new.go", "u.go", "x.log"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res, RestoreResult{Restored: []string{"a.go", "del.go", "new.go", "u.go"}, Ignored: []string{"x.log"}}) {
		t.Fatalf("%+v", res)
	}
	for name, want := range map[string]string{"a.go": "mine\n", "del.go": "d\n", "u.go": "untracked\n"} {
		if b, _ := os.ReadFile(filepath.Join(dir, name)); string(b) != want {
			t.Fatalf("%s = %q", name, b)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "new.go")); !os.IsNotExist(err) {
		t.Fatal("new.go must be removed")
	}
	if st := gitT(t, dir, "status", "--porcelain"); st != " M a.go\n?? u.go\n" {
		t.Fatalf("status after restore: %q", st)
	}
}

func TestTurnInSubdir(t *testing.T) {
	ctx := context.Background()
	root := repo(t, map[string]string{"app/a.go": "old\n", "other.go": "o\n"})
	dir := filepath.Join(root, "app")
	base, _ := Snapshot(ctx, dir)
	writeT(t, dir, "a.go", "new\n")
	writeT(t, root, "other.go", "changed outside the project\n")
	end, _ := Snapshot(ctx, dir)
	st, err := TurnStats(ctx, dir, base, end, []string{"a.go"})
	if err != nil || !reflect.DeepEqual(st, []Stat{{Rel: "a.go", Added: 1, Deleted: 1}}) {
		t.Fatalf("%+v %v", st, err)
	}
	if res, err := Restore(ctx, dir, base, end, []string{"a.go"}); err != nil || len(res.Restored) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "old\n" {
		t.Fatalf("a.go = %q", b)
	}
}

func TestRestoreKeepsLaterEdits(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "old\n", "b.go": "b\n"})
	base, _ := Snapshot(ctx, dir)
	writeT(t, dir, "a.go", "agent\n")
	writeT(t, dir, "b.go", "agent\n")
	end, _ := Snapshot(ctx, dir)
	writeT(t, dir, "a.go", "user after the turn\n")
	res, err := Restore(ctx, dir, base, end, []string{"a.go", "b.go"})
	if err != nil || !reflect.DeepEqual(res, RestoreResult{Restored: []string{"b.go"}, Changed: []string{"a.go"}}) {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.go")); string(b) != "user after the turn\n" {
		t.Fatalf("a.go = %q", b)
	}
}

func TestRestoreKeepsNewFilesWhenIgnoreRulesChanged(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{".gitignore": "*.yaml\n"})
	writeT(t, dir, "local.yaml", "secret: 1\n") // ignored: not in the snapshot
	base, _ := Snapshot(ctx, dir)
	writeT(t, dir, ".gitignore", "")
	writeT(t, dir, "local.yaml", "secret: 2\n")
	end, _ := Snapshot(ctx, dir)
	res, err := Restore(ctx, dir, base, end, []string{".gitignore", "local.yaml"})
	if err != nil || !reflect.DeepEqual(res, RestoreResult{Restored: []string{".gitignore"}, Kept: []string{"local.yaml"}}) {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "local.yaml")); err != nil {
		t.Fatal("a file that may have existed before the turn must not be deleted")
	}
}

func TestSnapshotScopedToDir(t *testing.T) {
	ctx := context.Background()
	root := repo(t, map[string]string{"app/a.go": "a\n"})
	writeT(t, root, "big.bin", "outside the project\n")
	tree, err := Snapshot(ctx, filepath.Join(root, "app"))
	if err != nil {
		t.Fatal(err)
	}
	if ls := gitT(t, root, "ls-tree", "-r", "--name-only", tree); strings.Contains(ls, "big.bin") {
		t.Fatalf("files outside the project must not be hashed: %s", ls)
	}
}

func TestRestoreRemovesEmptyDirs(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	base, _ := Snapshot(ctx, dir)
	writeT(t, dir, "docs/new/x.md", "x\n")
	end, _ := Snapshot(ctx, dir)
	if _, err := Restore(ctx, dir, base, end, []string{"docs/new/x.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs")); !os.IsNotExist(err) {
		t.Fatal("empty folders of removed files must go too")
	}
}

func TestSnapshotReportsGitFailure(t *testing.T) {
	if _, err := Snapshot(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a git failure other than 'not a repository' must be an error")
	}
}
