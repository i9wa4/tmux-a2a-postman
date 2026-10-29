//go:build darwin

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
		pid, socketErr = unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	})
	if err != nil {
		return 0, err
	}
	return pid, socketErr
}
