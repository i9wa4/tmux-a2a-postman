package message

import (
	"crypto/rand"
	"crypto/sha256"
	"errors"
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
	"github.com/i9wa4/tmux-a2a-postman/internal/msgtrace"
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
	deadLetterReasonMissingEvidence          = "missing-evidence"

	// DeadLetterReasonPopVerificationExhausted is exported (unlike its
	// siblings above): internal/daemon's handleDaemonSubmitPop applies it
	// directly via store.PlanDeadLetterMessage / a dedicated atomic write
	// helper (#755 F-013). That failure is detected at pop time on the
	// recipient's own already-delivered inbox, not during the post-to-inbox
	// delivery pipeline planDeliveryPolicy models, so planDeliveryPolicy
	// itself cannot produce it -- only this naming convention is shared
	// with it.
	DeadLetterReasonPopVerificationExhausted = "pop verification failed repeatedly"
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
	dlSuffixMissingEvidence  = "-dl-missing-evidence"

	// DlSuffixPopVerificationExhausted is exported for the same reason as
	// DeadLetterReasonPopVerificationExhausted above (#755 F-013).
	DlSuffixPopVerificationExhausted = "-dl-pop-verification-exhausted"
)

// inboxQueueCap is the maximum number of messages allowed in a recipient inbox
// before overflow messages are sent to dead-letter (agent-session queue guard).
const inboxQueueCap = 20

const (
	deadLetterReasonQueueFull = "inbox queue full"
	dlSuffixQueueFull         = "-dl-queue-full"
)

// errInboxCountFailed (C-5) identifies DeliverMessage's fail-closed
// countInboxMessages error specifically, so a caller (and a test) can tell
// it apart from any other error that might arise elsewhere in the same
// admission-fence callback.
var errInboxCountFailed = errors.New("inbox message count failed")

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

func deliveryTraceFieldsFromContent(filename, messagePath, tmuxSession, contextID, content string, info *MessageInfo) msgtrace.Fields {
	fields := msgtrace.FromContent(filename, messagePath, tmuxSession, content)
	if fields.ContextID == "" {
		fields.ContextID = contextID
	}
	if info != nil {
		if fields.Sender == "" {
			fields.Sender = info.From
		}
		if fields.Recipient == "" {
			fields.Recipient = info.To
		}
	}
	return fields
}

func syncMailboxProjectionWithTrace(sessionDir string, fields msgtrace.Fields) {
	if fields.TmuxSession == "" {
		fields.TmuxSession = filepath.Base(sessionDir)
	}
	emitTrace := msgtrace.HasMessageContext(fields)
	if err := projection.SyncMailboxProjection(sessionDir); err != nil {
		fields.Result = "error"
		fields.Reason = err.Error()
		if emitTrace {
			msgtrace.Log("projection_sync", fields)
		}
		log.Printf("postman: WARNING: component=%s event=sync_failed session_dir=%s err=%v\n", projection.MailboxProjectionComponent, sessionDir, err)
		return
	}
	fields.Result = "ok"
	if emitTrace {
		msgtrace.Log("projection_sync", fields)
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

func approvalDecisionFromContent(content string) (journal.ApprovalDecision, string, bool) {
	body := messageBodyFromContent(content)
	if body == "" {
		return "", "", false
	}
	firstLine := body
	if idx := strings.Index(firstLine, "\n"); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)
	switch {
	case strings.HasPrefix(firstLine, "APPROVED:"):
		return journal.ApprovalDecisionApproved, strings.TrimSpace(strings.TrimPrefix(firstLine, "APPROVED:")), true
	case strings.HasPrefix(firstLine, "NOT APPROVED:"):
		return journal.ApprovalDecisionRejected, strings.TrimSpace(strings.TrimPrefix(firstLine, "NOT APPROVED:")), true
	default:
		return "", "", false
	}
}

type approvalDeliveryEvent struct {
	EventType string
	Payload   interface{}
	ThreadID  string
}

func approvalEventForDelivery(messageID, from, to, content string) (approvalDeliveryEvent, bool) {
	metadata, _ := envelope.ParseMetadata(content)
	threadID := metadata.ThreadID
	if threadID == "" {
		threadID = mailboxThreadIDFromContent(content)
	}
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
		decision, _, ok := approvalDecisionFromContent(content)
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
	case strings.HasPrefix(threadID, "command-approval-"):
		// #626: a reply to a command_approver_node-delivered command approval
		// request (execute-bash mints this exact thread id prefix via
		// commandApprovalThreadID). Reuses the same APPROVED:/NOT
		// APPROVED: body-prefix convention as the orchestrator/critic case
		// above, but records via #625's own CommandApproval* event types,
		// never the older ApprovalDecidedEventType/ApprovalDecisionPayload
		// pair used by that unrelated hardcoded flow.
		decision, reason, ok := approvalDecisionFromContent(content)
		if !ok {
			log.Printf("postman: WARNING: message %s on command approval thread %s did not start with APPROVED:/NOT APPROVED: — not recorded as a decision\n", messageID, threadID)
			return approvalDeliveryEvent{}, false
		}
		return approvalDeliveryEvent{
			EventType: journal.CommandApprovalDecidedEventType,
			Payload: journal.CommandApprovalDecisionPayload{
				Reviewer:       sender,
				Decision:       decision,
				Reason:         reason,
				MessageID:      messageID,
				InputRequestID: metadata.FillsInputRequestID,
				CommandHash:    metadata.CommandHash,
			},
			ThreadID: threadID,
		}, true
	default:
		return approvalDeliveryEvent{}, false
	}
}

