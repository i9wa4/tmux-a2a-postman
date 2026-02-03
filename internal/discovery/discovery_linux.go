//go:build linux

package discovery

import (
	"os"
	"os/exec"
	"strings"
)

// getNodeFromProcessOS extracts A2A_NODE from a process environment on Linux.
// Reads /proc/<pid>/environ which contains null-separated environment variables.
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

// getContextIDFromProcess extracts A2A_CONTEXT_ID from a process environment on Linux.
func getContextIDFromProcess(pid string) string {
	data, err := os.ReadFile("/proc/" + pid + "/environ")
	if err != nil {
		return ""
	}
	for _, entry := range strings.Split(string(data), "\x00") {
		if strings.HasPrefix(entry, "A2A_CONTEXT_ID=") {
			return strings.TrimPrefix(entry, "A2A_CONTEXT_ID=")
		}
	}
	return ""
}
