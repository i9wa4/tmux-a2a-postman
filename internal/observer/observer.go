package observer

import (
	"os/exec"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// ClassifyMessageType determines the type of a message for digest filtering (Issue #72).
// NOTE: Only messages that pass through post/ reach SendObserverDigest.
// idle reminder and compaction notification are written directly to inbox/
// and never reach this function.
func ClassifyMessageType(filename, sender, recipient string) string {
	// Precedence:
	// 1. recipient=="postman" -> "pong"
	// 2. sender=="postman" -> "system" (currently only PING uses post/)
	// 3. default -> "message"
	if recipient == "postman" {
		return "pong"
	}
	if sender == "postman" {
		return "system"
	}
	return "message"
}

// SendObserverDigest sends digest notification to observers with matching observes config.
// Loop prevention: skip if sender starts with "observer".
// Duplicate prevention: track digested files in digestedFiles map.
// Issue #32: Skip postman-to-postman messages (system internal messages).
// Issue #62: Filter by observes config instead of subscribe_digest.
func SendObserverDigest(filename string, sender string, recipient string, nodes map[string]discovery.NodeInfo, cfg *config.Config, digestedFiles map[string]bool) {
	// Build reverse lookup map: simple name -> NodeInfo
	simpleNameToNode := make(map[string]discovery.NodeInfo)
	for fullKey, info := range nodes {
		if parts := strings.SplitN(fullKey, ":", 2); len(parts) == 2 {
			simpleNameToNode[parts[1]] = info
		}
	}

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

		// Issue #72: Check digest exclude types
		msgType := ClassifyMessageType(filename, sender, recipient)
		if len(nodeConfig.DigestExcludeTypes) > 0 {
			excluded := false
			for _, excludeType := range nodeConfig.DigestExcludeTypes {
				if excludeType == msgType {
					excluded = true
					break
				}
			}
			if excluded {
				continue
			}
		}

		nodeInfo, found := simpleNameToNode[nodeName]
		if !found {
			continue
		}

		// Build digest message
		// Issue #82: Use configurable template for digest item format
		digestItemTemplate := cfg.DigestItemFormat
		if digestItemTemplate == "" {
			digestItemTemplate = "- Message: {filename}\n  From: {sender}"
		}
		timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
		itemVars := map[string]string{
			"filename": filename,
			"sender":   sender,
		}
		digestItem := template.ExpandTemplate(digestItemTemplate, itemVars, timeout)

		vars := map[string]string{
			"sender":       sender,
			"filename":     filename,
			"digest_items": digestItem,
		}
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
