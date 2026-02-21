package notification

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// resolveNodeNameForNotification resolves a simple node name to a session-prefixed node name.
// Similar to message.ResolveNodeName but localized for notification package.
func resolveNodeNameForNotification(nodeName, sourceSessionName string, knownNodes map[string]discovery.NodeInfo) string {
	// If already prefixed (contains ":"), use as-is
	if strings.Contains(nodeName, ":") {
		if _, found := knownNodes[nodeName]; found {
			return nodeName
		}
		return "" // Prefixed but not found
	}

	// Try same-session first (priority)
	sameSessionKey := sourceSessionName + ":" + nodeName
	if _, found := knownNodes[sameSessionKey]; found {
		return sameSessionKey
	}

	// Try any other session
	for fullName := range knownNodes {
		// Extract node name from "session:node" format
		parts := strings.SplitN(fullName, ":", 2)
		if len(parts) == 2 && parts[1] == nodeName {
			return fullName
		}
	}

	return "" // Not found
}

// BuildNotification builds a notification message using notification_template.
// Variables available: from_node, node, timestamp, filename, inbox_path,
// talks_to_line, template, reply_command, context_id.
// recipient and sender are simple node names (not session-prefixed).
// sourceSessionName is the session name where the message originated.
func BuildNotification(cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, contextID, recipient, sender, sourceSessionName, filename string, pongActiveNodes map[string]bool) string {
	// Get recipient's template (use simple name for config lookup)
	recipientTemplate := ""
	if nodeConfig, ok := cfg.Nodes[recipient]; ok {
		recipientTemplate = nodeConfig.Template
	}
	// Issue #49: Prepend common_template if present
	if cfg.CommonTemplate != "" {
		if recipientTemplate != "" {
			recipientTemplate = cfg.CommonTemplate + "\n\n" + recipientTemplate
		} else {
			recipientTemplate = cfg.CommonTemplate
		}
	}

	// Get talks_to list for recipient (use simple name for adjacency lookup)
	talksTo := config.GetTalksTo(adjacency, recipient)
	// Issue #84: Filter to only PONG-active nodes
	activeTalksTo := []string{}
	for _, node := range talksTo {
		nodeFullName := resolveNodeNameForNotification(node, sourceSessionName, nodes)
		// Issue #84: Filter by PONG-active status (resolveNodeNameForNotification success + PONG received)
		if nodeFullName != "" && pongActiveNodes[nodeFullName] {
			activeTalksTo = append(activeTalksTo, node)
		}
	}

	// Format talks_to line
	talksToLine := ""
	if len(activeTalksTo) > 0 {
		talksToLine = fmt.Sprintf("Can talk to: %s", strings.Join(activeTalksTo, ", "))
	}

	// Resolve recipient name to full name for SessionDir lookup (Issue #33)
	recipientFullName := resolveNodeNameForNotification(recipient, sourceSessionName, nodes)

	// Build inbox path using recipient's actual session directory
	var sessionDir string
	if recipientFullName != "" {
		if recipientInfo, found := nodes[recipientFullName]; found {
			// Use recipient's actual SessionDir from discovery
			sessionDir = recipientInfo.SessionDir
		}
	}
	if sessionDir == "" {
		// Fallback: calculate from filename
		// filename is in post/ directory: /path/to/session-xxx/post/message.md -> /path/to/session-xxx
		sessionDir = filepath.Dir(filepath.Dir(filename))
	}
	inboxPath := filepath.Join(sessionDir, "inbox", recipient)

	// Issue #39: Embed --context-id in reply_command
	replyCmd := cfg.ReplyCommand
	if strings.Contains(replyCmd, "create-draft") && !strings.Contains(replyCmd, "--context-id") {
		// Insert --context-id before --to if present
		if strings.Contains(replyCmd, "--to") {
			replyCmd = strings.Replace(replyCmd, "--to", fmt.Sprintf("--context-id %s --to", contextID), 1)
		} else {
			// Append at the end if --to is not present
			replyCmd = fmt.Sprintf("%s --context-id %s", replyCmd, contextID)
		}
	}

	// Inject --from <recipient> so create-draft correctly identifies sender
	if strings.Contains(replyCmd, "create-draft") && !strings.Contains(replyCmd, "--from") {
		if strings.Contains(replyCmd, "--to") {
			replyCmd = strings.Replace(replyCmd, "--to",
				fmt.Sprintf("--from %s --to", recipient), 1)
		} else {
			replyCmd = fmt.Sprintf("%s --from %s", replyCmd, recipient)
		}
	}

	// CRITICAL FIX: Expand {context_id} placeholder in reply_command template
	// This handles cases where reply_command contains {context_id} literally in config
	replyCmd = strings.ReplaceAll(replyCmd, "{context_id}", contextID)

	// Build variables map
	vars := map[string]string{
		"from_node":     sender,
		"node":          recipient,
		"timestamp":     extractTimestamp(filename),
		"filename":      filepath.Base(filename),
		"inbox_path":    inboxPath,
		"talks_to_line": talksToLine,
		"template":      recipientTemplate,
		"reply_command": replyCmd,
		"context_id":    contextID,
		"session_dir":   sessionDir,
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	// Issue #83: Use ping_template for PING messages from postman
	notifTemplate := cfg.NotificationTemplate
	if sender == "postman" {
		notifTemplate = cfg.PingTemplate
	}
	return template.ExpandTemplate(notifTemplate, vars, timeout)
}

// extractTimestamp extracts timestamp from filename.
// Format: YYYYMMDD-HHMMSS-from-...
func extractTimestamp(filename string) string {
	base := filepath.Base(filename)
	parts := strings.SplitN(base, "-", 3)
	if len(parts) >= 2 {
		return parts[0] + "-" + parts[1]
	}
	return ""
}

// SendToPane sends a message to a tmux pane using set-buffer + paste-buffer.
// Security: Sanitizes message before passing to tmux set-buffer.
// Error handling: Logs errors but does not fail (graceful degradation).
// enterCount controls how many Enter (C-m) keystrokes to send; 0 or 1 sends one, 2+ sends two.
func SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int) error {
	// Security: Sanitize message for tmux set-buffer
	sanitized := sanitizeForTmux(message)

	// 1. Set buffer
	cmd := exec.Command("tmux", "set-buffer", sanitized)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to set buffer for pane %s: %v\n", paneID, err)
		return err
	}

	// 2. Paste buffer to target pane
	cmd = exec.Command("tmux", "paste-buffer", "-t", paneID)
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to paste buffer to pane %s: %v\n", paneID, err)
		return err
	}

	// 3. Wait enter_delay
	time.Sleep(enterDelay)

	// 4. Send carriage return (C-m) to submit in Codex CLI multi-line readline mode.
	// "Enter" key name adds a newline in multi-line mode instead of submitting (#126).
	cmd = exec.Command("tmux", "send-keys", "-t", paneID, "C-m")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to send Enter to pane %s: %v\n", paneID, err)
		return err
	}

	// 5. If enterCount >= 2, send a second C-m after enterDelay (#126).
	if enterCount >= 2 {
		time.Sleep(enterDelay)
		cmd = exec.Command("tmux", "send-keys", "-t", paneID, "C-m")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to send second Enter: %w", err)
		}
	}

	return nil
}

// sanitizeForTmux sanitizes a string for safe use with tmux set-buffer.
// Escapes special shell characters to prevent command injection.
func sanitizeForTmux(s string) string {
	// NOTE: tmux set-buffer does not interpret shell metacharacters,
	// but we sanitize as a defense-in-depth measure.
	// Escape backslashes and quotes
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "$", "\\$")
	s = strings.ReplaceAll(s, "`", "\\`")
	return s
}
