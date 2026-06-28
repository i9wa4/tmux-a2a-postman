package message

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/idle"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/router"
	"github.com/i9wa4/tmux-a2a-postman/internal/store"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

// Dead-letter reason strings used in sender notifications and TUI events (Issue #161).
const (
	deadLetterReasonEnvelopeMismatch         = "envelope mismatch"
	deadLetterReasonUnknownRecipient         = "unknown recipient"
	deadLetterReasonUnknownRecipientSession  = "unknown recipient session"
	deadLetterReasonSenderSessionDisabled    = "sender session disabled"
	deadLetterReasonRecipientSessionDisabled = "recipient session disabled"
	deadLetterReasonForeignSession           = "foreign session"
)

// Dead-letter filename suffixes appended before .md extension (Issue #206).
const (
	dlSuffixParseError       = "-dl-parse-error"
	dlSuffixEnvelopeMismatch = "-dl-envelope-mismatch"
	dlSuffixUnknownRecipient = "-dl-unknown-recipient"
	dlSuffixUnknownSession   = "-dl-unknown-session"
	dlSuffixUnknownSender    = "-dl-unknown-sender"
	dlSuffixRoutingDenied    = "-dl-routing-denied"
	dlSuffixSessionDisabled  = "-dl-session-disabled"
	DlSuffixTTLExpired       = "-dl-ttl-expired"
	dlSuffixForeignSession   = "-dl-foreign-session"
	dlSuffixForgedSender     = "-dl-forged-sender"
)

// inboxQueueCap is the maximum number of messages allowed in a recipient inbox
// before overflow messages are sent to dead-letter (agent-session queue guard).
const inboxQueueCap = 20

const (
	deadLetterReasonQueueFull = "inbox queue full"
	dlSuffixQueueFull         = "-dl-queue-full"
)

// deadLetterDst builds the dead-letter destination path with reason suffix.
// Transforms "msg.md" → "msg-dl-{reason}.md" in dead-letter/ directory.
func deadLetterDst(sessionDir, filename, suffix string) string {
	return store.DeadLetterPath(sessionDir, filename, suffix)
}

func moveToDeadLetter(srcPath, dstPath string) error {
	return store.MoveToDeadLetter(srcPath, dstPath)
}

func recordMailboxProjectionPayload(sessionDir, sessionName, eventType string, visibility journal.Visibility, payload journal.MailboxEventPayload) {
	payload = enrichMailboxProjectionPayload(payload)
	if err := journal.RecordProcessMailboxPayload(sessionDir, sessionName, eventType, visibility, payload, time.Now()); err != nil {
		log.Printf("postman: WARNING: component=%s event=append_failed mailbox_event=%s err=%v\n", projection.MailboxProjectionComponent, eventType, err)
	}
}

func enrichMailboxProjectionPayload(payload journal.MailboxEventPayload) journal.MailboxEventPayload {
	if payload.Content == "" {
		return payload
	}
	metadata, err := envelope.ParseMetadata(payload.Content)
	if err != nil {
		return payload
	}
	if payload.MessageID == "" {
		payload.MessageID = metadata.MessageID
	}
	if payload.ContextID == "" {
		payload.ContextID = metadata.ContextID
	}
	if payload.From == "" {
		payload.From = metadata.From
	}
	if payload.To == "" {
		payload.To = metadata.To
	}
	if payload.ReplyPolicy == "" {
		payload.ReplyPolicy = envelope.ResolveReplyPolicyFromMetadata(metadata)
	}
	if payload.ReplyTo == "" {
		payload.ReplyTo = metadata.ReplyTo
	}
	if payload.MessageType == "" {
		payload.MessageType = metadata.MessageType
	}
	if payload.Timestamp == "" {
		payload.Timestamp = metadata.Timestamp
	}
	if payload.ThreadID == "" {
		payload.ThreadID = metadata.ThreadID
	}
	if payload.TaskID == "" {
		payload.TaskID = metadata.TaskID
	}
	if payload.RunID == "" {
		payload.RunID = metadata.RunID
	}
	if payload.InputRequestID == "" {
		payload.InputRequestID = metadata.InputRequestID
	}
	if payload.FillsInputRequestID == "" {
		payload.FillsInputRequestID = metadata.FillsInputRequestID
	}
	if payload.InputRequestSetID == "" {
		payload.InputRequestSetID = metadata.InputRequestSetID
	}
	if payload.BranchID == "" {
		payload.BranchID = metadata.BranchID
	}
	if payload.CompletionRule == "" {
		payload.CompletionRule = metadata.CompletionRule
	}
	return payload
}

func syncMailboxProjection(sessionDir string) {
	if err := projection.SyncMailboxProjection(sessionDir); err != nil {
		log.Printf("postman: WARNING: component=%s event=sync_failed session_dir=%s err=%v\n", projection.MailboxProjectionComponent, sessionDir, err)
	}
}

