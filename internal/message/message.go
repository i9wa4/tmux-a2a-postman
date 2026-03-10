package message

import (
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// validNodeNameRe validates from/to fields in message filenames (#174).
// Allows alphanumeric characters and hyphens, must start with alphanumeric.
var validNodeNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

// Dead-letter reason strings used in sender notifications and TUI events (Issue #161).
const (
	deadLetterReasonEnvelopeMismatch         = "envelope mismatch"
	deadLetterReasonUnknownRecipient         = "unknown recipient"
	deadLetterReasonSenderSessionDisabled    = "sender session disabled"
	deadLetterReasonRecipientSessionDisabled = "recipient session disabled"
)

// Dead-letter filename suffixes appended before .md extension (Issue #206).
const (
	dlSuffixParseError       = "-dl-parse-error"
	dlSuffixEnvelopeMismatch = "-dl-envelope-mismatch"
	dlSuffixUnknownRecipient = "-dl-unknown-recipient"
	dlSuffixUnknownSender    = "-dl-unknown-sender"
	dlSuffixRoutingDenied    = "-dl-routing-denied"
	dlSuffixSessionDisabled  = "-dl-session-disabled"
	DlSuffixTTLExpired       = "-dl-ttl-expired"
)

// deadLetterDst builds the dead-letter destination path with reason suffix.
// Transforms "msg.md" → "msg-dl-{reason}.md" in dead-letter/ directory.
func deadLetterDst(sessionDir, filename, suffix string) string {
	base := strings.TrimSuffix(filename, ".md")
	return filepath.Join(sessionDir, "dead-letter", base+suffix+".md")
}

// StripDeadLetterSuffix removes the -dl-{reason} suffix from a dead-letter filename.
// Transforms "msg-dl-routing-denied.md" → "msg.md".
func StripDeadLetterSuffix(filename string) string {
	base := strings.TrimSuffix(filename, ".md")
	if idx := strings.Index(base, "-dl-"); idx >= 0 {
		return base[:idx] + ".md"
	}
	return filename
}

// DaemonEvent represents an event to be sent to the TUI (Issue #53).
type DaemonEvent struct {
	Type    string
	Message string
	Details map[string]interface{}
}

// MessageInfo holds parsed information from a message filename.
type MessageInfo struct {
	Timestamp   string
	From        string
	To          string
	SessionHash string // Optional 4-char hex hash extracted from filename (#198)
	Filename    string // Original filename (set by ScanInboxMessages)
}

// SessionHash returns a 4-character hex hash of the tmux session name (#198).
// Returns empty string if sessionName is empty.
func SessionHash(sessionName string) string {
	if sessionName == "" {
		return ""
	}
	h := sha256.Sum256([]byte(sessionName))
	return fmt.Sprintf("%x", h[:2])
}

// GenerateFilename builds a message filename with optional session hash (#198).
// Format: {timestamp}-s{hash}-from-{sender}-to-{recipient}.md (with hash)
// Format: {timestamp}-from-{sender}-to-{recipient}.md (without hash)
func GenerateFilename(ts, sender, recipient, sessionName string) string {
	hash := SessionHash(sessionName)
	if hash != "" {
		return fmt.Sprintf("%s-s%s-from-%s-to-%s.md", ts, hash, sender, recipient)
	}
	return fmt.Sprintf("%s-from-%s-to-%s.md", ts, sender, recipient)
}

// sessionHashRe matches the optional -s{4hex} session hash suffix in the timestamp portion (#198).
var sessionHashRe = regexp.MustCompile(`-s([0-9a-f]{4})$`)

// ParseMessageFilename parses a message filename in the format:
// {timestamp}-from-{sender}-to-{recipient}.md
// {timestamp}-s{hash}-from-{sender}-to-{recipient}.md (with session hash, #198)
// Example: 20260201-022121-from-orchestrator-to-worker.md
// Example: 20260201-022121-s1a2b-from-orchestrator-to-worker.md
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

	timestampRaw := base[:fromIdx]
	from := rest[:toIdx]
	to := rest[toIdx+len("-to-"):]

	if timestampRaw == "" || from == "" || to == "" {
		return nil, fmt.Errorf("invalid filename: empty field in %q", filename)
	}

	// Validate from/to charset (#174): reject path traversal and injection
	if !validNodeNameRe.MatchString(from) {
		return nil, fmt.Errorf("invalid filename: invalid from field %q in %q", from, filename)
	}
	if !validNodeNameRe.MatchString(to) {
		return nil, fmt.Errorf("invalid filename: invalid to field %q in %q", to, filename)
	}

	// Extract optional session hash from timestamp portion (#198)
	var sessionHash string
	timestamp := timestampRaw
	if m := sessionHashRe.FindStringSubmatch(timestampRaw); m != nil {
		sessionHash = m[1]
		timestamp = timestampRaw[:len(timestampRaw)-len(m[0])]
	}

	return &MessageInfo{
		Timestamp:   timestamp,
		From:        from,
		To:          to,
		SessionHash: sessionHash,
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
		dst := deadLetterDst(sourceSessionDir, filename, dlSuffixParseError)
		// Issue #53: Notify dead-letter event
		if events != nil {
			events <- DaemonEvent{
				Type:    "message_received",
				Message: fmt.Sprintf("Dead-letter: %s (parse error)", filename),
			}
		}
		return os.Rename(postPath, dst)
	}

	// Pre-delivery staleness warning: log WARN for messages sitting in post/ too long (#218)
	if cfg.MessageAgeWarningSeconds > 0 {
		if msgTime, parseErr := time.Parse("20060102-150405", info.Timestamp); parseErr == nil {
			preDeliveryAge := time.Since(msgTime)
			if preDeliveryAge.Seconds() > cfg.MessageAgeWarningSeconds {
				log.Printf("postman: WARNING: stale post/ message %s (age: %s, threshold: %.0fs)\n", filename, preDeliveryAge.Truncate(time.Second), cfg.MessageAgeWarningSeconds)
			}
		}
	}

	// Issue #161: Validate frontmatter envelope (skip for postman/daemon-origin messages)
	if info.From != "postman" && info.From != "daemon" {
		rawBytes, readErr := os.ReadFile(postPath)
		if os.IsNotExist(readErr) {
			return nil // Already processed (duplicate event)
		}
		if readErr != nil {
			log.Printf("postman: WARNING: failed to read message for envelope validation %s: %v\n", filename, readErr)
		} else {
			envFrom, envTo, parseErr := parseEnvelopeFromTo(string(rawBytes))
			if parseErr != nil || envFrom != info.From || envTo != info.To {
				dst := deadLetterDst(sourceSessionDir, filename, dlSuffixEnvelopeMismatch)
				sendDeadLetterNotification(sourceSessionDir, contextID, info.From, deadLetterReasonEnvelopeMismatch, filename)
				if events != nil {
					events <- DaemonEvent{
						Type:    "message_received",
						Message: fmt.Sprintf("Dead-letter: %s -> %s (%s)", info.From, info.To, deadLetterReasonEnvelopeMismatch),
					}
				}
				return os.Rename(postPath, dst)
			}
		}
	}

	// Resolve recipient name (Issue #33: session-aware adjacency)
	recipientFullName := discovery.ResolveNodeName(info.To, sourceSessionName, knownNodes)
	if recipientFullName == "" {
		// Unknown recipient: move to dead-letter/ in source session
		dst := deadLetterDst(sourceSessionDir, filename, dlSuffixUnknownRecipient)
		sendDeadLetterNotification(sourceSessionDir, contextID, info.From, deadLetterReasonUnknownRecipient, filename)
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
	senderFullName := discovery.ResolveNodeName(info.From, sourceSessionName, knownNodes)
	if senderFullName == "" && info.From != "postman" && info.From != "daemon" {
		// Unknown sender: move to dead-letter/ in source session
		dst := deadLetterDst(sourceSessionDir, filename, dlSuffixUnknownSender)
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
	// IMPORTANT: sender="postman" or sender="daemon" is always allowed (#172)
	if info.From != "postman" && info.From != "daemon" {
		allowed := false
		// Try adjacency lookup with both simple name and full name
		// This supports both old-style (simple names) and new-style (session:node) adjacency configs
		for _, senderKey := range []string{info.From, senderFullName} {
			if neighbors, ok := adjacency[senderKey]; ok {
				for _, neighbor := range neighbors {
					// Resolve neighbor name to full name
					neighborFullName := discovery.ResolveNodeName(neighbor, sourceSessionName, knownNodes)
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
			if mkErr := os.MkdirAll(senderInbox, 0o700); mkErr == nil {
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

				// Issue #92, #222: DM-1 normalized full envelope template
				warnTemplate := cfg.EdgeViolationWarningTemplate

				// Build variables map for template expansion
				vars := map[string]string{
					"context_id":          contextID,
					"node":                info.From,
					"iso_timestamp":       now.Format(time.RFC3339),
					"timestamp":           now.Format(time.RFC3339),
					"attempted_recipient": info.To,
					"allowed_edges":       neighborsStr,
					"reply_command":       cfg.ReplyCommand,
					"session_dir":         sourceSessionDir,
					"filename":            warnFilename,
				}

				// Expand template
				timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
				warnContent := template.ExpandTemplate(warnTemplate, vars, timeout)

				// Issue #92: Append reply instructions for verbose mode
				mode := cfg.EdgeViolationWarningMode
				if mode == "" {
					mode = "compact"
				}
				if mode == "verbose" {
					replyCmd := cfg.ReplyCommand
					if !strings.Contains(replyCmd, "--context-id") {
						replyCmd = fmt.Sprintf("%s --context-id %s", replyCmd, contextID)
					}
					replyInstructions := fmt.Sprintf("\n\nSteps:\n\n1. %s --to <recipient>\n   - Replace `<recipient>` with one of: %s\n2. Edit the draft content\n3. tmux-a2a-postman send <file>",
						replyCmd,
						neighborsStr,
					)
					warnContent += replyInstructions
				}
				warnPath := filepath.Join(senderInbox, warnFilename)
				_ = os.WriteFile(warnPath, []byte(warnContent), 0o600)
			}

			// Routing denied: move to dead-letter/ in source session
			dst := deadLetterDst(sourceSessionDir, filename, dlSuffixRoutingDenied)
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

	// Both sessions must be enabled (unless sender is postman or daemon)
	// NOTE: Postman/daemon exemption applies to all system-generated messages.
	// Postman sends PING; daemon sends alerts to ui_node (#172).
	if info.From != "postman" && info.From != "daemon" {
		if !isSessionEnabled(senderSessionName) {
			dst := deadLetterDst(sourceSessionDir, filename, dlSuffixSessionDisabled)
			log.Printf("📨 postman: sender session %s disabled (moved to dead-letter/)\n", senderSessionName)
			sendDeadLetterNotification(sourceSessionDir, contextID, info.From, deadLetterReasonSenderSessionDisabled, filename)
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
	if info.From != "postman" && info.From != "daemon" {
		if !isSessionEnabled(recipientSessionName) {
			dst := deadLetterDst(sourceSessionDir, filename, dlSuffixSessionDisabled)
			log.Printf("📨 postman: recipient session %s disabled (moved to dead-letter/)\n", recipientSessionName)
			sendDeadLetterNotification(sourceSessionDir, contextID, info.From, deadLetterReasonRecipientSessionDisabled, filename)
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
	if err := os.MkdirAll(recipientInbox, 0o700); err != nil {
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
	nodeEnterDelay := cfg.GetNodeConfig(info.To).EnterDelay
	enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
	if nodeEnterDelay != 0 {
		enterDelay = time.Duration(nodeEnterDelay * float64(time.Second))
	}
	tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	paneIDForProbe := paneID
	enterCount := notification.ResolveEnterCount(
		cfg.GetNodeConfig(info.To).EnterCount,
		func() (string, error) {
			out, err := exec.Command("tmux", "display-message", "-t",
				paneIDForProbe, "-p", "#{pane_current_command}").Output()
			return strings.TrimSpace(string(out)), err
		},
	)

	_ = notification.SendToPane(paneID, notificationMsg, enterDelay, tmuxTimeout, enterCount)
	// NOTE: Error already logged by SendToPane (WARNING level)
	// Continue with delivery (notification failure does not fail delivery)

	// Update activity timestamps for idle detection (Issue #55)
	// NOTE: Exclude system messages (from "postman" or "daemon") from ball tracking.
	// Both UpdateSendActivity and UpdateReceiveActivity skip system senders
	// to prevent system-delivered messages from causing false "holding" state.
	// Issue #79: Use session-prefixed keys for tracking
	if info.From != "postman" && info.From != "daemon" {
		idleTracker.UpdateSendActivity(sourceSessionName + ":" + info.From)
	}
	if info.From != "postman" && info.From != "daemon" {
		idleTracker.UpdateReceiveActivity(recipientSessionName + ":" + info.To)
	}

	// Delivery latency logging (#179): parse message timestamp and log age.
	// Issue #212: Also emit latency_warning event when threshold exceeded.
	if msgTime, err := time.Parse("20060102-150405", info.Timestamp); err == nil {
		age := time.Since(msgTime)
		if cfg.MessageAgeWarningSeconds > 0 && age.Seconds() > cfg.MessageAgeWarningSeconds {
			log.Printf("📬 postman: delivered %s -> %s (age: %s — WARNING: exceeds %.0fs threshold)\n", filename, info.To, age.Truncate(time.Second), cfg.MessageAgeWarningSeconds)
			if events != nil {
				events <- DaemonEvent{
					Type:    "latency_warning",
					Message: fmt.Sprintf("Delivery latency alert: %s -> %s (age: %s, threshold: %.0fs)", info.From, info.To, age.Truncate(time.Second), cfg.MessageAgeWarningSeconds),
				}
			}
		} else {
			log.Printf("📬 postman: delivered %s -> %s (age: %s)\n", filename, info.To, age.Truncate(time.Second))
		}
	} else {
		log.Printf("📬 postman: delivered %s -> %s\n", filename, info.To)
	}
	return nil
}

// sendDeadLetterNotification writes a dead-letter notification directly to the
// sender's inbox. Bypasses post/ to avoid re-triggering the daemon delivery loop.
// Pattern follows the routing-denied notification at DeliverMessage:162-175.
// Issue #208: Extended with dead-letter path, allowed neighbors, and re-send hint.
func sendDeadLetterNotification(sessionDir, contextID, senderNode, reason, originalFilename string) {
	senderInbox := filepath.Join(sessionDir, "inbox", senderNode)
	if mkErr := os.MkdirAll(senderInbox, 0o700); mkErr != nil {
		log.Printf("postman: WARNING: failed to create dead-letter notification inbox for %s: %v\n", senderNode, mkErr)
		return
	}
	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s.md", ts, senderNode)

	// Build dead-letter file path for reference
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", originalFilename)

	content := fmt.Sprintf(
		"---\nmethod: message/send\nparams:\n  contextId: %s\n  from: postman\n  to: %s\n  timestamp: %s\n  messageType: dead_letter_notification\n---\n\n## Dead-letter Notification\n\nYour message %q was not delivered.\nReason: %s\n\nDead-letter path: %s\n\nTo re-send with corrected recipient: tmux-a2a-postman resend --context-id <id> --file <dead-letter-path>\n",
		contextID,
		senderNode,
		now.Format(time.RFC3339),
		originalFilename,
		reason,
		deadLetterPath,
	)
	notifPath := filepath.Join(senderInbox, filename)
	if writeErr := os.WriteFile(notifPath, []byte(content), 0o600); writeErr != nil {
		log.Printf("postman: WARNING: failed to write dead-letter notification for %s: %v\n", senderNode, writeErr)
	}
}

// parseEnvelopeFromTo extracts the from and to fields from the YAML frontmatter
// of a message file. Frontmatter is the block between the first two "---" delimiters.
// from and to must appear as indented fields under the params: top-level key.
// Returns an error if the frontmatter block is absent or if either field is missing.
func parseEnvelopeFromTo(content string) (from, to string, err error) {
	// Find opening "---\n"
	first := strings.Index(content, "---\n")
	if first < 0 {
		return "", "", fmt.Errorf("no frontmatter block found")
	}
	rest := content[first+4:]
	// Find closing "\n---"
	second := strings.Index(rest, "\n---")
	if second < 0 {
		return "", "", fmt.Errorf("frontmatter not closed")
	}
	frontmatter := rest[:second]

	// Scan lines for params: block, then collect from/to
	inParams := false
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimRight(line, "\r") // handle \r\n line endings
		if line == "params:" {
			inParams = true
			continue
		}
		if inParams {
			// Stop at next top-level key (non-empty, no leading space)
			if len(line) > 0 && line[0] != ' ' {
				inParams = false
				continue
			}
			if strings.HasPrefix(line, "  from: ") {
				from = strings.TrimSpace(strings.TrimPrefix(line, "  from: "))
			} else if strings.HasPrefix(line, "  to: ") {
				to = strings.TrimSpace(strings.TrimPrefix(line, "  to: "))
			}
		}
	}

	if from == "" || to == "" {
		return "", "", fmt.Errorf("missing from or to in params block")
	}
	return from, to, nil
}

// DrainStalePost moves stale messages from post/ to dead-letter/ with ttl-expired suffix.
// A message is stale if its file modification time exceeds ttlSeconds.
// Returns the number of drained messages. Skips if ttlSeconds <= 0.
func DrainStalePost(sessionDir string, ttlSeconds float64) int {
	if ttlSeconds <= 0 {
		return 0
	}
	postDir := filepath.Join(sessionDir, "post")
	entries, err := os.ReadDir(postDir)
	if err != nil {
		return 0
	}
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	if mkErr := os.MkdirAll(deadLetterDir, 0o700); mkErr != nil {
		log.Printf("postman: WARNING: failed to create dead-letter dir for TTL drain: %v\n", mkErr)
		return 0
	}
	ttl := time.Duration(ttlSeconds * float64(time.Second))
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		fi, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(fi.ModTime()) > ttl {
			src := filepath.Join(postDir, entry.Name())
			dst := deadLetterDst(sessionDir, entry.Name(), DlSuffixTTLExpired)
			if err := os.Rename(src, dst); err == nil {
				log.Printf("postman: drained stale post/ message: %s (TTL expired)\n", entry.Name())
				count++
			}
		}
	}
	return count
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
