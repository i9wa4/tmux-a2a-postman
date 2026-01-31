//go:build darwin

package main

import (
	"os/exec"
	"strings"
)

// getNodeFromProcessOS extracts A2A_NODE from a process environment on macOS.
// Uses "ps eww" to read the full command line with environment variables.
func getNodeFromProcessOS(pid string) string {
	out, err := exec.Command("ps", "eww", "-o", "command=", "-p", pid).CombinedOutput()
	if err != nil {
		return ""
	}
	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, "A2A_NODE=") {
			return strings.TrimPrefix(field, "A2A_NODE=")
		}
	}
	return ""
}
