//go:build linux

package discovery

import (
	"os"
	"os/exec"
	"strings"
)

// getNodeFromProcessOS extracts A2A_NODE from a process environment on Linux.
// Issue #48: First tries tmux show-environment (detects late-exported vars),
// then falls back to /proc/<pid>/environ for startup-only env vars.
// Also checks child processes if direct process doesn't have A2A_NODE.
func getNodeFromProcessOS(pid string, paneID string) string {
	// Method 1: Try tmux show-environment (Issue #48)
	if node := getNodeFromTmux(paneID); node != "" {
		return node
	}

	// Method 2: Fallback to /proc/<pid>/environ (startup env only)
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

// getNodeFromTmux extracts A2A_NODE from tmux pane environment (Issue #48).
// This method detects variables exported after process startup.
func getNodeFromTmux(paneID string) string {
	out, err := exec.Command("tmux", "show-environment", "-t", paneID, "A2A_NODE").CombinedOutput()
	if err != nil {
		return ""
	}
	// Parse output: "A2A_NODE=worker" or "-A2A_NODE" (unset)
	line := strings.TrimSpace(string(out))
	if strings.HasPrefix(line, "A2A_NODE=") {
		return strings.TrimPrefix(line, "A2A_NODE=")
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
