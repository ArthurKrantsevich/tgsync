//go:build unix

package files

import "syscall"

// openFlags keep a file sent to Telegram from being a symlink swapped in
// after the check or a FIFO that blocks the open.
const openFlags = syscall.O_NOFOLLOW | syscall.O_NONBLOCK