func recordApprovalEvent(sessionDir, sessionName string, event approvalDeliveryEvent, now time.Time) {
	if event.EventType == journal.CommandApprovalDecidedEventType && commandApprovalDecisionAlreadyApplied(sessionDir, event, now) {
		return
	}
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
		return
	}
	if event.EventType == journal.CommandApprovalDecidedEventType {
		if err := journal.SyncCommandApprovalDecisionHistory(sessionDir); err != nil {
			log.Printf("postman: WARNING: command approval decision history sync failed: %v\n", err)
		}
	}
}

func commandApprovalDecisionAlreadyApplied(sessionDir string, event approvalDeliveryEvent, now time.Time) bool {
	payload, ok := event.Payload.(journal.CommandApprovalDecisionPayload)
	if !ok {
		return false
	}
	state, ok, err := projection.ProjectCommandApprovalState(sessionDir, now)
	if err != nil || !ok {
		return false
	}
	thread, found := state.Threads[event.ThreadID]
	if !found {
		return false
	}
	if thread.Status != projection.CommandApprovalStatusApproved && thread.Status != projection.CommandApprovalStatusRejected {
		return false
	}
	if thread.CommandApproverAddress == "" || thread.RequesterAddress == "" || payload.ReviewerAddress != thread.CommandApproverAddress || payload.RequesterAddress != thread.RequesterAddress {
		return false
	}
	if thread.InputRequestID != "" && payload.InputRequestID != "" && thread.InputRequestID != payload.InputRequestID {
		return false
	}
	if thread.CommandHash != "" && payload.CommandHash != "" && thread.CommandHash != payload.CommandHash {
		return false
	}
	return true
}

func recordApprovalEventForDelivery(sourceSessionDir, sourceSessionName, recipientSessionDir, recipientSessionName, messageID, from, to, content string, now time.Time) {
	event, ok := approvalEventForDelivery(messageID, from, to, content)
	if !ok {
		return
	}
	if event.EventType == journal.CommandApprovalDecidedEventType {
		event = withCommandApprovalDecisionAddresses(event, nodeaddr.Full(from, sourceSessionName), nodeaddr.Full(to, recipientSessionName))
		if !isTrustedCommandApprovalDecision(recipientSessionDir, recipientSessionName, sourceSessionName, messageID, from, to, content, now) {
			return
		}
	}

	recordApprovalEvent(sourceSessionDir, sourceSessionName, event, now)
	if recipientSessionDir != sourceSessionDir {
		recordApprovalEvent(recipientSessionDir, recipientSessionName, event, now)
	}
}

func withCommandApprovalDecisionAddresses(event approvalDeliveryEvent, reviewerAddress, requesterAddress string) approvalDeliveryEvent {
	payload, ok := event.Payload.(journal.CommandApprovalDecisionPayload)
	if !ok {
		return event
	}
	payload.ReviewerAddress = reviewerAddress
	payload.RequesterAddress = requesterAddress
	event.Payload = payload
	return event
}

// isTrustedCommandApprovalDecision is deliberately narrower than the legacy
// orchestrator/critic approval hook. It permits pre-denial correlation only
// for a decision on an existing command-approval request whose resolved
// approver and requester still match the envelope, and whose exact reply slot
// is named by fills_input_request_id. Routing remains default-deny.
func isTrustedCommandApprovalDecision(requesterSessionDir, requesterSessionName, reviewerSessionName, messageID, from, to, content string, now time.Time) bool {
	event, ok := approvalEventForDelivery(messageID, from, to, content)
	if !ok || event.EventType != journal.CommandApprovalDecidedEventType {
		return false
	}
	metadata, err := envelope.ParseMetadata(content)
	if err != nil || metadata.FillsInputRequestID == "" {
		return false
	}
	payload, ok := event.Payload.(journal.CommandApprovalDecisionPayload)
	if !ok {
		return false
	}
	state, ok, err := projection.ProjectCommandApprovalState(requesterSessionDir, now)
	if err != nil || !ok {
		return false
	}
	thread, found := state.Threads[event.ThreadID]
	if !found || thread.CommandHash == "" {
		return false
	}
	if thread.Status == projection.CommandApprovalStatusApproved || thread.Status == projection.CommandApprovalStatusRejected {
		return false
	}
	if thread.InputRequestID == "" || metadata.FillsInputRequestID != thread.InputRequestID || payload.InputRequestID != thread.InputRequestID {
		return false
	}
	if metadata.CommandHash == "" || metadata.CommandHash != thread.CommandHash || payload.CommandHash != thread.CommandHash {
		return false
	}
	return thread.CommandApproverAddress != "" &&
		thread.RequesterAddress != "" &&
		thread.CommandApproverAddress == nodeaddr.Full(from, reviewerSessionName) &&
		thread.RequesterAddress == nodeaddr.Full(to, requesterSessionName)
}

