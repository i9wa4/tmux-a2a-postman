//go:build !darwin && !linux

package workspace

import (
	"errors"
	"net"
)

func unixPeerPID(net.Conn) (int, error) {
	return 0, errors.New("Unix peer PID unsupported on this platform")
}
