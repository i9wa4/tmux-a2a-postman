package ping

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

// ExtractSimpleName extracts the simple node name from a session-prefixed name.
// If the name contains ":", returns the part after ":". Otherwise, returns the name as-is.
func ExtractSimpleName(fullName string) string {
	parts := strings.SplitN(fullName, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return fullName
}

// SendPingToNode sends a PING message to a specific node.
// nodeName should be the full session-prefixed name (session:node).
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, pongActiveNodes map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	// Extract simple name for filename and config lookups (Issue #33)
	simpleName := ExtractSimpleName(nodeName)

	// Derive sourceSessionName from nodeInfo.SessionName so each target node
	// resolves its own session correctly (fixes multi-session regression).
	sourceSessionName := nodeInfo.SessionName

	now := time.Now()
	ts := now.Format("20060102-150405")
	taskID := ts + "-ping"

	// Use simple name in filename (Issue #33: keep filenames simple)
	filename := fmt.Sprintf("%s-from-postman-to-%s.md", ts, simpleName)
	postPath := filepath.Join(nodeInfo.SessionDir, "post", filename)

	content := envelope.BuildEnvelope(cfg, tmpl, simpleName, "postman", contextID, taskID, postPath, activeNodes, adjacency, nodes, sourceSessionName, pongActiveNodes)

	// Ensure post directory exists for this node's session
	postDir := filepath.Join(nodeInfo.SessionDir, "post")
	if err := os.MkdirAll(postDir, 0o755); err != nil {
		return fmt.Errorf("creating post directory: %w", err)
	}

	if err := os.WriteFile(postPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("writing PING message: %w", err)
	}

	return nil
}
