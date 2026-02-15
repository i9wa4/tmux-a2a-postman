package discovery

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// NodeInfo holds information about a discovered node.
type NodeInfo struct {
	PaneID      string
	SessionName string
	SessionDir  string
}

// DiscoverNodes scans tmux panes and returns a map of node name -> NodeInfo.
// Only panes that have A2A_NODE env var set are included.
// Server-wide discovery: scans all sessions (-a flag).
// SessionDir is calculated as baseDir/contextID/sessionName.
func DiscoverNodes(baseDir, contextID string) (map[string]NodeInfo, error) {
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_pid} #{pane_id} #{session_name}").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("tmux list-panes: %w: %s", err, out)
	}

	nodes := make(map[string]NodeInfo)
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " ", 3)
		if len(parts) != 3 {
			continue
		}
		pid, paneID, sessionName := parts[0], parts[1], parts[2]

		// Security: Validate PID
		if err := validatePID(pid); err != nil {
			// Skip invalid PID (don't fail entire discovery)
			continue
		}

		// Issue #48: Pass paneID for tmux-based detection
		if node := getNodeFromProcessOS(pid, paneID); node != "" {
			// Calculate SessionDir as baseDir/contextID/sessionName
			sessionDir := filepath.Join(baseDir, contextID, sessionName)
			// Use session-prefixed node name to avoid collisions (Issue #33)
			// Format: session_name:node_name
			nodeKey := sessionName + ":" + node
			nodes[nodeKey] = NodeInfo{
				PaneID:      paneID,
				SessionName: sessionName,
				SessionDir:  sessionDir,
			}
		}
	}
	return nodes, nil
}

// validatePID validates that the given PID is a numeric value.
func validatePID(pid string) error {
	if _, err := strconv.Atoi(pid); err != nil {
		return fmt.Errorf("invalid PID: %s", pid)
	}
	return nil
}

// DiscoverAllSessions returns all tmux session names.
// Issue #117: Returns ALL sessions (not just those with A2A nodes).
func DiscoverAllSessions() ([]string, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").CombinedOutput()
	if err != nil {
		// If no server running, return empty list (not an error)
		if strings.Contains(string(out), "no server running") {
			return []string{}, nil
		}
		return nil, fmt.Errorf("tmux list-sessions: %w: %s", err, out)
	}

	sessions := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			sessions = append(sessions, line)
		}
	}
	return sessions, nil
}
