package discovery

import (
	"fmt"
	"os/exec"
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
func DiscoverNodes(baseDir string) (map[string]NodeInfo, error) {
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

		if node := getNodeFromProcessOS(pid); node != "" {
			// NOTE: Discovery only finds pane location (PaneID, SessionName).
			// SessionDir is NOT set here - caller provides contextID from postman's --context-id.
			nodes[node] = NodeInfo{
				PaneID:      paneID,
				SessionName: sessionName,
				SessionDir:  "", // Caller sets this using postman's context ID
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