func mailboxThreadIDFromContent(content string) string {
	if content == "" {
		return ""
	}
	metadata, err := ParseEnvelopeMetadata(content)
	if err != nil {
		return ""
	}
	return metadata.ThreadID
}

func messageBodyFromContent(content string) string {
	return envelope.BodyFromContent(content)
}

func MessageBodyFromContent(content string) string {
	return envelope.BodyFromContent(content)
}

func ResolveReplyPolicyFromContent(content string) string {
	return envelope.ResolveReplyPolicyFromContent(content)
}

func ResolveReplyPolicyForSend(body string, noReply, replyRequired bool) string {
	return envelope.ResolveReplyPolicyForSend(body, noReply, replyRequired)
}

func IsNoReplyBody(content string) bool {
	return envelope.IsNoReplyBody(content)
}

func EnsureEnvelopeParams(content string, fields map[string]string) string {
	return envelope.EnsureParams(content, fields)
}

func approvalDecisionFromContent(content string) (journal.ApprovalDecision, bool) {
	body := messageBodyFromContent(content)
	if body == "" {
		return "", false
	}
	firstLine := body
	if idx := strings.Index(firstLine, "\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)
	switch {
	case strings.HasPrefix(firstLine, "APPROVED:"):
		return journal.ApprovalDecisionApproved, true
	case strings.HasPrefix(firstLine, "NOT APPROVED:"):
		return journal.ApprovalDecisionRejected, true
	default:
		return "", false
	}
}

type approvalDeliveryEvent struct {
	EventType string
	Payload   interface{}
	ThreadID  string
}

func approvalEventForDelivery(messageID, from, to, content string) (approvalDeliveryEvent, bool) {
	threadID := mailboxThreadIDFromContent(content)
	if threadID == "" {
		return approvalDeliveryEvent{}, false
	}

	sender := nodeaddr.Simple(from)
	recipient := nodeaddr.Simple(to)

	switch {
	case sender == "orchestrator" && recipient == "critic":
		return approvalDeliveryEvent{
			EventType: journal.ApprovalRequestedEventType,
			Payload: journal.ApprovalRequestPayload{
				Requester: sender,
				Reviewer:  recipient,
				MessageID: messageID,
			},
			ThreadID: threadID,
		}, true
	case sender == "critic" && recipient == "orchestrator":
		decision, ok := approvalDecisionFromContent(content)
		if !ok {
			return approvalDeliveryEvent{}, false
		}
		return approvalDeliveryEvent{
			EventType: journal.ApprovalDecidedEventType,
			Payload: journal.ApprovalDecisionPayload{
				Reviewer:  sender,
				Decision:  decision,
				MessageID: messageID,
			},
			ThreadID: threadID,
		}, true
	default:
		return approvalDeliveryEvent{}, false
	}
}

func recordApprovalEvent(sessionDir, sessionName string, event approvalDeliveryEvent, now time.Time) {
	if err := journal.RecordProcessEventWithOptions(
		sessionDir,
		sessionName,
		event.EventType,
		journal.VisibilityOperatorVisible,
		event.Payload,
		journal.AppendOptions{ThreadID: event.ThreadID},
		now,
	); err != nil {
		log.Printf("postman: WARNING: journal approval append failed for %s: %v\n", event.EventType, err)
	}
}

func recordApprovalEventForDelivery(sourceSessionDir, sourceSessionName, recipientSessionDir, recipientSessionName, messageID, from, to, content string, now time.Time) {
	event, ok := approvalEventForDelivery(messageID, from, to, content)
	if !ok {
		return
	}

	recordApprovalEvent(sourceSessionDir, sourceSessionName, event, now)
	if recipientSessionDir != sourceSessionDir {
		recordApprovalEvent(recipientSessionDir, recipientSessionName, event, now)
	}
}

func moveToDeadLetterWithProjection(sessionDir, sessionName, srcPath, dstPath, messageID, from, to, content string) error {
	if err := moveToDeadLetter(srcPath, dstPath); err != nil {
		return err
	}
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionDeadLetteredEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID:     messageID,
		From:          from,
		To:            to,
		ThreadID:      mailboxThreadIDFromContent(content),
		Path:          shadowRelativePath(sessionDir, dstPath),
		SourcePath:    shadowRelativePath(sessionDir, srcPath),
		FailureReason: deadLetterFailureReason(dstPath),
		Content:       content,
	})
	syncMailboxProjection(sessionDir)
	return nil
}

func deadLetterFailureReason(deadLetterPath string) string {
	base := strings.TrimSuffix(filepath.Base(deadLetterPath), ".md")
	idx := strings.LastIndex(base, "-dl-")
	if idx < 0 {
		return ""
	}
	return base[idx+len("-dl-"):]
}

