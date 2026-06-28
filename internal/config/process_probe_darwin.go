//go:build darwin

package config

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

func platformProcessCommand(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(info.Proc.P_comm[:]), "\x00"), nil
}

func platformProcessStartedAt(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%06d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}