func moveToDeadLetterWithProjection(sessionDir, sessionName, srcPath, dstPath, messageID, from, to, content string) error {
	if err := moveToDeadLetter(srcPath, dstPath); err != nil {
		return err
	}
	finishDeadLetterRecord(sessionDir, sessionName, srcPath, dstPath, messageID, from, to, content)
	return nil
}

// finishDeadLetterRecord records the journal/projection/msgtrace side effects
// for an ALREADY-MOVED dead-letter file. Per the DeliverMessage locking
// protocol documented there (B-1), this must run only after every session
// roots gate held during the move itself has been released, since
// syncMailboxProjectionWithTrace below takes the exclusive roots gate
// internally on a generation change and would self-deadlock (or, for a
// different session's gate, needlessly nest) if called while a gate from the
// move is still held.
func finishDeadLetterRecord(sessionDir, sessionName, srcPath, dstPath, messageID, from, to, content string) {
	fields := msgtrace.FromContent(messageID, shadowRelativePath(sessionDir, dstPath), sessionName, content)
	if fields.Sender == "" {
		fields.Sender = from
	}
	if fields.Recipient == "" {
		fields.Recipient = to
	}
	fields.Reason = deadLetterFailureReason(dstPath)
	msgtrace.Log("dead_letter", fields)
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
	syncMailboxProjectionWithTrace(sessionDir, fields)
}

// aboutToRequestSessionGatesForTest fires, when set, immediately before
// withSessionRootsGates requests its first shared roots gate.
var aboutToRequestSessionGatesForTest func()

// insideSessionRootsGatesForTest fires, when set, immediately after
// withSessionRootsGates has acquired every gate it needs and is about to
// run the protected body -- i.e. the moment a caller has actually entered
// its gated section. Tests use a bounded wait-then-assert that this has NOT
// fired while an external holder still holds a conflicting gate (the same
// non-sleep, non-ordering-inference pattern established in
// internal/store/mailbox_roots_test.go's contenderStarted proof), never a
// sleep and never end-of-run ordering, which a scheduler can satisfy by
// coincidence even against a broken (no-op) gate (B-5).
var insideSessionRootsGatesForTest func()

// sortedDistinctSessionDirs returns {a} when a and b are the same directory,
// otherwise both in a deterministic (lexically sorted) order. Sorting gives
// every concurrent caller the same acquisition order for the same pair of
// sessions, which is what makes nesting two independent per-session gates
// deadlock-free. C-4: both inputs are run through filepath.Clean first, so
// two spellings of the same directory (e.g. a trailing slash or a "./"
// segment) always compare and dedupe identically -- without this, a valid
// local sender's dir could fail the "same directory" check against its own
// differently-spelled session dir and be treated as foreign, and two
// concurrent callers could compute different acquisition orders for what is
// actually the same pair of directories.
func sortedDistinctSessionDirs(a, b string) []string {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return []string{a}
	}
	if a < b {
		return []string{a, b}
	}
	return []string{b, a}
}

// withSessionRootsGates acquires store.WithMailboxRootsShared for the source
// and recipient session directories, in sortedDistinctSessionDirs order
// (once, if they are the same directory), then runs fn. It is DeliverMessage
// and its dead-letter/notification bypass-writer helpers' entry point for
// their own mutation paths in this slice (P2-2a); it does not cover every
// writer in this package -- DrainStalePost's dead-letter move is a known,
// separately-owned gap (see its own doc comment). It must never be called
// while any session roots gate or admission fence is already held by the
// caller: nesting here is between two DIFFERENT sessions' gates only, never
// a second acquisition of a gate/fence the caller itself already holds.
func withSessionRootsGates(sourceSessionDir, recipientSessionDir string, fn func() error) error {
	if aboutToRequestSessionGatesForTest != nil {
		aboutToRequestSessionGatesForTest()
	}
	protected := fn
	if insideSessionRootsGatesForTest != nil {
		protected = func() error {
			insideSessionRootsGatesForTest()
			return fn()
		}
	}
	gate := func(dir string, next func() error) error {
		if skipSessionRootsGateForDirForTest != nil && skipSessionRootsGateForDirForTest(dir) {
			return next()
		}
		return store.WithMailboxRootsShared(dir, next)
	}
	dirs := sortedDistinctSessionDirs(sourceSessionDir, recipientSessionDir)
	if len(dirs) == 1 {
		return gate(dirs[0], protected)
	}
	return gate(dirs[0], func() error {
		return gate(dirs[1], protected)
	})
}

