//go:build linux

package workspace

import (
	"net"

	"golang.org/x/sys/unix"
)

func unixPeerPID(conn net.Conn) (int, error) {
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return 0, err
	}
	var pid int
	var socketErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr := unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
		socketErr = credErr
		if credErr == nil {
			pid = int(cred.Pid)
		}
	})
	if err != nil {
		return 0, err
	}
	return pid, socketErr
}
