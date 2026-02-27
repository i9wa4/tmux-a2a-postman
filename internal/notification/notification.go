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
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
)

// BuildNotification builds a notification message using notification_template.
// Variables available: from_node, node, timestamp, filename, inbox_path,
// talks_to_line, template, reply_command, context_id.
// recipient and sender are simple node names (not session-prefixed).
// sourceSessionName is the session name where the message originated.
func BuildNotification(cfg *config.Config, adjacency map[string][]string, nodes map[string]discovery.NodeInfo, contextID, recipient, sender, sourceSessionName, filename string, pongActiveNodes map[string]bool) string {
	return envelope.BuildEnvelope(cfg, cfg.NotificationTemplate, recipient, sender, contextID, "", filename, nil, adjacency, nodes, sourceSessionName, pongActiveNodes)
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
// enterCount controls how many C-m keystrokes to send; 0 or 1 sends one, N>=2 sends N total.
func SendToPane(paneID string, message string, enterDelay time.Duration, tmuxTimeout time.Duration, enterCount int) error {
	// Wrap with protocol sentinels so all pane output is clearly delimited.
	message = "<!-- message start -->\n" + message + "\n<!-- end of message -->"
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

	// 4. Send C-m to submit. C-m (carriage return) submits reliably in both Codex CLI and claude-chill.
	// "Enter" key name adds a newline in Codex CLI multi-line readline instead of submitting (#126).
	cmd = exec.Command("tmux", "send-keys", "-t", paneID, "C-m")
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "⚠️  postman: WARNING: failed to send C-m to pane %s: %v\n", paneID, err)
		return err
	}

	// 5. Send additional C-m keystrokes up to enterCount total
	for i := 1; i < enterCount; i++ {
		time.Sleep(enterDelay)
		cmd = exec.Command("tmux", "send-keys", "-t", paneID, "C-m")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to send C-m %d to pane %s: %w", i+1, paneID, err)
		}
	}

	return nil
}

// ResolveEnterCount returns the effective enter count for pane delivery.
// When configured == 0, probes runtime automatically via probeRuntime.
// probeRuntime returns the running command name for the pane.
func ResolveEnterCount(configured int, probeRuntime func() (string, error)) int {
	if configured == 0 {
		runtime, err := probeRuntime()
		if err == nil && runtime == "codex" {
			return 2
		}
		return 1
	} else if configured > 1 {
		runtime, err := probeRuntime()
		if err != nil || runtime != "codex" {
			return 1
		}
	}
	return configured
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
