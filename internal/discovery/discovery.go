package discovery

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
			// Resolve sessionDir: tmux session name → contextId
			// Check for A2A_CONTEXT_ID in process env, or use session name as contextId
			contextID := getContextIDFromProcess(pid)
			if contextID == "" {
				// Fallback: check if current-context-{sessionName} file exists
				contextFile := filepath.Join(baseDir, fmt.Sprintf("current-context-%s", sessionName))
				if data, err := os.ReadFile(contextFile); err == nil {
					contextID = strings.TrimSpace(string(data))
				}
			}
			if contextID == "" {
				// Final fallback: use session name as contextId
				contextID = sessionName
			}

			// Security: Validate context ID
			if err := validateContextID(contextID); err != nil {
				// Skip invalid context ID (don't fail entire discovery)
				continue
			}

			sessionDir := filepath.Join(baseDir, contextID)

			// Security: Validate session directory (prevent path traversal)
			if err := validateSessionDir(baseDir, sessionDir); err != nil {
				// Skip invalid session directory (don't fail entire discovery)
				continue
			}

			nodes[node] = NodeInfo{
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

// validateContextID validates that the context ID contains only safe characters.
// Safe characters: alphanumeric, hyphen, underscore.
func validateContextID(contextID string) error {
	pattern := regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	if !pattern.MatchString(contextID) {
		return fmt.Errorf("invalid contextID: must be alphanumeric, hyphen, underscore only")
	}
	return nil
}

// validateSessionDir validates that sessionDir does not escape baseDir.
// Prevents path traversal attacks.
func validateSessionDir(baseDir, sessionDir string) error {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return fmt.Errorf("resolving base directory: %w", err)
	}
	absSession, err := filepath.Abs(sessionDir)
	if err != nil {
		return fmt.Errorf("resolving session directory: %w", err)
	}
	if !strings.HasPrefix(absSession, absBase) {
		return fmt.Errorf("path traversal detected: session directory not under base directory")
	}
	return nil
}
