package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const gitTimeout = 30 * time.Second

// git runs git in dir; env entries are added to the environment.
func git(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	pre := []string{"-C", dir, "--literal-pathspecs"}
	if args[0] == "check-ignore" { // it rejects pathspec magic, literal included
		pre = pre[:2]
	}
	cmd := exec.CommandContext(ctx, "git", append(pre, args...)...)
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
// through a temporary copy of the index, so the user's index, refs and stash
// stay as they are. Outside git it returns "" and no error.
func Snapshot(ctx context.Context, dir string) (string, error) {
	if _, err := git(ctx, dir, []string{"LC_ALL=C"}, "rev-parse", "--is-inside-work-tree"); err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return "", nil
		}
		return "", err // safe.directory, a timeout, a missing dir: worth a log line
	}
	out, err := git(ctx, dir, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "tgsync-index-*")
	if err != nil {
		return "", err
	}
	idx := tmp.Name()
	tmp.Close()
	defer os.Remove(idx)
	// The copy keeps git's stat cache: unchanged files are not hashed again.
	orig := strings.TrimSpace(string(out))
	if data, err := os.ReadFile(orig); err == nil {
		if err := os.WriteFile(idx, data, 0o600); err != nil {
			return "", err
		}
		// Git re-reads a file whose stat matches its entry only when the
		// entry is not older than the index ("racy git"). The copy keeps the
		// original's mtime, or a same-size edit in the same second is missed.
		if st, err := os.Stat(orig); err == nil {
			_ = os.Chtimes(idx, st.ModTime(), st.ModTime())
		}
	} else {
		os.Remove(idx) // git rejects an empty index file; a missing one is fine
	}
	env := []string{"GIT_INDEX_FILE=" + idx}
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
