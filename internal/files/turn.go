package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// gitTimeout bounds each git run; tests shorten it.
var gitTimeout = 30 * time.Second

// gitHook, when set by a test, runs before each git with its context.
var gitHook func(ctx context.Context, args []string)

// staleLock is the age after which a side index lock is surely abandoned:
// the git that made it has hit gitTimeout by then. A younger lock may be a
// live git of another node sharing the cache folder.
func staleLock() time.Duration { return gitTimeout + 30*time.Second }

// Side indexes not used for sideMaxAge are removed, checked at most once per
// pruneEvery.
const (
	sideMaxAge = 30 * 24 * time.Hour
	pruneEvery = 24 * time.Hour
)

var (
	pruneMu   sync.Mutex
	lastPrune time.Time
)

// resetPrune lets the next Snapshot prune again.
func resetPrune() {
	pruneMu.Lock()
	lastPrune = time.Time{}
	pruneMu.Unlock()
}

// gitCmd prepares git with options pre and command args in ctx. On
// cancellation git gets a signal it can handle first, so it removes its lock
// files itself, and is killed only after WaitDelay; a plain kill leaves
// index.lock behind. Windows has only the kill.
func gitCmd(ctx context.Context, pre, args []string) *exec.Cmd {
	if gitHook != nil {
		gitHook(ctx, args)
	}
	cmd := exec.CommandContext(ctx, "git", append(pre, args...)...)
	cmd.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return cmd.Process.Kill()
		}
		return cmd.Process.Signal(syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// CacheDir is the base of the side indexes (see Snapshot); tests point it
// elsewhere.
var CacheDir = os.UserCacheDir

// sideLocks serializes snapshots sharing a side index: git refuses a second
// writer of an index while the first holds its lock file.
var sideLocks sync.Map // side index path → *sync.Mutex

// git runs git in dir; env entries are added to the environment.
func git(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	pre := []string{"-C", dir, "--literal-pathspecs"}
	if args[0] == "check-ignore" { // it rejects pathspec magic, literal included
		pre = pre[:2]
	}
	cmd := gitCmd(ctx, pre, args)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Snapshot writes the whole working tree, untracked files included and
// ignored files excluded, as a git tree and returns its hash. It goes
// through a side index of its own, so the user's index, refs and stash stay
// as they are. Outside git it returns "" and no error.
//
// The side index is kept between snapshots (one per repository and project
// folder, under CacheDir): its stat cache then covers untracked files too,
// and a big untracked folder is hashed once instead of twice per turn. It
// starts as a copy of the user's index. Only git writes it afterwards, so
// git's check for entries not older than the index ("racy git") works as
// in the user's own index.
func Snapshot(ctx context.Context, dir string) (string, error) {
	if _, err := git(ctx, dir, []string{"LC_ALL=C"}, "rev-parse", "--is-inside-work-tree"); err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return "", nil
		}
		return "", err // safe.directory, a timeout, a missing dir: worth a log line
	}
	orig, err := userIndex(ctx, dir)
	if err != nil {
		return "", err
	}
	side := sideIndex(orig, dir)
	if side == "" { // no cache folder: a one-off copy, as good as the first snapshot
		return snapshotTemp(ctx, dir, orig)
	}
	pruneSides(filepath.Dir(side))
	mu := sideLock(side)
	mu.Lock()
	defer mu.Unlock()
	// We hold the side index: a lock file left by a git killed mid-write
	// would fail every snapshot of this repository for good.
	removeStaleLock(side)
	tree, err := snapshotWith(ctx, dir, orig, side)
	if err == nil || ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		// A git that ran out of time would only run out of time again.
		chmodSide(side)
		return tree, err
	}
	// A damaged side index, or one whose blobs git gc pruned: start over
	// from the user's index.
	slog.Debug("snapshot side index reset", "dir", dir, "err", err)
	_ = os.Remove(side)
	tree, err = snapshotWith(ctx, dir, orig, side)
	chmodSide(side)
	if err == nil || ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return tree, err
	}
	// The side index itself is unusable: a read-only cache folder, or a
	// lock held by another node. A one-off copy still works.
	slog.Debug("snapshot through a temporary index", "dir", dir, "err", err)
	return snapshotTemp(ctx, dir, orig)
}

