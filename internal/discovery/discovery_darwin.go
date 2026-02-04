//go:build darwin

package discovery

import (
	"os/exec"
	"strings"
)

// getNodeFromProcessOS extracts A2A_NODE from a process environment on macOS.
// Uses "ps eww" to read the full command line with environment variables.
// Also checks child processes if direct process doesn't have A2A_NODE.
func getNodeFromProcessOS(pid string) string {
	// First check direct process
	if node := checkProcessForNode(pid); node != "" {
		return node
	}

	// Check child processes
	out, err := exec.Command("pgrep", "-P", pid).CombinedOutput()
	if err != nil {
		return ""
	}

	for _, childPid := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if childPid == "" {
			continue
		}
		if node := checkProcessForNode(childPid); node != "" {
			return node
		}
	}
	return ""
}

// checkProcessForNode checks a specific process for A2A_NODE environment variable.
func checkProcessForNode(pid string) string {
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

