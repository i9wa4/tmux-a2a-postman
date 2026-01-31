//go:build linux

package main

import (
	"os"
	"strings"
)

// getNodeFromProcessOS extracts A2A_NODE from a process environment on Linux.
// Reads /proc/<pid>/environ which contains null-separated environment variables.
func getNodeFromProcessOS(pid string) string {
	data, err := os.ReadFile("/proc/" + pid + "/environ")
	if err != nil {
		return ""
	}
	for _, entry := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(entry, "A2A_NODE=") {
			return strings.TrimPrefix(entry, "A2A_NODE=")
		}
	}
	return ""
}