func resolveRuntimeNode(address, sourceSessionName string, knownNodes map[string]discovery.NodeInfo) router.Resolution {
	sessions := map[string]bool{sourceSessionName: true}
	for _, nodeInfo := range knownNodes {
		if nodeInfo.SessionName != "" {
			sessions[nodeInfo.SessionName] = true
		}
	}
	return router.Resolve(address, sourceSessionName, func(key string) bool {
		_, found := knownNodes[key]
		return found
	}, func(sessionName string) bool {
		return sessions[sessionName]
	})
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

func emitDeliveryDecisionEvent(events chan<- DaemonEvent, decision deliveryDecision, info *MessageInfo, filename string) {
	if events == nil || decision.EventReason == "" {
		return
	}
	message := fmt.Sprintf("Dead-letter: %s (%s)", filename, decision.EventReason)
	if info != nil {
		message = fmt.Sprintf("Dead-letter: %s -> %s (%s)", info.From, info.To, decision.EventReason)
	}
	events <- DaemonEvent{
		Type:    "message_received",
		Message: message,
	}
}

func deadLetterDecisionDestination(sessionDir, filename string, decision deliveryDecision) string {
	return deadLetterDst(sessionDir, filename, decision.DeadLetterSuffix)
}

func moveToDeadLetterForDecision(sessionDir, sessionName, postPath, dst, filename string, info *MessageInfo, content string) error {
	from, to := "", ""
	if info != nil {
		from = info.From
		to = info.To
	}
	return moveToDeadLetterWithProjection(sessionDir, sessionName, postPath, dst, filename, from, to, content)
}

// MessageInfo holds parsed information from a message filename.
type MessageInfo struct {
	Timestamp   string
	From        string
	To          string
	SessionHash string // Optional 4-char hex hash extracted from filename (#198)
	Filename    string // Original filename (set by ScanInboxMessages)
}

type EnvelopeMetadata = envelope.Metadata

// SessionHash returns a 4-character hex hash of the tmux session name (#198).
// Returns empty string if sessionName is empty.
func SessionHash(sessionName string) string {
	if sessionName == "" {
		return ""
	}
	h := sha256.Sum256([]byte(sessionName))
	return fmt.Sprintf("%x", h[:2])
}

// GenerateFilename builds a message filename with optional session hash and random nonce (#198).
// Format: {timestamp}-s{hash}-r{nonce}-from-{sender}-to-{recipient}.md (with hash)
// Format: {timestamp}-r{nonce}-from-{sender}-to-{recipient}.md (without hash)
func GenerateFilename(ts, sender, recipient, sessionName string) (string, error) {
	if err := nodeaddr.Validate(sender); err != nil {
		return "", fmt.Errorf("invalid sender address: %w", err)
	}
	if err := nodeaddr.Validate(recipient); err != nil {
		return "", fmt.Errorf("invalid recipient address: %w", err)
	}

	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	nonce := fmt.Sprintf("%04x", b)
	hash := SessionHash(sessionName)
	senderSegment := nodeaddr.EncodeFilenameSegment(sender)
	recipientSegment := nodeaddr.EncodeFilenameSegment(recipient)
	if hash != "" {
		return fmt.Sprintf("%s-s%s-r%s-from-%s-to-%s.md", ts, hash, nonce, senderSegment, recipientSegment), nil
	}
	return fmt.Sprintf("%s-r%s-from-%s-to-%s.md", ts, nonce, senderSegment, recipientSegment), nil
}

// sessionHashRe matches the optional -s{4hex} session hash suffix in the timestamp portion (#198).
var sessionHashRe = regexp.MustCompile(`-s([0-9a-f]{4})$`)

// nonceRe matches the optional -r{4hex} random nonce in the timestamp portion.
var nonceRe = regexp.MustCompile(`-r([0-9a-f]{4})$`)

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
	fromSegment := rest[:toIdx]
	toSegment := rest[toIdx+len("-to-"):]

	from, err := nodeaddr.DecodeFilenameSegment(fromSegment)
	if err != nil {
		return nil, fmt.Errorf("invalid filename: invalid from field %q in %q: %w", fromSegment, filename, err)
	}
	to, err := nodeaddr.DecodeFilenameSegment(toSegment)
	if err != nil {
		return nil, fmt.Errorf("invalid filename: invalid to field %q in %q: %w", toSegment, filename, err)
	}

	if timestampRaw == "" || from == "" || to == "" {
		return nil, fmt.Errorf("invalid filename: empty field in %q", filename)
	}

	// Validate from/to address syntax (#174): reject path traversal and malformed
	// session-prefixed values while allowing explicit session:node recipients.
	if err := nodeaddr.Validate(from); err != nil {
		return nil, fmt.Errorf("invalid filename: invalid from field %q in %q: %w", from, filename, err)
	}
	if err := nodeaddr.Validate(to); err != nil {
		return nil, fmt.Errorf("invalid filename: invalid to field %q in %q: %w", to, filename, err)
	}

	// Extract optional session hash and nonce from timestamp portion (#198)
	var sessionHash string
	timestamp := timestampRaw

	// Step 1: Strip optional nonce (-r{4hex}) from end of timestamp portion.
	// Must be done BEFORE session hash stripping because sessionHashRe anchors
	// with $ and expects -s{4hex} at the very end of the string.
	if m := nonceRe.FindStringSubmatch(timestamp); m != nil {
		timestamp = timestamp[:len(timestamp)-len(m[0])]
	}

	// Step 2: Strip optional session hash (-s{4hex}) and capture it for MessageInfo.
	// Both lines are required: m[1] carries the hash value; the slice drops the suffix.
	if m := sessionHashRe.FindStringSubmatch(timestamp); m != nil {
		sessionHash = m[1]
		timestamp = timestamp[:len(timestamp)-len(m[0])]
	}

	return &MessageInfo{
		Timestamp:   timestamp,
		From:        from,
		To:          to,
		SessionHash: sessionHash,
	}, nil
}

