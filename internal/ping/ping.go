package ping

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// BuildPingMessage constructs a PING message using the template.
func BuildPingMessage(tmpl string, vars map[string]string, timeout time.Duration) string {
	return template.ExpandTemplate(tmpl, vars, timeout)
}

// SendPingToNode sends a PING message to a specific node.
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config) error {
	vars := map[string]string{
		"context_id": contextID,
		"node":       nodeName,
	}
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content := BuildPingMessage(tmpl, vars, timeout)

	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s.md", ts, nodeName)
	postPath := filepath.Join(nodeInfo.SessionDir, "post", filename)

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

// SendPingToAll sends PING messages to all discovered nodes.
func SendPingToAll(baseDir, contextID string, cfg *config.Config) {
	nodes, err := discovery.DiscoverNodes(baseDir)
	if err != nil {
		_ = err // Suppress unused variable warning
		return
	}

	for nodeName, nodeInfo := range nodes {
		if err := SendPingToNode(nodeInfo, contextID, nodeName, cfg.PingTemplate, cfg); err != nil {
			_ = err // Suppress unused variable warning
		}
	}
}
