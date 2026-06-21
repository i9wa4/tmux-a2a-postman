//go:build !darwin

package config

import "fmt"

func platformProcessCommand(pid int) (string, error) {
	return "", fmt.Errorf("platform process command unavailable for pid %d", pid)
}

func platformProcessStartedAt(pid int) (string, error) {
	return "", fmt.Errorf("platform process start time unavailable for pid %d", pid)
}
