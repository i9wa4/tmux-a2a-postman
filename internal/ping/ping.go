package ping

import (
	"fmt"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
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

// SendPingToNode sends a PING message to a specific node.
// nodeName should be the full session-prefixed name (session:node).
func SendPingToNode(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) error {
	_, err := SendPingToNodeWithResult(nodeInfo, contextID, nodeName, tmpl, cfg, activeNodes, livenessMap, adjacency, nodes)
	return err
}

func SendPingToNodeWithResult(nodeInfo discovery.NodeInfo, contextID, nodeName, tmpl string, cfg *config.Config, activeNodes []string, livenessMap map[string]bool, adjacency map[string][]string, nodes map[string]discovery.NodeInfo) (controlplane.SystemMessageResult, error) {
	target := controlplane.TargetForNode(nodeName, nodeInfo)
	simpleName := target.ActorID
	sourceSessionName := target.SessionName

	now := time.Now()
	ts := now.Format("20060102-150405")

	// Use simple name in filename (Issue #33: keep filenames simple)
	filename, err := message.GenerateFilename(ts, "postman", simpleName, sourceSessionName)
	if err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("generating filename: %w", err)
	}
	postPath := target.PostPath(filename)

	content := envelope.BuildEnvelope(cfg, tmpl, simpleName, "postman", contextID, postPath, activeNodes, adjacency, nodes, sourceSessionName, livenessMap)

	// Pass 2: inject daemon message variables.
	roleContent := envelope.BuildRoleContent(cfg, simpleName)
	content = template.ExpandVariables(content, map[string]string{
		"message_type": "ping",
		"heading":      "Ping",
		"message":      "PING from postman daemon. Do NOT reply to this message.",
		"role_content": roleContent,
	})

	return message.DeliverSystemMessageDirectResultToTarget(filename, target, "postman", contextID, content, cfg, adjacency, nodes, livenessMap)
}
