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

// BuildNotification builds a notification message using notification_template.
// Variables available: from_node, node, timestamp, filename, inbox_path,
// talks_to_line, template, reply_command, context_id.
func BuildNotification(cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, contextID, recipient, sender, filename string) string {
	// Get recipient's template
	recipientTemplate := ""
	if nodeConfig, ok := cfg.Nodes[recipient]; ok {
		recipientTemplate = nodeConfig.Template
	}

	// Get talks_to list for recipient
	talksTo := config.GetTalksTo(adjacency, recipient)
	// Filter to only active nodes
	activeTalksTo := []string{}
	for _, node := range talksTo {
		if _, found := nodes[node]; found {
			activeTalksTo = append(activeTalksTo, node)
		}
	}

	// Format talks_to line
	talksToLine := ""
	if len(activeTalksTo) > 0 {
		talksToLine = fmt.Sprintf("Can talk to: %s", strings.Join(activeTalksTo, ", "))
	}

	// Build inbox path using recipient's actual session directory
	var sessionDir string
	if recipientInfo, found := nodes[recipient]; found {
		// Use recipient's actual SessionDir from discovery
		sessionDir = recipientInfo.SessionDir
	} else {
		// Fallback: calculate from filename
		// filename is in post/ directory: /path/to/session-xxx/post/message.md -> /path/to/session-xxx
		sessionDir = filepath.Dir(filepath.Dir(filename))
	}
	inboxPath := filepath.Join(sessionDir, "inbox", recipient)

	// Build variables map
	vars := map[string]string{
		"from_node":      sender,
		"node":           recipient,
		"timestamp":      extractTimestamp(filename),
		"filename":       filepath.Base(filename),
		"inbox_path":     inboxPath,
		"talks_to_line":  talksToLine,
		"template":       recipientTemplate,
		"reply_command":  cfg.ReplyCommand,
		"context_id":     contextID,
		"session_dir":    sessionDir,
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	return template.ExpandTemplate(cfg.NotificationTemplate, vars, timeout)
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
func SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration) error {
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

	// 4. Send Enter key
	cmd = exec.Command("tmux", "send-keys", "-t", paneID, "Enter")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to send Enter to pane %s: %v\n", paneID, err)
		return err
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