func writeRoutingDeniedWarning(sourceSessionDir, contextID string, info *MessageInfo, senderSimpleName, senderFullName string, adjacency map[string][]string, cfg *config.Config) {
	senderInbox := filepath.Join(sourceSessionDir, "inbox", senderSimpleName)
	if mkErr := os.MkdirAll(senderInbox, 0o700); mkErr != nil {
		return
	}

	var neighbors []string
	for _, senderKey := range []string{info.From, senderFullName} {
		if nbrs, ok := adjacency[senderKey]; ok {
			neighbors = append(neighbors, nbrs...)
			break
		}
	}

	now := time.Now()
	warnTS := now.Format("20060102-150405")
	warnFilename := fmt.Sprintf("%s-from-postman-to-%s.md", warnTS, senderSimpleName)
	neighborsStr := strings.Join(neighbors, ", ")
	if neighborsStr == "" {
		neighborsStr = "none"
	}

	vars := map[string]string{
		"context_id":          contextID,
		"node":                senderSimpleName,
		"iso_timestamp":       now.Format(time.RFC3339),
		"timestamp":           now.Format(time.RFC3339),
		"attempted_recipient": info.To,
		"allowed_edges":       neighborsStr,
		"reply_command":       envelope.RenderReplyCommand(cfg.ReplyCommand, contextID, senderSimpleName),
		"session_dir":         sourceSessionDir,
		"filename":            warnFilename,
	}

	timeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	warnContent := template.ExpandTemplate(cfg.EdgeViolationWarningTemplate, vars, timeout, cfg.AllowShellForEdgeViolationWarningTemplate())
	mode := cfg.EdgeViolationWarningMode
	if mode == "" {
		mode = "compact"
	}
	if mode == "verbose" {
		replyInstructions := fmt.Sprintf(
			"\n\ntmux-a2a-postman send-heredoc --to <allowed-node> <<'POSTMAN_BODY'\n<your message>\nPOSTMAN_BODY\n  - Replace <allowed-node> with one of: %s\n  - Use the quoted heredoc delimiter so shell snippets stay literal.",
			neighborsStr,
		)
		warnContent += replyInstructions
	}
	warnPath := filepath.Join(senderInbox, warnFilename)
	_ = os.WriteFile(warnPath, []byte(warnContent), 0o600)
}

