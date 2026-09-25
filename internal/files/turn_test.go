package files

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Side indexes of the test repos go to a temporary cache, not the user's.
	cache, err := os.MkdirTemp("", "tgsync-cache-")
	if err != nil {
		panic(err)
	}
	CacheDir = func() (string, error) { return cache, nil }
	code := m.Run()
	os.RemoveAll(cache)
	os.Exit(code)
}

// TestSnapshotSeesSameSizeEditInSameSecond: git trusts a file whose size and
// mtime match its index entry, unless the entry is "racy" (not older than
// the index file). A snapshot must keep that check, or an edit that keeps
// the size within the same second is lost. core.trustctime=false keeps the
// ctime (which Windows does not change on a write) from hiding a lost edit.
func TestSnapshotSeesSameSizeEditInSameSecond(t *testing.T) {
	ctx := context.Background()
	stamp := func(t *testing.T, dir, name string, at time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(dir, name), at, at); err != nil {
			t.Fatal(err)
		}
	}
	blob := func(t *testing.T, dir, tree string) string {
		t.Helper()
		return gitT(t, dir, "cat-file", "-p", tree+":a.go")
	}
	// The side index starts as a copy of the user's index: the copy must
	// keep the original's mtime, or the user's racy entries look clean.
	t.Run("user index", func(t *testing.T) {
		dir := repo(t, map[string]string{"a.go": "old\n"})
		gitT(t, dir, "config", "core.trustctime", "false")
		at := time.Now().Add(-time.Hour).Truncate(time.Second)
		stamp(t, dir, "a.go", at)
		gitT(t, dir, "update-index", "--refresh") // the index records a.go at `at`
		stamp(t, dir, ".git/index", at)           // ...and was written in that second
		writeT(t, dir, "a.go", "new\n")           // same size, same second
		stamp(t, dir, "a.go", at)
		base, err := Snapshot(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := blob(t, dir, base); got != "new\n" {
			t.Fatalf("the edit was lost: %q", got)
		}
	})
	// Later snapshots reuse the side index git wrote: an entry not older
	// than that write must be checked again.
	t.Run("side index", func(t *testing.T) {
		dir := repo(t, map[string]string{"a.go": "old\n"})
		gitT(t, dir, "config", "core.trustctime", "false")
		at := time.Now().Add(time.Hour).Truncate(time.Second) // not older than any index write below
		stamp(t, dir, "a.go", at)
		base, err := Snapshot(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		writeT(t, dir, "a.go", "new\n") // same size, same second
		stamp(t, dir, "a.go", at)
		end, err := Snapshot(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		if blob(t, dir, base) != "old\n" || blob(t, dir, end) != "new\n" {
			t.Fatalf("the edit was lost: base %q end %q", blob(t, dir, base), blob(t, dir, end))
		}
	})
}

// TestSnapshotReusesStatCacheForUntracked: untracked files are not in the
// user's index, so only a side index kept between snapshots lets git skip
// hashing them again. The test changes an untracked file behind git's back
// (same size and mtime, ctime ignored): a snapshot that trusts its stat
// cache still sees the old content.
func TestSnapshotReusesStatCacheForUntracked(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	gitT(t, dir, "config", "core.trustctime", "false")
	at := time.Now().Add(-time.Hour).Truncate(time.Second)
	writeT(t, dir, "node_modules/x.js", "one\n")
	if err := os.Chtimes(filepath.Join(dir, "node_modules/x.js"), at, at); err != nil {
		t.Fatal(err)
	}
	first, err := Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	writeT(t, dir, "node_modules/x.js", "two\n")
	if err := os.Chtimes(filepath.Join(dir, "node_modules/x.js"), at, at); err != nil {
		t.Fatal(err)
	}
	second, err := Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("the untracked file was hashed again: the stat cache was not reused")
	}
	if idx := gitT(t, dir, "status", "--porcelain"); idx != "?? node_modules/\n" {
		t.Fatalf("the user's index changed: %q", idx)
	}
}

// TestSnapshotFollowsIgnoreRules: the side index remembers untracked files,
// but a file ignored later leaves the snapshot, while a file the user
// tracks despite the ignore rules stays in it.
func TestSnapshotFollowsIgnoreRules(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n", "keep.log": "k\n"})
	writeT(t, dir, "tmp.log", "t\n")
	if _, err := Snapshot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	writeT(t, dir, ".gitignore", "*.log\n")
	tree, err := Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	ls := gitT(t, dir, "ls-tree", "--name-only", tree)
	if strings.Contains(ls, "tmp.log") || !strings.Contains(ls, "keep.log") {
		t.Fatalf("tree: %s", ls)
	}
	writeT(t, dir, "forced.log", "f\n")
	gitT(t, dir, "add", "-f", "forced.log")
	if tree, err = Snapshot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if ls := gitT(t, dir, "ls-tree", "--name-only", tree); !strings.Contains(ls, "forced.log") {
		t.Fatalf("a file the user added with -f must stay: %s", ls)
	}
}

// TestSnapshotRecoversFromBrokenSideIndex: a damaged side index is dropped
// and seeded again from the user's index.
func TestSnapshotRecoversFromBrokenSideIndex(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	want, err := Snapshot(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	side, err := sidePath(ctx, dir)
	if err != nil || side == "" {
		t.Fatalf("side index: %q %v", side, err)
	}
	if st, err := os.Stat(side); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && st.Mode().Perm() != 0o600 {
		t.Fatalf("side index mode %v, want 0600", st.Mode().Perm())
	}
	if err := os.WriteFile(side, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := Snapshot(ctx, dir); err != nil || got != want {
		t.Fatalf("%q %v, want %q", got, err, want)
	}
}

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

// headTree is the tree of HEAD: the snapshot of a clean checkout.
func headTree(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(gitT(t, dir, "rev-parse", "HEAD^{tree}"))
}

// setMtime backdates path by age.
func setMtime(t *testing.T, path string, age time.Duration) {
	t.Helper()
	old := time.Now().Add(-age)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// A git killed on its timeout (or on node shutdown) leaves <side>.lock
// behind. Every later snapshot of that repository used to fail with
// "index.lock exists", and with it turn summaries and Restore.
func TestSnapshotRemovesStaleSideLock(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	if _, err := Snapshot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	side, err := sidePath(ctx, dir)
	if err != nil || side == "" {
		t.Fatalf("side index: %q %v", side, err)
	}
	if err := os.WriteFile(side+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	setMtime(t, side+".lock", time.Hour)
	if got, err := Snapshot(ctx, dir); err != nil || got != headTree(t, dir) {
		t.Fatalf("%q %v, want %q", got, err, headTree(t, dir))
	}
	if _, err := os.Stat(side + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("stale lock kept: %v", err)
	}
}

// A fresh lock may belong to another node sharing the cache folder: it is
// left alone, and the snapshot still works through a temporary index.
func TestSnapshotKeepsFreshSideLock(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	if _, err := Snapshot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	side, _ := sidePath(ctx, dir)
	if err := os.WriteFile(side+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(side + ".lock")
	if got, err := Snapshot(ctx, dir); err != nil || got != headTree(t, dir) {
		t.Fatalf("%q %v, want %q", got, err, headTree(t, dir))
	}
	if _, err := os.Stat(side + ".lock"); err != nil {
		t.Fatalf("fresh lock removed: %v", err)
	}
}

// A git that hit its timeout is not run again from scratch: the retry used
// to block the turn start or end for another gitTimeout.
func TestSnapshotNoRetryAfterTimeout(t *testing.T) {
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	oldTimeout := gitTimeout
	gitTimeout = 200 * time.Millisecond
	adds := 0
	gitHook = func(ctx context.Context, args []string) {
		if args[0] == "add" {
			adds++
			<-ctx.Done()
		}
	}
	defer func() { gitTimeout, gitHook = oldTimeout, nil }()
	if _, err := Snapshot(ctx, dir); err == nil {
		t.Fatal("a timed out git must be an error")
	}
	if adds != 1 {
		t.Fatalf("git add ran %d times, want 1", adds)
	}
}

// An unusable cache folder (read-only) used to fail every snapshot; the
// one-off temporary index works there.
func TestSnapshotFallsBackWhenCacheReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("folder permissions do not stop writes on Windows")
	}
	ctx := context.Background()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	cache := t.TempDir()
	folder := filepath.Join(cache, "tgsync", "snapshots")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(folder, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(folder, 0o700)
	if f, err := os.CreateTemp(folder, "probe"); err == nil { // root ignores modes
		f.Close()
		t.Skip("the folder is writable anyway")
	}
	old := CacheDir
	CacheDir = func() (string, error) { return cache, nil }
	defer func() { CacheDir = old }()
	if got, err := Snapshot(ctx, dir); err != nil || got != headTree(t, dir) {
		t.Fatalf("%q %v, want %q", got, err, headTree(t, dir))
	}
}

// Side indexes of projects not used for a month are removed; recent ones
// stay.
func TestSnapshotPrunesOldSideIndexes(t *testing.T) {
	ctx := context.Background()
	cache := t.TempDir()
	folder := filepath.Join(cache, "tgsync", "snapshots")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	old, recent := filepath.Join(folder, "old.index"), filepath.Join(folder, "recent.index")
	for _, p := range []string{old, old + ".lock", recent} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setMtime(t, old, 31*24*time.Hour)
	setMtime(t, old+".lock", 31*24*time.Hour)
	setMtime(t, recent, 24*time.Hour)
	oldCache := CacheDir
	CacheDir = func() (string, error) { return cache, nil }
	defer func() { CacheDir = oldCache }()
	resetPrune()
	dir := repo(t, map[string]string{"a.go": "a\n"})
	if _, err := Snapshot(ctx, dir); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{old, old + ".lock"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s kept: %v", filepath.Base(p), err)
		}
	}
	if _, err := os.Stat(recent); err != nil {
		t.Fatalf("recent side index removed: %v", err)
	}
}

func TestSnapshotReportsGitFailure(t *testing.T) {
	if _, err := Snapshot(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("a git failure other than 'not a repository' must be an error")
	}
}