// chmodSide keeps the side index private: git writes it with its umask. A
// mode change keeps the mtime the racy check needs.
func chmodSide(side string) { _ = os.Chmod(side, 0o600) }

// sideLock returns the in-process lock of side index side.
func sideLock(side string) *sync.Mutex {
	mu, _ := sideLocks.LoadOrStore(side, &sync.Mutex{})
	return mu.(*sync.Mutex)
}

// snapshotTemp takes the snapshot through a one-off copy of the user's index.
func snapshotTemp(ctx context.Context, dir, orig string) (string, error) {
	tmp, err := os.MkdirTemp("", "tgsync-index-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	return snapshotWith(ctx, dir, orig, filepath.Join(tmp, "index"))
}

// removeStaleLock removes the lock file of side index side when it is older
// than staleLock. The caller holds the side's in-process lock, so no git of
// this node uses it.
func removeStaleLock(side string) {
	lock := side + ".lock"
	st, err := os.Stat(lock)
	if err != nil || time.Since(st.ModTime()) < staleLock() {
		return
	}
	if err := os.Remove(lock); err == nil {
		slog.Info("removed a stale snapshot index lock", "path", lock)
	}
}

// pruneSides removes, at most once per pruneEvery, the side indexes in
// folder not written for sideMaxAge, with their leftover lock and seed
// files. A side index in use by this node is skipped.
func pruneSides(folder string) {
	pruneMu.Lock()
	if time.Since(lastPrune) < pruneEvery {
		pruneMu.Unlock()
		return
	}
	lastPrune = time.Now()
	pruneMu.Unlock()
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	for _, e := range entries {
		name := e.Name()
		base := strings.TrimSuffix(strings.TrimSuffix(name, ".lock"), ".seed")
		if e.IsDir() || !strings.HasSuffix(base, ".index") {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < sideMaxAge {
			continue
		}
		mu := sideLock(filepath.Join(folder, base))
		if !mu.TryLock() {
			continue
		}
		_ = os.Remove(filepath.Join(folder, name))
		mu.Unlock()
	}
}

// userIndex returns the absolute path of the repository's index.
func userIndex(ctx context.Context, dir string) (string, error) {
	out, err := git(ctx, dir, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// sidePath returns the side index of dir's repository and project folder,
// "" when there is no cache folder.
func sidePath(ctx context.Context, dir string) (string, error) {
	orig, err := userIndex(ctx, dir)
	if err != nil {
		return "", err
	}
	return sideIndex(orig, dir), nil
}

// sideIndex names the side index of index orig for project folder dir. The
// folder is part of the key: a snapshot covers only its folder, so projects
// sharing a repository keep their own caches.
func sideIndex(orig, dir string) string {
	base, err := CacheDir()
	if err != nil || base == "" {
		return ""
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return ""
	}
	folder := filepath.Join(base, "tgsync", "snapshots")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return ""
	}
	sum := sha256.Sum256([]byte(filepath.Clean(orig) + "\x00" + filepath.Clean(abs)))
	return filepath.Join(folder, hex.EncodeToString(sum[:12])+".index")
}

// snapshotWith takes the snapshot through index idx, seeding it from orig
// when it does not exist yet.
func snapshotWith(ctx context.Context, dir, orig, idx string) (string, error) {
	if _, err := os.Stat(idx); os.IsNotExist(err) {
		if err := seedIndex(orig, idx); err != nil {
			return "", err
		}
	}
	env := []string{"GIT_INDEX_FILE=" + idx}
	if err := syncIgnored(ctx, dir, env); err != nil {
		return "", err
	}
	// Only the project dir: outside it the tree keeps the index's content,
	// and big untracked files elsewhere in the repo are not hashed each turn.
	if _, err := git(ctx, dir, env, "add", "-A", "--", "."); err != nil {
		return "", err
	}
	tree, err := git(ctx, dir, env, "write-tree")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(tree)), nil
}

// seedIndex copies the user's index orig to idx. A missing orig leaves idx
// missing too: git rejects an empty index file, but starts a missing one.
func seedIndex(orig, idx string) error {
	data, err := os.ReadFile(orig)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	st, err := os.Stat(orig)
	if err != nil {
		return err
	}
	tmp := idx + ".seed"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	// Git re-reads a file whose stat matches its entry only when the entry
	// is not older than the index ("racy git"). The copy keeps the
	// original's mtime, or a same-size edit in the same second is missed.
	if err := os.Chtimes(tmp, st.ModTime(), st.ModTime()); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, idx); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// syncIgnored makes the side index track the same ignored files as the
// user's index. `add -A` never drops a tracked file, so without it a file
// ignored after a snapshot would stay in every later one, and a file the
// user added with -f would be missing when the side index is older.
func syncIgnored(ctx context.Context, dir string, side []string) error {
	ignored := func(env []string) (map[string]bool, error) {
		out, err := git(ctx, dir, env, "ls-files", "-z", "-c", "-i", "--exclude-standard", "--", ".")
		if err != nil {
			return nil, err
		}
		set := map[string]bool{}
		for _, p := range strings.Split(string(out), "\x00") {
			if p != "" {
				set[p] = true
			}
		}
		return set, nil
	}
	user, err := ignored(nil)
	if err != nil {
		return err
	}
	have, err := ignored(side)
	if err != nil {
		return err
	}
	var drop, add []string
	for p := range have {
		if !user[p] {
			drop = append(drop, p)
		}
	}
	for p := range user {
		if !have[p] {
			add = append(add, p)
		}
	}
	if len(drop) > 0 {
		if err := gitStdin(ctx, dir, side, drop, "update-index", "-z", "--force-remove", "--stdin"); err != nil {
			return err
		}
	}
	if len(add) > 0 {
		if err := gitStdin(ctx, dir, side, add, "update-index", "-z", "--add", "--remove", "--stdin"); err != nil {
			return err
		}
	}
	return nil
}

// gitStdin runs git with paths on stdin, NUL-terminated.
func gitStdin(ctx context.Context, dir string, env, paths []string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := gitCmd(ctx, []string{"-C", dir}, args)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Stat is one file's change during a turn.
type Stat struct {
	Rel            string
	Added, Deleted int
	New            bool // not in the start snapshot
	Gone           bool // deleted during the turn
	Binary         bool
}

// changes compares two trees for rels (paths relative to dir): status
// letters (A, M, D, T) and numstat figures {added, deleted, binary=1} by path.
func changes(ctx context.Context, dir, from, to string, rels []string) (status map[string]string, nums map[string][3]int, err error) {
	args := func(mode string) []string {
		return append([]string{"diff", "-z", "--no-renames", "--relative", mode, from, to, "--"}, rels...)
	}
	out, err := git(ctx, dir, nil, args("--name-status")...)
	if err != nil {
		return nil, nil, err
	}
	status = map[string]string{}
	parts := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		status[parts[i+1]] = parts[i]
	}
	out, err = git(ctx, dir, nil, args("--numstat")...)
	if err != nil {
		return nil, nil, err
	}
	nums = map[string][3]int{}
	for _, rec := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		f := strings.SplitN(rec, "\t", 3)
		if len(f) != 3 {
			continue
		}
		if f[0] == "-" {
			nums[f[2]] = [3]int{0, 0, 1}
			continue
		}
		a, _ := strconv.Atoi(f[0])
		d, _ := strconv.Atoi(f[1])
		nums[f[2]] = [3]int{a, d, 0}
	}
	return status, nums, nil
}

// TurnStats returns the change of each rel between the turn's start and end
// snapshots, in the order of rels. A file that did not change has a zero Stat.
func TurnStats(ctx context.Context, dir, base, end string, rels []string) ([]Stat, error) {
	status, nums, err := changes(ctx, dir, base, end, rels)
	if err != nil {
		return nil, err
	}
	out := make([]Stat, 0, len(rels))
	for _, rel := range rels {
		key := filepath.ToSlash(rel)
		n := nums[key]
		out = append(out, Stat{Rel: rel, Added: n[0], Deleted: n[1], Binary: n[2] == 1,
			New: status[key] == "A", Gone: status[key] == "D"})
	}
	return out, nil
}

// TurnDiff returns the diff of rels between the turn's start and end snapshots.
func TurnDiff(ctx context.Context, dir, base, end string, rels []string) (string, error) {
	out, err := git(ctx, dir, nil, append([]string{"diff", "--no-renames", "--relative", base, end, "--"}, rels...)...)
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return "", errors.New("нет изменений за ход")
	}
	return capDiff(out)
}