// DeliverMessage moves a message from post/ to the recipient's inbox/ or dead-letter/.
// Multi-session support: postPath is the full path to the message file in post/ directory.
// The message will be delivered to the recipient's session directory based on NodeInfo.SessionDir.
// Routing rules (DEFAULT DENY):
// - sender="daemon" is always allowed
// - otherwise, sender->recipient edge must exist in adjacency map
// Session check: both sender and recipient sessions must be enabled (unless sender is daemon)
// Issue #53: Added events channel parameter for dead-letter notifications
// Issue #71: Added idleTracker parameter for activity tracking
func DeliverMessage(postPath string, contextID string, knownNodes map[string]discovery.NodeInfo, adjacency map[string][]string, cfg *config.Config, isSessionEnabled func(string) bool, events chan<- DaemonEvent, idleTracker *idle.IdleTracker, daemonSession string) error {
	// Extract filename from postPath
	filename := filepath.Base(postPath)

	// Extract source session directory from postPath
	// postPath format: /path/to/context-id/session-name/post/message.md
	sourceSessionDir := filepath.Dir(filepath.Dir(postPath))
	sourceSessionName := filepath.Base(sourceSessionDir)
	messageContent := ""
	policyInput := deliveryPolicyInput{
		Filename:          filename,
		SourceSessionName: sourceSessionName,
		DaemonSession:     daemonSession,
		QueueCap:          inboxQueueCap,
	}

	// Check if file still exists (handles duplicate filesystem watcher event)
	if _, err := os.Stat(postPath); os.IsNotExist(err) {
		return nil // Already processed
	}

	info, err := ParseMessageFilename(filename)
	if err != nil {
		// Parse error: move to dead-letter/ in source session
		decision := planDeliveryPolicy(deliveryPolicyInput{
			Filename:   filename,
			ParseError: true,
		})
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
		rawContent, readErr := os.ReadFile(postPath)
		if readErr == nil {
			messageContent = string(rawContent)
		}
		// Issue #53: Notify dead-letter event
		emitDeliveryDecisionEvent(events, decision, nil, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, nil, messageContent)
	}
	policyInput.Info = *info
	senderSimpleName := nodeaddr.Simple(info.From)
	recipientSimpleName := nodeaddr.Simple(info.To)

	// Guard: legitimate postman traffic no longer traverses post/, so any
	// generic from=postman file is a forgery and must be dead-lettered.
	if info.From == "postman" {
		decision := planDeliveryPolicy(policyInput)
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
		log.Printf("postman: SECURITY: forged sender %q in session %q via generic post/ path — dead-lettering %s\n",
			info.From, sourceSessionName, filename)
		emitDeliveryDecisionEvent(events, decision, info, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
	}

	// Guard: "daemon" remains a reserved sender name only valid for messages
	// originating from the daemon's own session.
	if info.From == "daemon" && daemonSession != "" && sourceSessionName != daemonSession {
		decision := planDeliveryPolicy(policyInput)
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
		log.Printf("postman: SECURITY: forged sender %q in session %q (daemon session: %q) — dead-lettering %s\n",
			info.From, sourceSessionName, daemonSession, filename)
		emitDeliveryDecisionEvent(events, decision, info, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
	}

	// Issue #161: Validate frontmatter envelope (skip only for daemon-origin messages)
	if info.From != "daemon" {
		rawBytes, readErr := os.ReadFile(postPath)
		if os.IsNotExist(readErr) {
			return nil // Already processed (duplicate event)
		}
		if readErr != nil {
			log.Printf("postman: WARNING: failed to read message for envelope validation %s: %v\n", filename, readErr)
		} else {
			messageContent = string(rawBytes)
			envFrom, envTo, parseErr := parseEnvelopeFromTo(string(rawBytes))
			policyInput.EnvelopeChecked = true
			policyInput.EnvelopeMismatch = parseErr != nil || envFrom != info.From || envTo != info.To
			if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
				dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
				if decision.SendDeadLetterNotification {
					sendDeadLetterNotification(sourceSessionDir, contextID, senderSimpleName, decision.DeadLetterReason, filename, filepath.Base(dst))
				}
				emitDeliveryDecisionEvent(events, decision, info, filename)
				return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
			}
		}
	}

	if messageContent == "" {
		rawBytes, readErr := os.ReadFile(postPath)
		if readErr == nil {
			messageContent = string(rawBytes)
		} else if !os.IsNotExist(readErr) {
			log.Printf("postman: WARNING: failed to read message content %s: %v\n", filename, readErr)
		}
	}

	// Resolve recipient name (Issue #33: session-aware adjacency)
	recipientResolution := resolveRuntimeNode(info.To, sourceSessionName, knownNodes)
	policyInput.RecipientResolved = true
	policyInput.RecipientResolution = recipientResolution
	recipientFullName := recipientResolution.Address
	if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
		if decision.SendDeadLetterNotification {
			sendDeadLetterNotification(sourceSessionDir, contextID, senderSimpleName, decision.DeadLetterReason, filename, filepath.Base(dst))
		}
		// Issue #53: Notify dead-letter event
		emitDeliveryDecisionEvent(events, decision, info, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
	}
	nodeInfo := knownNodes[recipientFullName]

	// F4: Delivery-time session boundary check.
	// Reject delivery to a recipient whose session is neither the daemon's own session
	// nor explicitly enabled. This is the last-resort safety net against 混信.
	policyInput.RecipientForeign = daemonSession != "" && nodeInfo.SessionName != daemonSession && !isSessionEnabled(nodeInfo.SessionName)
	if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
		if decision.SendDeadLetterNotification {
			sendDeadLetterNotification(sourceSessionDir, contextID, senderSimpleName, decision.DeadLetterReason, filename, filepath.Base(dst))
		}
		log.Printf("postman: F4: dead-lettering %s — recipient session %q is foreign (daemon session: %q)\n", filename, nodeInfo.SessionName, daemonSession)
		emitDeliveryDecisionEvent(events, decision, info, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
	}

	// Resolve sender name (Issue #33: session-aware adjacency)
	senderResolution := resolveRuntimeNode(info.From, sourceSessionName, knownNodes)
	policyInput.SenderResolved = true
	policyInput.SenderResolution = senderResolution
	senderFullName := senderResolution.Address
	if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
		// Issue #53: Notify dead-letter event
		emitDeliveryDecisionEvent(events, decision, info, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
	}

	// Check routing permissions (DEFAULT DENY)
	// IMPORTANT: sender="daemon" is always allowed (#172)
	if info.From != "daemon" {
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
		policyInput.RoutingChecked = true
		policyInput.RoutingAllowed = allowed
		if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
			// Issue #80: Send warning message back to sender
			if decision.SendRoutingWarning {
				writeRoutingDeniedWarning(sourceSessionDir, contextID, info, senderSimpleName, senderFullName, adjacency, cfg)
			}

			// Routing denied: move to dead-letter/ in source session
			dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
			log.Printf("📨 postman: routing denied %s -> %s (moved to dead-letter/)\n", info.From, info.To)
			// Issue #53: Notify dead-letter event
			emitDeliveryDecisionEvent(events, decision, info, filename)
			return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
		}
	}

	// Check session enabled/disabled state
	// Extract sender and recipient session names
	senderSessionName := sourceSessionName
	recipientSessionName := nodeInfo.SessionName

	// Both sessions must be enabled (unless sender is daemon)
	if info.From != "daemon" {
		policyInput.SenderSessionChecked = true
		policyInput.SenderSessionEnabled = isSessionEnabled(senderSessionName)
		if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
			dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
			log.Printf("📨 postman: sender session %s disabled (moved to dead-letter/)\n", senderSessionName)
			if decision.SendDeadLetterNotification {
				sendDeadLetterNotification(sourceSessionDir, contextID, senderSimpleName, decision.DeadLetterReason, filename, filepath.Base(dst))
			}
			// Issue #53: Notify dead-letter event
			emitDeliveryDecisionEvent(events, decision, info, filename)
			return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
		}
	}
	if info.From != "daemon" {
		policyInput.RecipientSessionChecked = true
		policyInput.RecipientSessionEnabled = isSessionEnabled(recipientSessionName)
		if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
			dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
			log.Printf("📨 postman: recipient session %s disabled (moved to dead-letter/)\n", recipientSessionName)
			if decision.SendDeadLetterNotification {
				sendDeadLetterNotification(sourceSessionDir, contextID, senderSimpleName, decision.DeadLetterReason, filename, filepath.Base(dst))
			}
			// Issue #53: Notify dead-letter event
			emitDeliveryDecisionEvent(events, decision, info, filename)
			return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
		}
	}

	// Ensure recipient inbox subdirectory exists (in recipient's session directory)
	recipientSessionDir := nodeInfo.SessionDir
	recipientInbox := filepath.Join(recipientSessionDir, "inbox", recipientSimpleName)

	// Enforce inbox queue cap: dead-letter overflow beyond inboxQueueCap.
	// Protects agent-session nodes from unbounded queue growth (#agent-session).
	if count, countErr := countInboxMessages(recipientInbox); countErr == nil {
		policyInput.QueueChecked = true
		policyInput.QueueCount = count
		if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
			dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
			if decision.SendDeadLetterNotification {
				sendDeadLetterNotification(sourceSessionDir, contextID, senderSimpleName, decision.DeadLetterReason, filename, filepath.Base(dst))
			}
			log.Printf("postman: inbox queue full for %s (cap=%d, current=%d): dead-lettering %s\n", info.To, inboxQueueCap, count, filename)
			emitDeliveryDecisionEvent(events, decision, info, filename)
			return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
		}
	}

	dst, err := store.DeliverPostToInbox(postPath, recipientInbox, filename)
	if err != nil {
		return err
	}
	recordMailboxProjectionPayload(sourceSessionDir, sourceSessionName, projection.MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: filename,
		From:      info.From,
		To:        info.To,
		ThreadID:  mailboxThreadIDFromContent(messageContent),
		Path:      shadowRelativePath(sourceSessionDir, postPath),
		Content:   messageContent,
	})
	recordMailboxProjectionPayload(recipientSessionDir, recipientSessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: filename,
		From:      info.From,
		To:        info.To,
		ThreadID:  mailboxThreadIDFromContent(messageContent),
		Path:      shadowRelativePath(recipientSessionDir, dst),
		Content:   messageContent,
	})
	now := time.Now()
	recordApprovalEventForDelivery(
		sourceSessionDir,
		sourceSessionName,
		recipientSessionDir,
		recipientSessionName,
		filename,
		info.From,
		info.To,
		messageContent,
		now,
	)
	syncMailboxProjection(sourceSessionDir)
	if recipientSessionDir != sourceSessionDir {
		syncMailboxProjection(recipientSessionDir)
	}

	// Send tmux notification to the recipient pane
	// Issue #84: Get liveness map for talks_to_line filtering
	livenessMap := idleTracker.GetLivenessMap()
	sendDeliveryNotification(controlplane.TargetForNode(info.To, nodeInfo), cfg, adjacency, knownNodes, contextID, info.To, info.From, sourceSessionName, postPath, livenessMap)
	// NOTE: Error already logged by SendToPane (WARNING level)
	// Continue with delivery (notification failure does not fail delivery)

	// Update activity timestamps for idle detection (Issue #55)
	// NOTE: Exclude daemon system messages from activity tracking.
	// Both UpdateSendActivity and UpdateReceiveActivity skip daemon senders
	// to prevent system-delivered messages from causing false reply-lag state.
	// Issue #79: Use session-prefixed keys for tracking
	if info.From != "daemon" {
		idleTracker.UpdateSendActivity(senderFullName)
	}
	if info.From != "daemon" {
		idleTracker.UpdateReceiveActivity(recipientFullName)
	}

	// Delivery latency logging (#179): parse message timestamp and log age.
	if msgTime, err := time.Parse("20060102-150405", info.Timestamp); err == nil {
		age := time.Since(msgTime)
		log.Printf("📬 postman: delivered %s -> %s (age: %s)\n", filename, info.To, age.Truncate(time.Second))
	} else {
		log.Printf("📬 postman: delivered %s -> %s\n", filename, info.To)
	}
	return nil
}

