package ping

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string) error {
	// Get node config for template
	nodeConfig, hasNodeConfig := cfg.Nodes[nodeName]
	nodeTemplate := ""
	if hasNodeConfig {
		nodeTemplate = nodeConfig.Template
	}

	// Build talks_to_line from adjacency (edges)
	talksToLine := buildTalksToLine(nodeName, cfg)

	// Build reply command
	replyCmd := strings.ReplaceAll(cfg.ReplyCommand, "{node}", nodeName)
	replyCmd = strings.ReplaceAll(replyCmd, "{context_id}", contextID)

	now := time.Now()
	ts := now.Format("20060102-150405")

	vars := map[string]string{
		"context_id":    contextID,
		"node":          nodeName,
		"timestamp":     ts,
		"from_node":     "postman",
		"template":      nodeTemplate,
		"talks_to_line": talksToLine,
		"active_nodes":  strings.Join(activeNodes, ", "),
		"reply_command": replyCmd,
		"session_dir":   nodeInfo.SessionDir,
	}
	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	content := BuildPingMessage(tmpl, vars, timeout)

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
	fmt.Println("📮 postman: SendPingToAll starting...")

	nodes, err := discovery.DiscoverNodes(baseDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ postman: discovery failed: %v\n", err)
		return
	}
	fmt.Printf("📮 postman: discovered %d nodes for PING\n", len(nodes))

	// Build active nodes list
	activeNodes := make([]string, 0, len(nodes))
	for nodeName := range nodes {
		activeNodes = append(activeNodes, nodeName)
	}

	// Use postman's own contextID for session directory
	sessionDir := filepath.Join(baseDir, contextID)
	for nodeName, nodeInfo := range nodes {
		// Override nodeInfo.SessionDir with postman's session
		nodeInfo.SessionDir = sessionDir
		if err := SendPingToNode(nodeInfo, contextID, nodeName, cfg.PingTemplate, cfg, activeNodes); err != nil {
			fmt.Fprintf(os.Stderr, "❌ postman: PING to %s failed: %v\n", nodeName, err)
		} else {
			fmt.Printf("📮 postman: PING sent to %s\n", nodeName)
		}
	}
}

// buildTalksToLine builds the "Can talk to:" line from edges config.
func buildTalksToLine(nodeName string, cfg *config.Config) string {
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		return ""
	}
	if neighbors, ok := adjacency[nodeName]; ok && len(neighbors) > 0 {
		return fmt.Sprintf("Can talk to: %s", strings.Join(neighbors, ", "))
	}
	return ""
}
