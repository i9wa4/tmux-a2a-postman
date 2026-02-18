package discovery

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// NodeInfo holds information about a discovered node.
type NodeInfo struct {
	PaneID      string
	SessionName string
	SessionDir  string
}

// DiscoverNodes scans tmux panes and returns a map of node name -> NodeInfo.
// Only panes that have a non-empty pane title are included.
// Server-wide discovery: scans all sessions (-a flag).
// SessionDir is calculated as baseDir/contextID/sessionName.
func DiscoverNodes(baseDir, contextID string) (map[string]NodeInfo, error) {
	// Format: pane_id session_name pane_title (title last to allow spaces)
	out, err := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id} #{session_name} #{pane_title}").CombinedOutput()
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
		paneID, sessionName, paneTitle := parts[0], parts[1], parts[2]

		// Skip panes with no title
		if paneTitle == "" {
			continue
		}

		// Calculate SessionDir as baseDir/contextID/sessionName
		sessionDir := filepath.Join(baseDir, contextID, sessionName)
		// Use session-prefixed node name to avoid collisions (Issue #33)
		// Format: session_name:node_name
		nodeKey := sessionName + ":" + paneTitle
		nodes[nodeKey] = NodeInfo{
			PaneID:      paneID,
			SessionName: sessionName,
			SessionDir:  sessionDir,
		}
	}
	return nodes, nil
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
