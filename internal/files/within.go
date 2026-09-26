package files

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Abs makes p absolute against dir, spelled the way the OS opens it. On
// Windows it also folds the other spellings of the same file: no drive
// (\x, /x) means dir's drive, Git Bash's /c/x means C:\x, and trailing
// dots, spaces and :streams on a name are dropped.
func Abs(dir, p string) string {
	if runtime.GOOS == "windows" {
		return absWindows(dir, p)
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	return filepath.Clean(p)
}

var gitBashDrive = regexp.MustCompile(`^/([a-zA-Z])(/|$)`)

// absWindows is Abs for Windows paths; plain string work, so it is tested
// on every OS.
func absWindows(dir, p string) string {
	s := strings.ReplaceAll(p, `\`, "/")
	d := strings.ReplaceAll(dir, `\`, "/")
	if m := gitBashDrive.FindStringSubmatch(s); m != nil {
		s = strings.ToUpper(m[1]) + ":/" + s[len(m[0]):]
	}
	vol := func(x string) string {
		if len(x) >= 2 && x[1] == ':' {
			return x[:2]
		}
		return ""
	}
	v := vol(s)
	switch {
	case v != "":
		s = s[2:]
	case strings.HasPrefix(s, "/"):
		v = vol(d)
	default:
		v = vol(d)
		s = d[len(v):] + "/" + s
	}
	parts := strings.Split(s, "/")
	for i, part := range parts {
		if j := strings.IndexByte(part, ':'); j >= 0 {
			part = part[:j]
		}
		if part != "." && part != ".." {
			part = strings.TrimRight(part, ". ")
		}
		parts[i] = part
	}
	s = path.Clean("/" + strings.Join(parts, "/"))
	return v + strings.ReplaceAll(s, "/", `\`)
}

// SameFile reports whether a and b are the same existing file. It catches
// what string comparison misses: hard links, symlinks, Windows short names.
func SameFile(a, b string) bool {
	sa, err := os.Stat(a)
	if err != nil {
		return false
	}
	sb, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(sa, sb)
}

// FoldCase is true where file systems ignore case by default (Windows,
// macOS), so paths that differ only in case name the same file.
var FoldCase = runtime.GOOS == "windows" || runtime.GOOS == "darwin"

// Within reports whether p is base or inside it, and p relative to base.
// Both are cleaned first. It compares strings instead of calling
// filepath.Rel, so a different drive on Windows is simply outside.
func Within(base, p string) (string, bool) {
	return within(filepath.Clean(base), filepath.Clean(p), string(filepath.Separator), FoldCase)
}

// SamePath reports whether a and b name the same path.
func SamePath(a, b string) bool {
	rel, ok := Within(a, b)
	return ok && rel == "."
}

func within(base, p, sep string, fold bool) (string, bool) {
	eq := func(a, b string) bool {
		if fold {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	if eq(p, base) {
		return ".", true
	}
	prefix := base
	if !strings.HasSuffix(prefix, sep) {
		prefix += sep
	}
	if len(p) > len(prefix) && eq(p[:len(prefix)], prefix) {
		return p[len(prefix):], true
	}
	return "", false
}