func sendDeliveryNotification(target controlplane.Target, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, contextID, recipient, sender, sourceSessionName, notificationPath string, livenessMap map[string]bool) {
	recipientSimpleName := nodeaddr.Simple(recipient)
	notificationMsg := notification.BuildNotification(cfg, adjacency, knownNodes, contextID, recipient, sender, sourceSessionName, notificationPath, livenessMap)
	nodeEnterDelay := cfg.GetNodeConfig(recipientSimpleName).EnterDelay
	enterDelay := time.Duration(cfg.EnterDelay * float64(time.Second))
	if nodeEnterDelay != 0 {
		enterDelay = time.Duration(nodeEnterDelay * float64(time.Second))
	}
	tmuxTimeout := time.Duration(cfg.TmuxTimeout * float64(time.Second))
	verifyDelay := time.Duration(cfg.EnterVerifyDelay * float64(time.Second))
	adapter, err := controlplane.DefaultHandAdapter(target)
	if err != nil {
		log.Printf("postman: WARNING: failed to select hand adapter for %s: %v\n", target.RunID, err)
		return
	}
	delivery := controlplane.PaneDelivery{
		Content:        notificationMsg,
		EnterDelay:     enterDelay,
		TmuxTimeout:    tmuxTimeout,
		EnterCount:     cfg.GetNodeConfig(recipientSimpleName).EnterCount,
		BypassCooldown: true,
		VerifyDelay:    verifyDelay,
		MaxRetries:     cfg.EnterRetryMax,
	}
	log.Printf("postman: notification: attempting pane delivery to %s (pane=%s session=%s msg=%s)\n", recipient, target.Hand.Address, target.SessionName, filepath.Base(notificationPath))
	deliverNotificationWithRetry(adapter, target, delivery, recipient, knownNodes, filepath.Base(notificationPath))
}