// RestoreResult reports what Restore did with each file.
type RestoreResult struct {
	Restored []string // back to the start of the turn (new files removed)
	Ignored  []string // ignored by git: no copy in the snapshot
	Changed  []string // changed after the turn ended: left as they are
	Kept     []string // new, but ignore rules changed in the turn: may predate it
}

// Restore brings rels back to the start snapshot base: changed and deleted
// files get their old content, new files are removed. A file that changed
// after the turn ended (it differs from end) is left alone, so later work is
// never lost. When the turn changed a .gitignore, a "new" file may be an
// ignored file that existed before, so new files are kept. The index is not
// touched. It stops at the first error.
func Restore(ctx context.Context, dir, base, end string, rels []string) (RestoreResult, error) {
	var res RestoreResult
	cur, err := Snapshot(ctx, dir)
	if err != nil {
		return res, err
	}
	if cur == "" {
		return res, errors.New("проект не в git-репозитории")
	}
	later, _, err := changes(ctx, dir, end, cur, rels)
	if err != nil {
		return res, err
	}
	status, _, err := changes(ctx, dir, base, end, rels)
	if err != nil {
		return res, err
	}
	rules, _, err := changes(ctx, dir, base, end, nil)
	if err != nil {
		return res, err
	}
	ruleChanged := false
	for p := range rules {
		if path.Base(p) == ".gitignore" {
			ruleChanged = true
		}
	}
	for _, rel := range rels {
		key := filepath.ToSlash(rel)
		if later[key] != "" {
			res.Changed = append(res.Changed, rel)
			continue
		}
		switch status[key] {
		case "A":
			if ruleChanged {
				res.Kept = append(res.Kept, rel)
				continue
			}
			if err := os.Remove(filepath.Join(dir, rel)); err != nil && !os.IsNotExist(err) {
				return res, err
			}
			removeEmptyParents(dir, filepath.Dir(rel))
		case "M", "D", "T":
			if _, err := git(ctx, dir, nil, "restore", "--source="+base, "--worktree", "--", rel); err != nil {
				return res, err
			}
		default:
			if _, err := git(ctx, dir, nil, "check-ignore", "-q", "--", rel); err == nil {
				res.Ignored = append(res.Ignored, rel)
			}
			continue
		}
		res.Restored = append(res.Restored, rel)
	}
	return res, nil
}

// removeEmptyParents removes rel (a folder inside dir) and its parents while
// they are empty. os.Remove refuses a folder that still has files.
func removeEmptyParents(dir, rel string) {
	for rel != "." && rel != "" && rel != string(filepath.Separator) {
		if os.Remove(filepath.Join(dir, rel)) != nil {
			return
		}
		rel = filepath.Dir(rel)
	}
}
