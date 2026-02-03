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

// SendObserverDigest sends digest notification to observers with subscribe_digest=true.
// Loop prevention: skip if sender starts with "observer".
// Duplicate prevention: track digested files in digestedFiles map.
func SendObserverDigest(filename string, sender string, nodes map[string]discovery.NodeInfo, cfg *config.Config, digestedFiles map[string]bool) {
	// Loop prevention: skip observer messages
	if strings.HasPrefix(sender, "observer") {
		return
	}

	// Duplicate prevention: skip if already digested
	if digestedFiles[filename] {
		return
	}
	digestedFiles[filename] = true

	// Find nodes with subscribe_digest=true
	for nodeName, nodeConfig := range cfg.Nodes {
		if !nodeConfig.SubscribeDigest {
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