// deliverNotificationWithRetry attempts adapter.Deliver and, on failure, retries
// once using a refreshed pane ID from knownNodes when available. Extracted for
// testability: callers can inject a TmuxHandAdapter with a mock SendToPane.
func deliverNotificationWithRetry(adapter controlplane.HandAdapter, target controlplane.Target, delivery controlplane.PaneDelivery, recipient string, knownNodes map[string]discovery.NodeInfo, filename string) {
	if err := adapter.Deliver(target, delivery); err != nil {
		// Retry once: look up a potentially refreshed PaneID from knownNodes (the
		// daemon's discovery loop may have updated it since goroutine launch).
		retryTarget := target
		if knownNodes != nil {
			if freshInfo, ok := knownNodes[target.RunID]; ok && freshInfo.PaneID != "" && freshInfo.PaneID != target.Hand.Address {
				retryTarget = controlplane.TargetForNode(recipient, freshInfo)
			}
		}
		if retryErr := adapter.Deliver(retryTarget, delivery); retryErr != nil {
			log.Printf("postman: WARNING: pane notification failed: node=%s pane=%s session=%s msg=%s err=%v\n", recipient, retryTarget.Hand.Address, retryTarget.SessionName, filename, retryErr)
			return
		}
	}
	log.Printf("postman: notification: pane delivery succeeded for %s (pane=%s msg=%s)\n", recipient, target.Hand.Address, filename)
}

func DeliverSystemMessageDirect(filename string, nodeInfo discovery.NodeInfo, recipient, sender, contextID, content string, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, livenessMap map[string]bool) error {
	_, err := DeliverSystemMessageDirectResult(filename, nodeInfo, recipient, sender, contextID, content, cfg, adjacency, knownNodes, livenessMap)
	return err
}

func DeliverSystemMessageDirectResult(filename string, nodeInfo discovery.NodeInfo, recipient, sender, contextID, content string, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, livenessMap map[string]bool) (controlplane.SystemMessageResult, error) {
	return DeliverSystemMessageDirectResultToTarget(filename, controlplane.TargetForNode(recipient, nodeInfo), sender, contextID, content, cfg, adjacency, knownNodes, livenessMap)
}

