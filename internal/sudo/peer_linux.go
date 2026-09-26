package sudo

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const peerCheckAvailable = true

// peerIsSudo checks that the socket caller was started by sudo, so another
// process of the same user cannot redeem a token it happened to learn. It
// returns the pid of that sudo process.
func peerIsSudo(c net.Conn) (int, error) {
	pid, err := peerPID(c)
	if err != nil {
		return 0, err
	}
	return sudoParent("/proc", pid, 0)
}

// peerPID returns the process id of the socket's other end.
func peerPID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credErr != nil {
		return 0, credErr
	}
	return int(cred.Pid), nil
}

// sudoParent returns the parent of pid when that parent is sudo, read from
// the proc file system at proc.
func sudoParent(proc string, pid, wantEUID int) (int, error) {
	stat, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	// Fields after the command name, which is in parentheses: state ppid ...
	rest := string(stat[strings.LastIndexByte(string(stat), ')')+1:])
	fields := strings.Fields(rest)
	if len(fields) < 2 {
		return 0, errors.New("unexpected /proc stat")
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, err
	}
	comm, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(ppid), "comm"))
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(string(comm)) != "sudo" {
		return 0, errors.New("caller is not started by sudo")
	}
	// Any program of the user can call itself sudo. Real sudo is setuid
	// root, so it runs with effective uid 0 while it asks for the password.
	status, err := os.ReadFile(filepath.Join(proc, strconv.Itoa(ppid), "status"))
	if err != nil {
		return 0, err
	}
	euid := -1
	for _, line := range strings.Split(string(status), "\n") {
		if v, ok := strings.CutPrefix(line, "Uid:"); ok {
			// Uid: real effective saved filesystem
			if f := strings.Fields(v); len(f) >= 2 {
				euid, _ = strconv.Atoi(f[1])
			}
			break
		}
	}
	if euid < 0 || euid != wantEUID {
		return 0, fmt.Errorf("caller's parent sudo runs as uid %d, not %d", euid, wantEUID)
	}
	return ppid, nil
}
