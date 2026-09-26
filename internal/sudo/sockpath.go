package sudo

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// SocketPath is where the askpass socket lives for node folder home. Unix
// socket paths are limited to 104 bytes on macOS (108 on Linux), so a long
// home falls back to a short name in the temp folder.
func SocketPath(home string) string {
	p := filepath.Join(home, "askpass.sock")
	if len(p) < 100 {
		return p
	}
	sum := sha256.Sum256([]byte(home))
	return filepath.Join(os.TempDir(), "tgsync-"+hex.EncodeToString(sum[:4])+".sock")
}
