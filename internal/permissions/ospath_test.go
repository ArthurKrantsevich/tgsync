package permissions

import (
	"path/filepath"
	"runtime"
)

// osPath turns a Unix-style absolute test path into an absolute path on
// this OS: /etc/hosts stays as is on Unix and becomes C:\etc\hosts on Windows.
func osPath(p string) string {
	if runtime.GOOS == "windows" {
		return `C:` + filepath.FromSlash(p)
	}
	return p
}
