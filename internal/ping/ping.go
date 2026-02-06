package ping

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
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

// BuildPingMessage constructs a PING message using the template.
func BuildPingMessage(tmpl string, vars map[string]string, timeout time.Duration) string {
	return template.ExpandTemplate(tmpl, vars, timeout)
}

// SendPingToNode sends a PING message to a specific node.
// nodeName should be the full session-prefixed name (session:node).
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string) error {
	// Extract simple name for filename and config lookups (Issue #33)
	simpleName := ExtractSimpleName(nodeName)

	// Get node config for template (use simple name)
	nodeConfig, hasNodeConfig := cfg.Nodes[simpleName]
	nodeTemplate := ""
	if hasNodeConfig {
		nodeTemplate = nodeConfig.Template
	}

	// Build talks_to_line from adjacency (edges) - use simple name
	talksToLine := buildTalksToLine(simpleName, cfg)

	// Build reply command (use simple name for backward compatibility)
	replyCmd := strings.ReplaceAll(cfg.ReplyCommand, "{node}", simpleName)
	replyCmd = strings.ReplaceAll(replyCmd, "{context_id}", contextID)

	now := time.Now()
	ts := now.Format("20060102-150405")

	vars := map[string]string{
		"context_id":    contextID,
		"node":          simpleName, // Use simple name in template vars
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

	// Use simple name in filename (Issue #33: keep filenames simple)
	filename := fmt.Sprintf("%s-from-postman-to-%s.md", ts, simpleName)
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
	log.Println("📮 postman: SendPingToAll starting...")

	nodes, err := discovery.DiscoverNodes(baseDir, contextID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ postman: discovery failed: %v\n", err)
		return
	}
	log.Printf("📮 postman: discovered %d nodes for PING\n", len(nodes))

	// Build active nodes list (use simple names for display - Issue #33)
	activeNodes := make([]string, 0, len(nodes))
	for nodeName := range nodes {
		simpleName := ExtractSimpleName(nodeName)
		activeNodes = append(activeNodes, simpleName)
	}

	// Send PING to each node using their actual SessionDir
	for nodeName, nodeInfo := range nodes {
		if err := SendPingToNode(nodeInfo, contextID, nodeName, cfg.PingTemplate, cfg, activeNodes); err != nil {
			log.Printf("❌ postman: PING to %s failed: %v\n", nodeName, err)
		} else {
			// Issue #36: Use log package (outputs to file in TUI mode, stderr in --no-tui mode)
			log.Printf("📮 postman: PING sent to %s\n", nodeName)
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
