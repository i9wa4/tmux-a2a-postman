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
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
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
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, pongActiveNodes map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, sourceSessionName string) error {
	// Extract simple name for filename and config lookups (Issue #33)
	simpleName := ExtractSimpleName(nodeName)

	now := time.Now()
	ts := now.Format("20060102-150405")
	taskID := ts + "-ping"
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

// SendPingToAll sends PING messages to all discovered nodes.
func SendPingToAll(baseDir, contextID string, cfg *config.Config, idleTracker *idle.IdleTracker) {
	log.Println("📮 postman: SendPingToAll starting...")

	nodes, _, err := discovery.DiscoverNodesWithCollisions(baseDir, contextID)
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

	// Issue #84: Get PONG-active nodes for talks_to_line filtering
	pongActiveNodes := idleTracker.GetPongActiveNodes()

	// Compute adjacency from config edges
	adjacency, err := config.ParseEdges(cfg.Edges)
	if err != nil {
		adjacency = map[string][]string{}
	}

	// Extract source session name from any discovered node
	sourceSessionName := ""
	for k := range nodes {
		parts := strings.SplitN(k, ":", 2)
		if len(parts) == 2 {
			sourceSessionName = parts[0]
			break
		}
	}

	// Send PING to each node using their actual SessionDir
	for nodeName, nodeInfo := range nodes {
		if err := SendPingToNode(nodeInfo, contextID, nodeName, cfg.MessageTemplate, cfg, activeNodes, pongActiveNodes, adjacency, nodes, sourceSessionName); err != nil {
			log.Printf("❌ postman: PING to %s failed: %v\n", nodeName, err)
		} else {
			// Issue #36: Use log package (outputs to file in TUI mode, stderr in --no-tui mode)
			log.Printf("📮 postman: PING sent to %s\n", nodeName)
		}
	}
}