func DeliverSystemMessageDirectToTarget(filename string, target controlplane.Target, sender, contextID, content string, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, livenessMap map[string]bool) error {
	_, err := DeliverSystemMessageDirectResultToTarget(filename, target, sender, contextID, content, cfg, adjacency, knownNodes, livenessMap)
	return err
}

func DeliverSystemMessageDirectResultToTarget(filename string, target controlplane.Target, sender, contextID, content string, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, livenessMap map[string]bool) (controlplane.SystemMessageResult, error) {
	if err := nodeaddr.Validate(target.ActorID); err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("invalid recipient address: %w", err)
	}
	adapter, err := controlplane.DefaultHandAdapter(target)
	if err != nil {
		return controlplane.SystemMessageResult{}, fmt.Errorf("selecting hand adapter: %w", err)
	}
	result, err := adapter.DeliverSystemMessage(target, controlplane.SystemMessageDelivery{
		Filename:        filename,
		Sender:          sender,
		ThreadID:        mailboxThreadIDFromContent(content),
		Content:         content,
		QueueCap:        inboxQueueCap,
		QueueFullSuffix: dlSuffixQueueFull,
	})
	if err != nil {
		return controlplane.SystemMessageResult{}, err
	}
	if !result.Delivered {
		return result, nil
	}

	notificationPath := target.PostPath(filename)
	sendDeliveryNotification(target, cfg, adjacency, knownNodes, contextID, target.ActorID, sender, target.SessionName, notificationPath, livenessMap)
	log.Printf("📬 postman: delivered %s -> %s\n", filename, target.ActorID)
	return result, nil
}

// countInboxMessages returns the number of .md files in an inbox directory.
// Returns 0, nil if the directory does not exist (empty inbox is not an error).
func countInboxMessages(inboxDir string) (int, error) {
	return store.CountInboxMessages(inboxDir)
}

func shadowRelativePath(sessionDir, fullPath string) string {
	return store.ShadowRelativePath(sessionDir, fullPath)
}

// sendDeadLetterNotification writes a dead-letter notification directly to the
// sender's inbox. Bypasses post/ to avoid re-triggering the daemon delivery loop.
// Pattern follows the routing-denied notification at DeliverMessage:162-175.
// Issue #208: Extended with dead-letter path and recovery guidance.
// deadLetterBasename is the actual basename of the dead-letter file (after rename).
func sendDeadLetterNotification(sessionDir, contextID, senderNode, reason, originalFilename, deadLetterBasename string) {
	senderSimpleName := nodeaddr.Simple(senderNode)
	senderInbox := filepath.Join(sessionDir, "inbox", senderSimpleName)
	if mkErr := os.MkdirAll(senderInbox, 0o700); mkErr != nil {
		log.Printf("postman: WARNING: failed to create dead-letter notification inbox for %s: %v\n", senderNode, mkErr)
		return
	}
	now := time.Now()
	ts := now.Format("20060102-150405")
	filename := fmt.Sprintf("%s-from-postman-to-%s.md", ts, senderSimpleName)

	// Build dead-letter file path for reference
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", deadLetterBasename)

	content := fmt.Sprintf(
		"---\nparams:\n  contextId: %s\n  from: postman\n  to: %s\n  timestamp: %s\n  messageType: dead_letter_notification\n---\n\n## Dead-letter Notification\n\nYour message %q was not delivered.\nReason: %s\n\nDead-letter path: %s\n\nRecovery: inspect the dead-letter file above, then send a corrected message with the heredoc-explicit command and quoted delimiter:\ntmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'\n<corrected message>\nPOSTMAN_BODY\n",
		contextID,
		senderSimpleName,
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
	metadata, err := ParseEnvelopeMetadata(content)
	if err != nil {
		return "", "", err
	}
	return metadata.From, metadata.To, nil
}

// ParseEnvelopeMetadata extracts selected fields from the params block inside
// a message frontmatter envelope.
func ParseEnvelopeMetadata(content string) (EnvelopeMetadata, error) {
	return envelope.ParseMetadata(content)
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
			info, _ := ParseMessageFilename(entry.Name())
			content, readErr := os.ReadFile(src)
			if readErr != nil && !os.IsNotExist(readErr) {
				log.Printf("postman: WARNING: failed to read stale post payload %s: %v\n", src, readErr)
			}
			from := ""
			to := ""
			if info != nil {
				from = info.From
				to = info.To
			}
			if err := moveToDeadLetterWithProjection(sessionDir, filepath.Base(sessionDir), src, dst, entry.Name(), from, to, string(content)); err == nil {
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
		info.Filename = entry.Name()
		messages = append(messages, *info)
	}

	return messages
}

func ArchiveInboxMessage(absPath, filename string) (string, error) {
	return store.ArchiveInboxMessage(absPath, filename)
}