// skipSessionRootsGateForDirForTest, when set, reports whether
// withSessionRootsGates should skip acquiring the real gate for a specific
// (already-cleaned) session directory, running the protected body directly
// for that directory instead. It exists solely for the C-1(c) demonstration
// that a SPECIFIC session's gate -- not just "some" gate -- is load-bearing
// for a given call site: a test removes only the source directory's gate
// (leaving the recipient's real) and confirms the cross-session blocking
// test then fails, which a blanket no-op-lock patch cannot distinguish.
var skipSessionRootsGateForDirForTest func(dir string) bool

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
		Details: map[string]interface{}{
			"failure_reason": decision.EventReason,
		},
	}
}

func deadLetterDecisionDestination(sessionDir, filename string, decision deliveryDecision) string {
	return deadLetterDst(sessionDir, filename, decision.DeadLetterSuffix)
}

// moveToDeadLetterForDecision performs DeliverMessage's dead-letter move
// under the SOURCE session's roots gate ONLY (C-2): a dead-letter move
// touches only sessionDir's own post/ and dead-letter/ directories, never
// the recipient's, so gating the recipient session here would let a
// blocked, invalid, or missing recipient session stall or fail source
// dead-lettering -- the message would stay in post/ and retry indefinitely
// -- and would create lock state in a foreign session for no reason. The
// gate is released before the journal/projection side effects run
// (finishDeadLetterRecord), never held across them. The ordinary
// (non-dead-letter) cross-session delivery rename in DeliverMessage is
// unaffected by this and still takes both sorted session gates, since that
// rename genuinely touches both session directories.
func moveToDeadLetterForDecision(sessionDir, sessionName, postPath, dst, filename string, info *MessageInfo, content string) error {
	from, to := "", ""
	if info != nil {
		from = info.From
		to = info.To
	}
	if err := withSessionRootsGates(sessionDir, sessionDir, func() error {
		return moveToDeadLetter(postPath, dst)
	}); err != nil {
		return err
	}
	finishDeadLetterRecord(sessionDir, sessionName, postPath, dst, filename, from, to, content)
	return nil
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

// P2-2a (docs/design/mailbox-overflow-policy.md §2.1): by the time this is
// called, senderFullName has already been through resolveRuntimeNode
// (DeliverMessage's sender-resolution check) and confirmed non-dead-letter,
// so it is already known; this re-validates anyway for defense in depth
// against any future caller that skips that path. The write is serialized
// under this recipient's admission fence (claim-only; no sequence is
// issued here), preceded by the session-level mailbox roots gate taken
// SHARED, per the documented lock order.
// writeRoutingDeniedWarning writes a best-effort edge-violation warning back
// into the sender's own inbox under sourceSessionDir. B-4: senderFullName is
// the already-resolved full address (resolveRuntimeNode was run on it by
// the caller), but the resolution is re-validated here, including that the
// resolved node's own SessionDir equals sourceSessionDir -- the session this
// warning writes into -- rejecting a sender that resolves to a DIFFERENT
// session's node even if it shares senderSimpleName with a local one. B-3:
// the target inbox's message count is read under the same admission fence
// as the write and the write is suppressed (never dead-lettered or
// recursed) at inboxQueueCap; a durable, cap-independent signal is deferred
// to slice (c).
func writeRoutingDeniedWarning(sourceSessionDir, contextID string, info *MessageInfo, senderSimpleName, senderFullName string, adjacency map[string][]string, cfg *config.Config, knownNodes map[string]discovery.NodeInfo, sourceSessionName string) directInboxWriteOutcome {
	resolution := resolveRuntimeNode(senderFullName, sourceSessionName, knownNodes)
	if !resolution.Found {
		log.Printf("postman: routing-denied warning skipped: sender %q is not a known node\n", senderFullName)
		return directInboxWriteOutcomeSkippedUnknownSender
	}
	resolvedNode, ok := knownNodes[resolution.Address]
	// C-4: compare canonicalized paths (see sortedDistinctSessionDirs) so a
	// differently-spelled but identical session dir is never mistaken for a
	// foreign one.
	if !ok || filepath.Clean(resolvedNode.SessionDir) != filepath.Clean(sourceSessionDir) {
		log.Printf("postman: routing-denied warning skipped: sender %q resolved outside the target session (resolved session %q)\n", senderFullName, resolvedNode.SessionName)
		return directInboxWriteOutcomeSkippedForeignSession
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

	// Best-effort write, matching the prior unconditional os.WriteFile's
	// discarded error.
	senderInbox := filepath.Join(sourceSessionDir, "inbox", senderSimpleName)
	result := directInboxWriteOutcomeWriteError
	fenceErr := withSessionRootsGates(sourceSessionDir, sourceSessionDir, func() error {
		_, err := store.WithAdmissionFence(sourceSessionDir, senderSimpleName, func(*store.AdmissionHandle) error {
			count, countErr := countInboxMessages(senderInbox)
			if countErr != nil {
				log.Printf("postman: WARNING: routing-denied warning skipped for %s: inbox count failed: %v\n", senderFullName, countErr)
				result = directInboxWriteOutcomeCountError
				return nil
			}
			if count >= inboxQueueCap {
				log.Printf("postman: routing-denied warning suppressed at cap: sender=%s attempted_recipient=%s (cap=%d, current=%d)\n", senderFullName, info.To, inboxQueueCap, count)
				result = directInboxWriteOutcomeSuppressedAtCap
				return nil
			}
			if mkErr := os.MkdirAll(senderInbox, 0o700); mkErr != nil {
				return mkErr
			}
			warnPath := filepath.Join(senderInbox, warnFilename)
			if writeErr := os.WriteFile(warnPath, []byte(warnContent), 0o600); writeErr != nil {
				return writeErr
			}
			result = directInboxWriteOutcomeWritten
			return nil
		})
		return err
	})
	if fenceErr != nil {
		log.Printf("postman: WARNING: failed to write routing-denied warning for %s: %v\n", senderFullName, fenceErr)
		return directInboxWriteOutcomeWriteError
	}
	return result
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
			metadata, parseErr := ParseEnvelopeMetadata(string(rawBytes))
			envFrom, envTo := "", ""
			if parseErr == nil {
				envFrom, envTo = metadata.From, metadata.To
			}
			policyInput.EnvelopeChecked = true
			policyInput.EnvelopeMismatch = parseErr != nil || envFrom != info.From || envTo != info.To
			if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
				dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
				if decision.SendDeadLetterNotification {
					sendDeadLetterNotification(sourceSessionDir, contextID, info.From, decision.DeadLetterReason, filename, filepath.Base(dst), knownNodes, sourceSessionName)
				}
				emitDeliveryDecisionEvent(events, decision, info, filename)
				return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
			}
			policyInput.EvidencePresenceGateChecked = true
			observedAt := evidenceGateObservedAt(sourceSessionDir, sourceSessionName, filename, postPath, time.Now().UTC())
			policyInput.EvidencePresenceGateActive = cfg.EvidencePresenceGateActiveAt(observedAt)
			senderBody, senderBodyExact := envelope.SenderBodyFromTrustedContent(messageContent, filename)
			policyInput.CompletionClaim = senderBodyExact && isCompletionClaim(senderBody)
			policyInput.EvidencePresent = hasEvidenceReplayContract(metadata)
			if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
				dst := deadLetterDecisionDestination(sourceSessionDir, filename, decision)
				if decision.SendDeadLetterNotification {
					sendDeadLetterNotification(sourceSessionDir, contextID, info.From, decision.DeadLetterReason, filename, filepath.Base(dst), knownNodes, sourceSessionName)
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
			sendDeadLetterNotification(sourceSessionDir, contextID, info.From, decision.DeadLetterReason, filename, filepath.Base(dst), knownNodes, sourceSessionName)
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
			sendDeadLetterNotification(sourceSessionDir, contextID, info.From, decision.DeadLetterReason, filename, filepath.Base(dst), knownNodes, sourceSessionName)
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

	// Authenticate both session endpoints before considering the narrow
	// command-approval reverse-path exception below. A disabled session never
	// gains pre-denial journal effects.
	if info.From != "daemon" && !isSessionEnabled(sourceSessionName) {
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, deliveryDecision{DeadLetterSuffix: dlSuffixSessionDisabled})
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
	}
	if info.From != "daemon" && !isSessionEnabled(nodeInfo.SessionName) {
		dst := deadLetterDecisionDestination(sourceSessionDir, filename, deliveryDecision{DeadLetterSuffix: dlSuffixSessionDisabled})
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
			// A command-approval decision is correlated to a trusted request by
			// thread state and reviewer identity during projection. Record it
			// before ordinary graph policy dead-letters this nonadjacent reply;
			// ordinary mail itself remains denied and never reaches the requester.
			if decision.DeadLetterSuffix == dlSuffixRoutingDenied && isTrustedCommandApprovalDecision(nodeInfo.SessionDir, nodeInfo.SessionName, sourceSessionName, filename, info.From, info.To, messageContent, time.Now()) {
				recordApprovalEventForDelivery(sourceSessionDir, sourceSessionName, nodeInfo.SessionDir, nodeInfo.SessionName, filename, info.From, info.To, messageContent, time.Now())
			}
			// Issue #80: Send warning message back to sender
			if decision.SendRoutingWarning {
				writeRoutingDeniedWarning(sourceSessionDir, contextID, info, senderSimpleName, senderFullName, adjacency, cfg, knownNodes, sourceSessionName)
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
				sendDeadLetterNotification(sourceSessionDir, contextID, info.From, decision.DeadLetterReason, filename, filepath.Base(dst), knownNodes, sourceSessionName)
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
				sendDeadLetterNotification(sourceSessionDir, contextID, info.From, decision.DeadLetterReason, filename, filepath.Base(dst), knownNodes, sourceSessionName)
			}
			// Issue #53: Notify dead-letter event
			emitDeliveryDecisionEvent(events, decision, info, filename)
			return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, dst, filename, info, messageContent)
		}
	}

	// Ensure recipient inbox subdirectory exists (in recipient's session directory)
	recipientSessionDir := nodeInfo.SessionDir
	recipientInbox := filepath.Join(recipientSessionDir, "inbox", recipientSimpleName)

	// P2-2a (docs/design/mailbox-overflow-policy.md §2.1): the queue-cap
	// decision and the actual inbox delivery below must be one atomic
	// operation under this recipient's admission fence, preceded by shared
	// mailbox roots gates on BOTH sourceSessionDir and recipientSessionDir
	// (sortedDistinctSessionDirs order, once if the same directory -- B-1):
	// DeliverPostToInbox below renames the file out of sourceSessionDir's
	// post/ directly into recipientSessionDir's inbox/ in one os.Rename, so
	// a concurrent quarantine transition on EITHER session must be excluded,
	// not just the recipient's. Without the fence itself, two concurrent
	// deliveries could both observe room under the cap and both admit,
	// overflowing it -- the exact TOCTOU race the fence exists to close.
	// recipientSimpleName was already confirmed against knownNodes via
	// resolveRuntimeNode above (see recipientResolution) and the F4
	// foreign-session check, so known-node validation always happens before
	// this fence is ever requested. This slice uses the fence as a pure
	// mutual-exclusion primitive (claim-only): it does not call
	// NextAdmissionSequence/MarkCommitted, so no admission sequence is
	// issued or consumed here; assigning and using a durable FIFO sequence
	// is deferred to a dedicated future slice. B-2: a countInboxMessages
	// failure fails CLOSED -- the message is left in post/ (no move is ever
	// attempted) and the error propagates to the caller for a retry,
	// matching docs/design/mailbox-overflow-policy.md §2.1's fail-closed
	// rule; it must never silently skip the cap check and admit.
	var (
		deadLetterDecision deliveryDecision
		deadLetterDst      string
		deadLettered       bool
		deliveredDst       string
	)
	fenceErr := withSessionRootsGates(sourceSessionDir, recipientSessionDir, func() error {
		_, err := store.WithAdmissionFence(recipientSessionDir, recipientSimpleName, func(*store.AdmissionHandle) error {
			// Enforce inbox queue cap: dead-letter overflow beyond inboxQueueCap.
			// Protects agent-session nodes from unbounded queue growth (#agent-session).
			count, countErr := countInboxMessages(recipientInbox)
			if countErr != nil {
				log.Printf("postman: WARNING: failed to count inbox messages for %s: %v (failing closed, message stays in post/)\n", recipientSimpleName, countErr)
				// C-5: wrap with errInboxCountFailed so a caller (and a test)
				// can tell "fail-closed counting" apart from any other error
				// that might arise later in this fence callback -- a bare
				// propagated countErr would be indistinguishable from, say,
				// an error from the later rename hitting the same broken
				// path, which could pass a test meant to prove this specific
				// branch is reached.
				return fmt.Errorf("%w for %s: %v", errInboxCountFailed, recipientSimpleName, countErr)
			}
			policyInput.QueueChecked = true
			policyInput.QueueCount = count
			if decision := planDeliveryPolicy(policyInput); decision.Action == deliveryActionDeadLetter {
				deadLetterDecision = decision
				deadLetterDst = deadLetterDecisionDestination(sourceSessionDir, filename, decision)
				deadLettered = true
				return nil
			}
			dst, err := store.DeliverPostToInbox(postPath, recipientInbox, filename)
			if err != nil {
				return err
			}
			deliveredDst = dst
			return nil
		})
		return err
	})
	if fenceErr != nil {
		return fenceErr
	}
	if deadLettered {
		if deadLetterDecision.SendDeadLetterNotification {
			sendDeadLetterNotification(sourceSessionDir, contextID, info.From, deadLetterDecision.DeadLetterReason, filename, filepath.Base(deadLetterDst), knownNodes, sourceSessionName)
		}
		log.Printf("postman: inbox queue full for %s (cap=%d, current=%d): dead-lettering %s\n", info.To, inboxQueueCap, policyInput.QueueCount, filename)
		emitDeliveryDecisionEvent(events, deadLetterDecision, info, filename)
		return moveToDeadLetterForDecision(sourceSessionDir, sourceSessionName, postPath, deadLetterDst, filename, info, messageContent)
	}
	dst := deliveredDst
	resultFields := deliveryTraceFieldsFromContent(filename, shadowRelativePath(recipientSessionDir, dst), recipientSessionName, contextID, messageContent, info)
	resultFields.DeliveryAttempt = 1
	resultFields.Result = "delivered"
	msgtrace.Log("delivery_result", resultFields)
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
	sourceProjectionFields := deliveryTraceFieldsFromContent(filename, shadowRelativePath(sourceSessionDir, postPath), sourceSessionName, contextID, messageContent, info)
	sourceProjectionFields.DeliveryAttempt = 1
	syncMailboxProjectionWithTrace(sourceSessionDir, sourceProjectionFields)
	if recipientSessionDir != sourceSessionDir {
		recipientProjectionFields := deliveryTraceFieldsFromContent(filename, shadowRelativePath(recipientSessionDir, dst), recipientSessionName, contextID, messageContent, info)
		recipientProjectionFields.DeliveryAttempt = 1
		syncMailboxProjectionWithTrace(recipientSessionDir, recipientProjectionFields)
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

// DeliveryNotificationObservation exposes the real notification constructed
// after a direct system-message delivery. It is deliberately observation-only:
// the pane adapter remains on the production path.
type DeliveryNotificationObservation struct {
	Target            controlplane.Target
	Recipient         string
	Sender            string
	SourceSessionName string
	NotificationPath  string
	Message           string
}

var deliveryNotificationObserverForTest func(DeliveryNotificationObservation)

// SetDeliveryNotificationObserverForTest observes a notification built by the
// real direct-delivery path and returns a restoration closure.
func SetDeliveryNotificationObserverForTest(observer func(DeliveryNotificationObservation)) func() {
	previous := deliveryNotificationObserverForTest
	deliveryNotificationObserverForTest = observer
	return func() { deliveryNotificationObserverForTest = previous }
}

func sendDeliveryNotification(target controlplane.Target, cfg *config.Config, adjacency map[string][]string, knownNodes map[string]discovery.NodeInfo, contextID, recipient, sender, sourceSessionName, notificationPath string, livenessMap map[string]bool) {
	recipientSimpleName := nodeaddr.Simple(recipient)
	notificationMsg := notification.BuildNotification(cfg, adjacency, knownNodes, contextID, recipient, sender, sourceSessionName, notificationPath, livenessMap)
	if deliveryNotificationObserverForTest != nil {
		deliveryNotificationObserverForTest(DeliveryNotificationObservation{
			Target:            target,
			Recipient:         recipient,
			Sender:            sender,
			SourceSessionName: sourceSessionName,
			NotificationPath:  notificationPath,
			Message:           notificationMsg,
		})
	}
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

// SetDeliveryNotificationHookForTest replaces direct-delivery notification
// emission for a test and returns a restoration closure.
func SetDeliveryNotificationHookForTest(hook func(controlplane.Target, *config.Config, map[string][]string, map[string]discovery.NodeInfo, string, string, string, string, string, map[string]bool)) func() {
	previous := sendDeliveryNotificationHook
	sendDeliveryNotificationHook = hook
	return func() { sendDeliveryNotificationHook = previous }
}

var sendDeliveryNotificationHook = sendDeliveryNotification

// deliverNotificationWithRetry attempts adapter.Deliver and, on failure, retries
// once using a refreshed pane ID from knownNodes when available. Extracted for
// testability: callers can inject a TmuxHandAdapter with a mock SendToPane.
func deliverNotificationWithRetry(adapter controlplane.HandAdapter, target controlplane.Target, delivery controlplane.PaneDelivery, recipient string, knownNodes map[string]discovery.NodeInfo, filename string) {
	if err := adapter.Deliver(target, delivery); err != nil {
		// A stale-address retry cannot help an unresponsive-but-correctly-addressed
		// pane, and re-delivering would re-paste the message and press Enter again
		// against the same pane (#816 guardian F-039).
		if errors.Is(err, notification.ErrPaneUnresponsive) {
			log.Printf("postman: WARNING: pane notification failed: node=%s pane=%s session=%s msg=%s err=%v\n", recipient, target.Hand.Address, target.SessionName, filename, err)
			return
		}
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
		return result, err
	}
	if !result.Delivered {
		return result, nil
	}

	notificationPath := target.PostPath(filename)
	sendDeliveryNotificationHook(target, cfg, adjacency, knownNodes, contextID, target.ActorID, sender, target.SessionName, notificationPath, livenessMap)
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

// directInboxWriteOutcome identifies what happened when one of the two
// direct (non-DeliverPostToInbox) inbox writers below -- sendDeadLetterNotification
// and writeRoutingDeniedWarning -- attempted its write (B-3/B-4).
type directInboxWriteOutcome string

const (
	directInboxWriteOutcomeWritten               directInboxWriteOutcome = "written"
	directInboxWriteOutcomeSkippedUnknownSender  directInboxWriteOutcome = "skipped-unknown-sender"
	directInboxWriteOutcomeSkippedForeignSession directInboxWriteOutcome = "skipped-foreign-session"
	directInboxWriteOutcomeSuppressedAtCap       directInboxWriteOutcome = "suppressed-at-cap"
	directInboxWriteOutcomeCountError            directInboxWriteOutcome = "count-error"
	directInboxWriteOutcomeWriteError            directInboxWriteOutcome = "write-error"
)

// resolveSenderWithinSession validates senderNode against knownNodes AND
// confirms the resolved node's own SessionDir equals targetSessionDir (B-4):
// a sender address that resolves to a DIFFERENT session's node -- for
// example a forged or cross-session-qualified address that happens to share
// a simple name with a node local to targetSessionDir -- must never be
// treated as that local node. Returns the simple name to use for inbox
// path-building and the outcome to use when the caller should skip (empty
// senderSimpleName is returned alongside a non-written outcome).
func resolveSenderWithinSession(senderNode, sourceSessionName, targetSessionDir string, knownNodes map[string]discovery.NodeInfo) (string, directInboxWriteOutcome) {
	senderSimpleName := nodeaddr.Simple(senderNode)
	resolution := resolveRuntimeNode(senderNode, sourceSessionName, knownNodes)
	if !resolution.Found {
		return senderSimpleName, directInboxWriteOutcomeSkippedUnknownSender
	}
	resolvedNode, ok := knownNodes[resolution.Address]
	// C-4: compare canonicalized paths (see sortedDistinctSessionDirs) so a
	// differently-spelled but identical session dir is never mistaken for a
	// foreign one.
	if !ok || filepath.Clean(resolvedNode.SessionDir) != filepath.Clean(targetSessionDir) {
		return senderSimpleName, directInboxWriteOutcomeSkippedForeignSession
	}
	return senderSimpleName, directInboxWriteOutcomeWritten
}

// sendDeadLetterNotification writes a dead-letter notification directly to the
// sender's inbox. Bypasses post/ to avoid re-triggering the daemon delivery loop.
// Pattern follows writeRoutingDeniedWarning's routing-denied notification.
// Issue #208: Extended with dead-letter path and recovery guidance.
// deadLetterBasename is the actual basename of the dead-letter file (after rename).
//
// P2-2a (docs/design/mailbox-overflow-policy.md §2.1): some callers of this
// function run before sender resolution has confirmed senderNode against
// knownNodes (several dead-letter branches in DeliverMessage fire earlier
// than the sender-resolution check), so senderNode cannot be assumed known
// here. Known-node validation happens first, before any write or fence
// acquisition, and additionally requires the resolved node's own session to
// equal sessionDir -- the session this notification would write into (B-4):
// an unresolved OR cross-session sender's write is skipped entirely rather
// than creating an inbox directory for an arbitrary, possibly forged name.
// A resolved, same-session sender's write is serialized under this
// recipient's admission fence (claim-only; no sequence is issued here),
// preceded by the session-level mailbox roots gate taken SHARED, per the
// documented lock order; the target inbox's message count is read under
// that same fence and the write is suppressed (never dead-lettered or
// recursed into DeliverMessage) when it is already at inboxQueueCap (B-3). A
// durable, cap-independent signal distinct from this best-effort notification
// is deferred to slice (c).
func sendDeadLetterNotification(sessionDir, contextID, senderNode, reason, originalFilename, deadLetterBasename string, knownNodes map[string]discovery.NodeInfo, sourceSessionName string) directInboxWriteOutcome {
	senderSimpleName, outcome := resolveSenderWithinSession(senderNode, sourceSessionName, sessionDir, knownNodes)
	if outcome != directInboxWriteOutcomeWritten {
		log.Printf("postman: dead-letter notification skipped: sender %q is not a known node of the target session (%s)\n", senderNode, outcome)
		return outcome
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

	senderInbox := filepath.Join(sessionDir, "inbox", senderSimpleName)
	result := directInboxWriteOutcomeWriteError
	fenceErr := withSessionRootsGates(sessionDir, sessionDir, func() error {
		_, err := store.WithAdmissionFence(sessionDir, senderSimpleName, func(*store.AdmissionHandle) error {
			count, countErr := countInboxMessages(senderInbox)
			if countErr != nil {
				log.Printf("postman: WARNING: dead-letter notification skipped for %s: inbox count failed: %v\n", senderNode, countErr)
				result = directInboxWriteOutcomeCountError
				return nil
			}
			if count >= inboxQueueCap {
				log.Printf("postman: dead-letter notification suppressed at cap: sender=%s reason=%q original=%s (cap=%d, current=%d)\n", senderNode, reason, originalFilename, inboxQueueCap, count)
				result = directInboxWriteOutcomeSuppressedAtCap
				return nil
			}
			if mkErr := os.MkdirAll(senderInbox, 0o700); mkErr != nil {
				return mkErr
			}
			notifPath := filepath.Join(senderInbox, filename)
			if writeErr := os.WriteFile(notifPath, []byte(content), 0o600); writeErr != nil {
				return writeErr
			}
			result = directInboxWriteOutcomeWritten
			return nil
		})
		return err
	})
	if fenceErr != nil {
		log.Printf("postman: WARNING: failed to write dead-letter notification for %s: %v\n", senderNode, fenceErr)
		return directInboxWriteOutcomeWriteError
	}
	return result
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
