package heartbeat

import (
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// SendHeartbeatTrigger writes a heartbeat trigger to post/ for the configured LLM node.
// Single-slot semantics: checks inbox before writing; recycles stale triggers to dead-letter/.
// Returns error on failure; caller sleeps until next interval.
// Uses DaemonMessageTemplate with two-pass expansion (BuildEnvelope + Pass 2).
// No-op when DaemonMessageTemplate is empty.
func SendHeartbeatTrigger(
	sharedNodes *atomic.Pointer[map[string]discovery.NodeInfo],
	contextID, llmNode, prompt string,
	intervalSeconds float64,
	cfg *config.Config,
	adjacency map[string][]string,
) error {
	nodes := sharedNodes.Load()
	if nodes == nil {
		return nil
	}
	nodeInfo, ok := (*nodes)[llmNode]
	if !ok {
		log.Printf("heartbeat: llm_node %q not found in active nodes; skipping", llmNode)
		return nil
	}
	target := controlplane.TargetForNode(llmNode, nodeInfo)

	now := time.Now()
	ttl := time.Duration(intervalSeconds*2) * time.Second

	// Write trigger to post/
	ts := now.Format("20060102-150405")
	filename, err := message.GenerateFilename(ts, "postman", target.ActorID, target.SessionName)
	if err != nil {
		return fmt.Errorf("heartbeat: generating filename: %w", err)
	}
	filePath := target.PostPath(filename)

	expandedPrompt := strings.ReplaceAll(prompt, "{context_id}", contextID)

	tmpl := cfg.DaemonMessageTemplate
	if tmpl == "" {
		// No template configured: heartbeat send is a no-op.
		return nil
	}

	activeNodes := *sharedNodes.Load()
	sourceSessionName := target.SessionName
	scaffolded := envelope.BuildEnvelope(
		cfg, tmpl, target.ActorID, "postman",
		contextID, filePath,
		nil, adjacency, activeNodes, sourceSessionName,
		nil, // livenessMap = nil → static adjacency
	)
	content := template.ExpandVariables(scaffolded, map[string]string{
		"message_type": "heartbeat",
		"heading":      "Heartbeat",
		"role_content": envelope.BuildRoleContent(cfg, target.ActorID),
		"message":      expandedPrompt,
	})

	adapter, err := controlplane.DefaultHandAdapter(target)
	if err != nil {
		return fmt.Errorf("heartbeat: selecting hand adapter: %w", err)
	}
	if _, err := adapter.WriteHeartbeatTrigger(target, controlplane.HeartbeatTrigger{
		Filename: filename,
		Content:  content,
		TTL:      ttl,
	}); err != nil {
		log.Printf("heartbeat: %v", err)
		return fmt.Errorf("heartbeat: %w", err)
	}
	return nil
}
