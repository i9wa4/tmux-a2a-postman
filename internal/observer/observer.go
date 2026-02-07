package observer

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// SendObserverDigest sends digest notification to observers with matching observes config.
// Loop prevention: skip if sender starts with "observer".
// Duplicate prevention: track digested files in digestedFiles map.
// Issue #32: Skip postman-to-postman messages (system internal messages).
// Issue #62: Filter by observes config instead of subscribe_digest.
func SendObserverDigest(filename string, sender string, recipient string, nodes map[string]discovery.NodeInfo, cfg *config.Config, digestedFiles map[string]bool) {
	// Loop prevention: skip observer messages
	if strings.HasPrefix(sender, "observer") {
		return
	}

	// Issue #32: Skip postman-to-postman messages (system internal)
	if sender == "postman" && recipient == "postman" {
		return
	}

	// Duplicate prevention: skip if already digested
	if digestedFiles[filename] {
		return
	}
	digestedFiles[filename] = true

	// Find nodes with matching observes config
	for nodeName, nodeConfig := range cfg.Nodes {
		if len(nodeConfig.Observes) == 0 {
			continue
		}

		// Check if sender or recipient is in observes list
		if !containsNode(nodeConfig.Observes, sender) && !containsNode(nodeConfig.Observes, recipient) {
			continue
		}

		nodeInfo, found := nodes[nodeName]
		if !found {
			continue
		}

		// Build digest message
		digestItem := fmt.Sprintf("- Message: %s\n  From: %s", filename, sender)
		vars := map[string]string{
			"sender":       sender,
			"filename":     filename,
			"digest_items": digestItem,
		}
		timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
		content := template.ExpandTemplate(cfg.DigestTemplate, vars, timeout)

		// Send directly to pane via tmux send-keys
		if err := exec.Command("tmux", "send-keys", "-t", nodeInfo.PaneID, content, "Enter").Run(); err != nil {
			_ = err // Suppress unused variable warning
		}
	}
}

func containsNode(observes []string, node string) bool {
	for _, n := range observes {
		if n == node {
			return true
		}
	}
	return false
}
