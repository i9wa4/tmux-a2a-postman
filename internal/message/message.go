package message

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// DaemonEvent represents an event to be sent to the TUI (Issue #53).
type DaemonEvent struct {
	Type    string
	Message string
	Details map[string]interface{}
}

// MessageInfo holds parsed information from a message filename.
type MessageInfo struct {
	Timestamp string
	From      string
	To        string
}

// ResolveNodeName resolves a simple node name to a session-prefixed node name.
// Resolution priority:
// 1. If nodeName already contains ":", use as-is (already prefixed)
// 2. Look for nodeName in the same session as sourceSessionName
// 3. Look for nodeName in any other session
// Returns the resolved node name or empty string if not found.
func ResolveNodeName(nodeName, sourceSessionName string, knownNodes map[string]discovery.NodeInfo) string {
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

// ParseMessageFilename parses a message filename in the format:
// {timestamp}-from-{sender}-to-{recipient}.md
// Example: 20260201-022121-from-orchestrator-to-worker.md
func ParseMessageFilename(filename string) (*MessageInfo, error) {
	// Remove .md extension
	if !strings.HasSuffix(filename, ".md") {
		return nil, fmt.Errorf("invalid filename: missing .md extension: %q", filename)
	}
	base := strings.TrimSuffix(filename, ".md")

	// Find "-from-" and "-to-" markers
	fromIdx := strings.Index(base, "-from-")
	if fromIdx < 0 {
		return nil, fmt.Errorf("invalid filename: missing '-from-' marker: %q", filename)
	}

	rest := base[fromIdx+len("-from-"):]
	toIdx := strings.Index(rest, "-to-")
	if toIdx < 0 {
		return nil, fmt.Errorf("invalid filename: missing '-to-' marker: %q", filename)
	}

	timestamp := base[:fromIdx]
	from := rest[:toIdx]
	to := rest[toIdx+len("-to-"):]

	if timestamp == "" || from == "" || to == "" {
		return nil, fmt.Errorf("invalid filename: empty field in %q", filename)
	}

	return &MessageInfo{
		Timestamp: timestamp,
		From:      from,
		To:        to,
	}, nil
}

// DeliverMessage moves a message from post/ to the recipient's inbox/ or dead-letter/.
// Multi-session support: postPath is the full path to the message file in post/ directory.
// The message will be delivered to the recipient's session directory based on NodeInfo.SessionDir.
// Routing rules (DEFAULT DENY):
// - sender="postman" is always allowed
// - otherwise, sender->recipient edge must exist in adjacency map
// Session check: both sender and recipient sessions must be enabled (unless sender is postman)
// Issue #53: Added events channel parameter for dead-letter notifications
// Issue #71: Added idleTracker parameter for activity tracking
func DeliverMessage(postPath string, contextID string, knownNodes map[string]discovery.NodeInfo, adjacency map[string][]string, cfg *config.Config, isSessionEnabled func(string) bool, events chan<- DaemonEvent, idleTracker *idle.IdleTracker) error {
	// Extract filename from postPath
	filename := filepath.Base(postPath)

	// Extract source session directory from postPath
	// postPath format: /path/to/context-id/session-name/post/message.md
	sourceSessionDir := filepath.Dir(filepath.Dir(postPath))
	sourceSessionName := filepath.Base(sourceSessionDir)

	// Check if file still exists (handles duplicate fsnotify event)
	if _, err := os.Stat(postPath); os.IsNotExist(err) {
		return nil // Already processed
	}

	info, err := ParseMessageFilename(filename)
	if err != nil {
		// Parse error: move to dead-letter/ in source session
		dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
		// Issue #53: Notify dead-letter event
		if events != nil {
			events <- DaemonEvent{
				Type:    "message_received",
				Message: fmt.Sprintf("Dead-letter: %s (parse error)", filename),
			}
		}
		return os.Rename(postPath, dst)
	}

	// PONG handling: messages to "postman" are PONG responses
	// Move directly to read/ in source session (skip inbox delivery)
	// NOTE: PONG state tracking (MarkPongReceived) is handled in daemon.go
	// post-delivery block, not here. This function only moves PONG to read/.
	if info.To == "postman" {
		dst := filepath.Join(sourceSessionDir, "read", filename)
		if err := os.Rename(postPath, dst); err != nil {
			return fmt.Errorf("moving PONG to read: %w", err)
		}
		log.Printf("postman: PONG received from %s\n", info.From)
		return nil
	}

	// Resolve recipient name (Issue #33: session-aware adjacency)
	recipientFullName := ResolveNodeName(info.To, sourceSessionName, knownNodes)
	if recipientFullName == "" {
		// Unknown recipient: move to dead-letter/ in source session
		dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
		// Issue #53: Notify dead-letter event
		if events != nil {
			events <- DaemonEvent{
				Type:    "message_received",
				Message: fmt.Sprintf("Dead-letter: %s -> %s (unknown recipient)", info.From, info.To),
			}
		}
		return os.Rename(postPath, dst)
	}
	nodeInfo := knownNodes[recipientFullName]
	paneID := nodeInfo.PaneID

	// Resolve sender name (Issue #33: session-aware adjacency)
	senderFullName := ResolveNodeName(info.From, sourceSessionName, knownNodes)
	if senderFullName == "" && info.From != "postman" {
		// Unknown sender: move to dead-letter/ in source session
		dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
		// Issue #53: Notify dead-letter event
		if events != nil {
			events <- DaemonEvent{
				Type:    "message_received",
				Message: fmt.Sprintf("Dead-letter: %s -> %s (unknown sender)", info.From, info.To),
			}
		}
		return os.Rename(postPath, dst)
	}

	// Check routing permissions (DEFAULT DENY)
	// IMPORTANT: sender="postman" is always allowed
	if info.From != "postman" {
		allowed := false
		// Try adjacency lookup with both simple name and full name
		// This supports both old-style (simple names) and new-style (session:node) adjacency configs
		for _, senderKey := range []string{info.From, senderFullName} {
			if neighbors, ok := adjacency[senderKey]; ok {
				for _, neighbor := range neighbors {
					// Resolve neighbor name to full name
					neighborFullName := ResolveNodeName(neighbor, sourceSessionName, knownNodes)
					if neighborFullName == recipientFullName {
						allowed = true
						break
					}
				}
				if allowed {
					break
				}
			}
		}
		if !allowed {
			// Issue #80: Send warning message back to sender
			senderInbox := filepath.Join(sourceSessionDir, "inbox", info.From)
			if mkErr := os.MkdirAll(senderInbox, 0o755); mkErr == nil {
				// Build list of allowed neighbors for sender
				var neighbors []string
				for _, senderKey := range []string{info.From, senderFullName} {
					if nbrs, ok := adjacency[senderKey]; ok {
						neighbors = append(neighbors, nbrs...)
						break
					}
				}

				now := time.Now()
				warnTS := now.Format("20060102-150405")
				warnFilename := fmt.Sprintf("%s-from-postman-to-%s.md", warnTS, info.From)
				neighborsStr := strings.Join(neighbors, ", ")
				if neighborsStr == "" {
					neighborsStr = "none"
				}

				// Issue #92: Use configurable template with mode support
				warnTemplate := cfg.EdgeViolationWarningTemplate
				isDefaultTemplate := (warnTemplate == "")
				if isDefaultTemplate {
					warnTemplate = "you can't talk to \"{attempted_recipient}\". Can talk to: {allowed_edges}. Your message has been moved to dead-letter/."
				}

				// Build variables map for template expansion
				vars := map[string]string{
					"context_id":          contextID,
					"node":                info.From,
					"timestamp":           now.Format(time.RFC3339),
					"attempted_recipient": info.To,
					"allowed_edges":       neighborsStr,
					"reply_command":       cfg.ReplyCommand,
				}

				// Expand template
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				warnBody := template.ExpandTemplate(warnTemplate, vars, timeout)

				// Issue #92: Add reply instructions for verbose mode
				mode := cfg.EdgeViolationWarningMode
				if mode == "" {
					mode = "compact"
				}
				if mode == "verbose" && isDefaultTemplate {
					// IMPORTANT FIX: Only add reply instructions when using default template
					// Custom templates should not be modified by verbose mode
					// BLOCKING FIX: Avoid duplicate --context-id if already present
					replyCmd := cfg.ReplyCommand
					if !strings.Contains(replyCmd, "--context-id") {
						replyCmd = fmt.Sprintf("%s --context-id %s", replyCmd, contextID)
					}
					replyInstructions := fmt.Sprintf("\n\nSteps:\n\n1. %s --to <recipient>\n   - Replace `<recipient>` with one of: %s\n2. Edit the draft content\n3. mv from draft/ to post/",
						replyCmd,
						neighborsStr,
					)
					warnBody += replyInstructions
				}

				// Build full message with header
				warnContent := fmt.Sprintf(
					"---\nmethod: message/send\nparams:\n  contextId: %s\n  from: postman\n  to: %s\n  timestamp: %s\n---\n\n## Content\n\n%s\n",
					contextID,
					info.From,
					now.Format(time.RFC3339),
					warnBody,
				)
				warnPath := filepath.Join(senderInbox, warnFilename)
				_ = os.WriteFile(warnPath, []byte(warnContent), 0o644)
			}

			// Routing denied: move to dead-letter/ in source session
			dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
			log.Printf("📨 postman: routing denied %s -> %s (moved to dead-letter/)\n", info.From, info.To)
			// Issue #53: Notify dead-letter event
			if events != nil {
				events <- DaemonEvent{
					Type:    "message_received",
					Message: fmt.Sprintf("Dead-letter: %s -> %s (routing denied)", info.From, info.To),
				}
			}
			return os.Rename(postPath, dst)
		}
	}

	// Check session enabled/disabled state
	// Extract sender and recipient session names
	senderSessionName := sourceSessionName
	recipientSessionName := nodeInfo.SessionName

	// Both sessions must be enabled (unless sender is postman)
	// NOTE: Postman exemption applies to all messages from postman, not just PING.
	// Currently only PING uses this exemption. If other postman message types are added
	// in the future, consider whether they should also bypass session checks.
	if info.From != "postman" {
		if !isSessionEnabled(senderSessionName) {
			dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
			log.Printf("📨 postman: sender session %s disabled (moved to dead-letter/)\n", senderSessionName)
			// Issue #53: Notify dead-letter event
			if events != nil {
				events <- DaemonEvent{
					Type:    "message_received",
					Message: fmt.Sprintf("Dead-letter: %s -> %s (sender session disabled)", info.From, info.To),
				}
			}
			return os.Rename(postPath, dst)
		}
	}
	if info.From != "postman" {
		if !isSessionEnabled(recipientSessionName) {
			dst := filepath.Join(sourceSessionDir, "dead-letter", filename)
			log.Printf("📨 postman: recipient session %s disabled (moved to dead-letter/)\n", recipientSessionName)
			// Issue #53: Notify dead-letter event
			if events != nil {
				events <- DaemonEvent{
					Type:    "message_received",
					Message: fmt.Sprintf("Dead-letter: %s -> %s (recipient session disabled)", info.From, info.To),
				}
			}
			return os.Rename(postPath, dst)
		}
	}

	// Ensure recipient inbox subdirectory exists (in recipient's session directory)
	recipientSessionDir := nodeInfo.SessionDir
	recipientInbox := filepath.Join(recipientSessionDir, "inbox", info.To)
	if err := os.MkdirAll(recipientInbox, 0o755); err != nil {
		return fmt.Errorf("creating recipient inbox: %w", err)
	}

	dst := filepath.Join(recipientInbox, filename)
	if err := os.Rename(postPath, dst); err != nil {
		return fmt.Errorf("moving to inbox: %w", err)
	}

	// Send tmux notification to the recipient pane
	// Issue #84: Get PONG-active nodes for talks_to_line filtering
	pongActiveNodes := idleTracker.GetPongActiveNodes()
	notificationMsg := notification.BuildNotification(cfg, adjacency, knownNodes, contextID, info.To, info.From, sourceSessionName, postPath, pongActiveNodes)
	enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
	tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	enterCount := cfg.Nodes[info.To].EnterCount
	_ = notification.SendToPane(paneID, notificationMsg, enterDelay, tmuxTimeout, enterCount)
	// NOTE: Error already logged by SendToPane (WARNING level)
	// Continue with delivery (notification failure does not fail delivery)

	// Update activity timestamps for idle detection (Issue #55)
	// NOTE: Exclude system messages (from/to "postman") from ball tracking.
	// Both UpdateSendActivity and UpdateReceiveActivity skip when info.From == "postman"
	// to prevent system-delivered messages from causing false "holding" state.
	// Issue #79: Use session-prefixed keys for tracking
	if info.From != "postman" {
		idleTracker.UpdateSendActivity(sourceSessionName + ":" + info.From)
	}
	if info.From != "postman" && info.To != "postman" {
		idleTracker.UpdateReceiveActivity(recipientSessionName + ":" + info.To)
	}

	log.Printf("📬 postman: delivered %s -> %s\n", filename, info.To)
	return nil
}

// ScanInboxMessages scans the inbox directory and returns a list of MessageInfo.
func ScanInboxMessages(inboxPath string) []MessageInfo {
	var messages []MessageInfo

	entries, err := os.ReadDir(inboxPath)
	if err != nil {
		return messages
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		info, err := ParseMessageFilename(entry.Name())
		if err != nil {
			continue
		}
		messages = append(messages, *info)
	}

	return messages
}
