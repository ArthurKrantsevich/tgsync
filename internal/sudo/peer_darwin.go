package sudo

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

const peerCheckAvailable = true

// peerIsSudo checks that the socket caller was started by sudo, so another
// process of the same user cannot redeem a token it happened to learn. It
// returns the pid of that sudo process.
func peerIsSudo(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var pidErr error
	if err := raw.Control(func(fd uintptr) {
		pid, pidErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	}); err != nil {
		return 0, err
	}
	if pidErr != nil {
		return 0, pidErr
	}
	caller, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return 0, err
	}
	ppid := int(caller.Eproc.Ppid)
	parent, err := unix.SysctlKinfoProc("kern.proc.pid", ppid)
	if err != nil {
		return 0, err
	}
	if unix.ByteSliceToString(parent.Proc.P_comm[:]) != "sudo" {
		return 0, errors.New("caller is not started by sudo")
	}
	// Any program of the user can call itself sudo. Real sudo is setuid
	// root, so its effective uid is 0 while it asks for the password.
	if parent.Eproc.Ucred.Uid != 0 {
		return 0, errors.New("caller's parent sudo does not run as root")
	}
	return ppid, nil
}
