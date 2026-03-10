package heartbeat

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// SendHeartbeatTrigger writes a heartbeat trigger to post/ for the configured LLM node.
// Single-slot semantics: checks inbox before writing; recycles stale triggers to dead-letter/.
// Returns error on failure; caller sleeps until next interval.
// When HeartbeatMessageTemplate is configured, uses two-pass expansion (BuildEnvelope + Pass 2).
// Falls back to legacy hardcoded format when HeartbeatMessageTemplate is empty.
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

	// Single-slot guard: check inbox for existing triggers
	inboxDir := filepath.Join(nodeInfo.SessionDir, "inbox", llmNode)
	entries, err := os.ReadDir(inboxDir)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("heartbeat: reading inbox %s: %w", inboxDir, err)
	}

	now := time.Now()
	ttl := time.Duration(intervalSeconds*2) * time.Second
	unread := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		age := now.Sub(info.ModTime())
		filePath := filepath.Join(inboxDir, entry.Name())
		if age > ttl {
			// Stale: recycle to dead-letter/
			deadLetter := filepath.Join(nodeInfo.SessionDir, "dead-letter", entry.Name())
			if err := os.Rename(filePath, deadLetter); err != nil {
				log.Printf("heartbeat: failed to recycle stale trigger %s: %v", filePath, err)
				return fmt.Errorf("heartbeat: recycling stale trigger: %w", err)
			}
		} else {
			unread++
		}
	}

	if unread > 0 {
		// LLM still processing
		return nil
	}

	// Write trigger to post/
	ts := now.Format("20060102-150405")
	taskID := ts + "-hb01"
	filename := message.GenerateFilename(ts, "postman", llmNode, nodeInfo.SessionName)
	postDir := filepath.Join(nodeInfo.SessionDir, "post")
	filePath := filepath.Join(postDir, filename)

	expandedPrompt := strings.ReplaceAll(prompt, "{context_id}", contextID)

	tmpl := cfg.HeartbeatMessageTemplate
	var content string
	if tmpl == "" {
		// Legacy path: hardcoded format
		content = fmt.Sprintf("---\nmethod: message/send\nparams:\n  contextId: %s\n  taskId: %s\n  from: postman\n  to: %s\n  timestamp: %s\n---\n\n## Content\n\n%s\n",
			contextID, taskID, llmNode, now.Format(time.RFC3339), expandedPrompt)
	} else {
		// New path: BuildEnvelope + Pass 2
		nodes := *sharedNodes.Load()
		sourceSessionName := nodeInfo.SessionName
		scaffolded := envelope.BuildEnvelope(
			cfg, tmpl, llmNode, "postman",
			contextID, taskID, filePath,
			nil, adjacency, nodes, sourceSessionName,
			nil, // pongActiveNodes = nil → static adjacency
		)
		content = template.ExpandVariables(scaffolded, map[string]string{
			"role_content": envelope.BuildRoleContent(cfg, llmNode),
			"message":      expandedPrompt,
		})
	}

	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		log.Printf("heartbeat: failed to write trigger %s: %v", filePath, err)
		return fmt.Errorf("heartbeat: writing trigger: %w", err)
	}
	return nil
}
