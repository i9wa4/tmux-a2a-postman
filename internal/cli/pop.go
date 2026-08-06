package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/popnotice"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimecontext"
	"github.com/i9wa4/tmux-a2a-postman/internal/store"
	"gopkg.in/yaml.v3"
)

type popReadEventWriter interface {
	AppendEvent(eventType string, visibility journal.Visibility, payload interface{}, now time.Time) (journal.Event, error)
}

type popReadEventIdempotentWriter interface {
	AppendCurrentSessionEventIfAbsent(eventType string, visibility journal.Visibility, payload interface{}, options journal.AppendOptions, now time.Time, equivalent journal.EventEquivalenceFunc) (journal.Event, bool, error)
}

type directPopReadOutboxRecord struct {
	SchemaVersion int                         `json:"schema_version"`
	ContextID     string                      `json:"context_id"`
	SessionName   string                      `json:"session_name"`
	ReadID        string                      `json:"read_id"`
	ReadPath      string                      `json:"read_path"`
	Filename      string                      `json:"filename"`
	Content       string                      `json:"content"`
	Payload       journal.MailboxEventPayload `json:"payload"`
	CreatedAt     string                      `json:"created_at"`
}

var (
	openCurrentPopReadWriter = func(sessionDir string) (popReadEventWriter, error) {
		return journal.OpenCurrentWriter(sessionDir)
	}
	openShadowPopReadWriter = func(sessionDir, contextID, sessionName string, holderPID int, now time.Time) (popReadEventWriter, error) {
		return journal.OpenShadowWriter(sessionDir, contextID, sessionName, holderPID, now)
	}
	directPopReadAfterAppendHook func() error
)

// RunPop reads and optionally archives the oldest unread inbox message (#277).
func RunPop(args []string) error {
	return runPopWithContext(defaultCommandContext(), args)
}

func runPopWithContext(ctx commandContext, args []string) error {
	ctx = ctx.withDefaults()
	fs := flag.NewFlagSet("pop", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "context ID") // Issue #315: forward global --context-id
	configPath := fs.String("config", "", "path to config file (optional)")
	runtimeContextMode := fs.String("runtime-context", "summary", "runtime context output mode: summary or none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *runtimeContextMode != "summary" && *runtimeContextMode != "none" {
		return fmt.Errorf("--runtime-context must be summary or none")
	}

	inboxArgs := fs.Args()
	if *contextID != "" {
		inboxArgs = append([]string{"--context-id", *contextID}, inboxArgs...)
	}
	if *configPath != "" {
		inboxArgs = append([]string{"--config", *configPath}, inboxArgs...)
	}
	inboxPath, err := ctx.resolveInboxPath(inboxArgs)
	if err != nil {
		return err
	}
	sessionDir := filepath.Dir(filepath.Dir(inboxPath))
	contextDir := filepath.Dir(sessionDir)
	resolvedContextID := filepath.Base(contextDir)
	baseDir := filepath.Dir(contextDir)
	sessionName := filepath.Base(sessionDir)
	nodeName := filepath.Base(inboxPath)

	cfg, err := ctx.loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	if ctx.contextOwnsSession(baseDir, resolvedContextID, sessionName) {
		response, err := ctx.roundTripDaemonSubmit(sessionDir, projection.DaemonSubmitRequest{
			Command: projection.DaemonSubmitPop,
			Node:    nodeName,
		}, daemonSubmitTimeout(cfg.TmuxTimeout))
		if err != nil {
			return fmt.Errorf("daemon submit pop: %w", err)
		}
		if response.Empty {
			return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir), projection.SubmitPathDaemon)
		}
		remaining := response.UnreadBefore - 1
		if remaining < 0 {
			remaining = 0
		}
		markdownPath := response.MarkdownPath
		if markdownPath == "" && response.Filename != "" {
			markdownPath = filepath.Join(sessionDir, "read", response.Filename)
		}
		return writePopMessageOutput(ctx.stdout, response.Content, response.Filename, markdownPath, intPtr(response.UnreadBefore), intPtr(remaining), response.StaleBacklog, *runtimeContextMode, popSessionDiagnosticsForSession(sessionDir), projection.SubmitPathDaemon, popReceiverContextOptions{
			ContextID:   resolvedContextID,
			SessionName: sessionName,
			Node:        nodeName,
		})
	}

	if err := replayDirectPopReadOutbox(sessionDir); err != nil {
		return err
	}
	if err := store.RecoverArchiveBindings(sessionDir); err != nil {
		return fmt.Errorf("recovering direct pop archive binding: %w", err)
	}

	msgs := message.ScanInboxMessages(inboxPath)
	if len(msgs) == 0 {
		return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir), projection.SubmitPathPost)
	}
	sort.Slice(msgs, func(i, j int) bool {
		return msgs[i].Filename < msgs[j].Filename
	})

	abs := filepath.Join(inboxPath, msgs[0].Filename)
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			// Race: file disappeared between listing and reading; retry once.
			msgs = message.ScanInboxMessages(inboxPath)
			if len(msgs) == 0 {
				return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir), projection.SubmitPathPost)
			}
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Filename < msgs[j].Filename
			})
			abs = filepath.Join(inboxPath, msgs[0].Filename)
			data, err = os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir), projection.SubmitPathPost)
				}
				return fmt.Errorf("reading message: %w", err)
			}
		} else {
			return fmt.Errorf("reading message: %w", err)
		}
	}

	remaining := len(msgs)
	staleBacklog := popnotice.BuildStaleBacklogNotice(msgs[0], msgs[1:])
	readPath, err := archivePoppedMessage(abs, msgs[0].Filename, data)
	if err != nil {
		return err
	}
	if err := recordDirectPopRead(sessionDir, resolvedContextID, sessionName, readPath, msgs[0].Filename, string(data)); err != nil {
		return err
	}
	remaining--
	return writePopMessageOutput(ctx.stdout, string(data), msgs[0].Filename, readPath, intPtr(len(msgs)), intPtr(remaining), staleBacklog, *runtimeContextMode, popSessionDiagnosticsForSession(sessionDir), projection.SubmitPathPost, popReceiverContextOptions{
		ContextID:   resolvedContextID,
		SessionName: sessionName,
		Node:        nodeName,
	})
}

func archivePoppedMessage(absPath, filename string, data []byte) (string, error) {
	return store.ArchiveInboxMessageVerified(absPath, filename, data)
}

func recordDirectPopRead(sessionDir, contextID, sessionName, readPath, filename, content string) error {
	record := directPopReadOutboxRecord{
		SchemaVersion: 1,
		ContextID:     contextID,
		SessionName:   sessionName,
		ReadID:        stableDirectPopReadID(contextID, sessionName, filename),
		ReadPath:      readPath,
		Filename:      filename,
		Content:       content,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
	}
	record.Payload = directPopReadPayload(contextID, record.ReadID, readPath, filename, content)
	outboxPath, err := writeDirectPopReadOutbox(sessionDir, record)
	if err != nil {
		return err
	}
	if err := appendDirectPopReadRecord(sessionDir, record); err != nil {
		return err
	}
	if directPopReadAfterAppendHook != nil {
		if err := directPopReadAfterAppendHook(); err != nil {
			return err
		}
	}
	if err := os.Remove(outboxPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing pop read outbox record: %w", err)
	}
	if err := syncPopDirectory(filepath.Dir(outboxPath)); err != nil {
		return fmt.Errorf("syncing pop read outbox removal: %w", err)
	}
	return nil
}

func directPopReadPayload(contextID, readID, readPath, filename, content string) journal.MailboxEventPayload {
	if content == "" {
		if readContent, err := os.ReadFile(readPath); err == nil && len(readContent) > 0 {
			content = string(readContent)
		}
	}
	payload := journal.MailboxEventPayload{
		MessageID: filename,
		ReadID:    readID,
		Path:      filepath.Join("read", filename),
		Content:   content,
	}
	if meta, err := envelope.ParseMetadata(content); err == nil {
		payload.ContextID = meta.ContextID
		payload.From = meta.From
		payload.To = meta.To
		payload.ReplyPolicy = meta.ReplyPolicy
		payload.ReplyTo = meta.ReplyTo
		payload.MessageType = meta.MessageType
		payload.Timestamp = meta.Timestamp
		payload.ThreadID = meta.ThreadID
		payload.TaskID = meta.TaskID
		payload.RunID = meta.RunID
		payload.MandateID = meta.MandateID
		payload.AuthorityGeneration = meta.AuthorityGeneration
		payload.LaneID = meta.LaneID
		payload.ParentLaneID = meta.ParentLaneID
		payload.AcceptancePredicate = meta.AcceptancePredicate
		payload.SupersessionState = meta.SupersessionState
		payload.TerminalAcceptanceState = meta.TerminalAcceptanceState
		payload.InputRequestID = meta.InputRequestID
		payload.FillsInputRequestID = meta.FillsInputRequestID
		payload.InputRequestSetID = meta.InputRequestSetID
		payload.BranchID = meta.BranchID
		payload.CompletionRule = meta.CompletionRule
	}
	if payload.ContextID == "" {
		payload.ContextID = contextID
	}
	return payload
}

func stableDirectPopReadID(contextID, sessionName, filename string) string {
	sum := sha256.Sum256([]byte(contextID + "\x00" + sessionName + "\x00" + filename))
	return "direct-pop-read:" + hex.EncodeToString(sum[:])
}

func appendDirectPopReadRecord(sessionDir string, record directPopReadOutboxRecord) error {
	if record.ReadID == "" {
		record.ReadID = stableDirectPopReadID(record.ContextID, record.SessionName, record.Filename)
	}
	record.Payload.ReadID = record.ReadID
	writer, err := openCurrentPopReadWriter(sessionDir)
	if err != nil {
		writer, err = openShadowPopReadWriter(sessionDir, record.ContextID, record.SessionName, os.Getpid(), time.Now())
		if err != nil {
			return fmt.Errorf("recording pop read event: %w", err)
		}
	}
	if idempotentWriter, ok := writer.(popReadEventIdempotentWriter); ok {
		if _, _, err := idempotentWriter.AppendCurrentSessionEventIfAbsent(projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, record.Payload, journal.AppendOptions{
			ThreadID: record.Payload.ThreadID,
		}, time.Now(), equivalentPopReadEvent(record.ReadID)); err != nil {
			return fmt.Errorf("recording pop read event: %w", err)
		}
		return nil
	}
	if _, err := writer.AppendEvent(projection.MailboxProjectionReadEventType, journal.VisibilityOperatorVisible, record.Payload, time.Now()); err != nil {
		return fmt.Errorf("recording pop read event: %w", err)
	}
	return nil
}

func equivalentPopReadEvent(readID string) journal.EventEquivalenceFunc {
	return func(event journal.Event) (bool, error) {
		if event.Type != projection.MailboxProjectionReadEventType || readID == "" {
			return false, nil
		}
		var payload journal.MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return false, err
		}
		return payload.ReadID == readID, nil
	}
}

func directPopReadOutboxDir(sessionDir string) string {
	return filepath.Join(sessionDir, "snapshot", "pop-read-outbox")
}

func writeDirectPopReadOutbox(sessionDir string, record directPopReadOutboxRecord) (string, error) {
	dir := directPopReadOutboxDir(sessionDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating pop read outbox: %w", err)
	}
	path := filepath.Join(dir, record.Filename+".json")
	tmp, err := os.CreateTemp(dir, "."+record.Filename+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("creating pop read outbox temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	data, err := json.Marshal(record)
	if err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("encoding pop read outbox: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("writing pop read outbox: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("syncing pop read outbox: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("closing pop read outbox: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("publishing pop read outbox: %w", err)
	}
	cleanup = false
	if err := syncPopDirectory(dir); err != nil {
		return "", fmt.Errorf("syncing pop read outbox directory: %w", err)
	}
	return path, nil
}

func replayDirectPopReadOutbox(sessionDir string) error {
	dir := directPopReadOutboxDir(sessionDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading pop read outbox: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading pop read outbox record: %w", err)
		}
		var record directPopReadOutboxRecord
		if err := json.Unmarshal(data, &record); err != nil {
			return fmt.Errorf("decoding pop read outbox record %s: %w", path, err)
		}
		if err := appendDirectPopReadRecord(sessionDir, record); err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("removing replayed pop read outbox record: %w", err)
		}
	}
	if len(entries) > 0 {
		if err := syncPopDirectory(dir); err != nil {
			return fmt.Errorf("syncing replayed pop read outbox directory: %w", err)
		}
	}
	return nil
}

func syncPopDirectory(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := handle.Sync(); err != nil {
		_ = handle.Close()
		return err
	}
	return handle.Close()
}

type popEmptyOutput struct {
	Status             string                 `json:"status"`
	SessionDiagnostics *popSessionDiagnostics `json:"session_diagnostics,omitempty"`
	SubmitPath         projection.SubmitPath  `json:"submit_path,omitempty"`
}

type popMessageOutput struct {
	Status                      string                            `json:"status"`
	MessageID                   string                            `json:"message_id,omitempty"`
	MarkdownPath                string                            `json:"markdown_path,omitempty"`
	MarkdownAbsolutePath        string                            `json:"markdown_absolute_path,omitempty"`
	Frontmatter                 map[string]any                    `json:"frontmatter,omitempty"`
	From                        string                            `json:"from"`
	To                          string                            `json:"to"`
	ReplyPolicy                 string                            `json:"reply_policy,omitempty"`
	ReplyTo                     string                            `json:"reply_to,omitempty"`
	MandateID                   string                            `json:"mandate_id,omitempty"`
	AuthorityGeneration         int                               `json:"authority_generation,omitempty"`
	LaneID                      string                            `json:"lane_id,omitempty"`
	ParentLaneID                string                            `json:"parent_lane_id,omitempty"`
	AcceptancePredicate         string                            `json:"acceptance_predicate,omitempty"`
	SupersessionState           string                            `json:"supersession_state,omitempty"`
	TerminalAcceptanceState     string                            `json:"terminal_acceptance_state,omitempty"`
	InputRequestID              string                            `json:"input_request_id,omitempty"`
	FillsInputRequestID         string                            `json:"fills_input_request_id,omitempty"`
	InputRequestSetID           string                            `json:"input_request_set_id,omitempty"`
	BranchID                    string                            `json:"branch_id,omitempty"`
	CompletionRule              string                            `json:"completion_rule,omitempty"`
	Timestamp                   string                            `json:"timestamp"`
	UnreadBefore                *int                              `json:"unread_before,omitempty"`
	Remaining                   *int                              `json:"remaining,omitempty"`
	StaleBacklog                *projection.PopStaleBacklogNotice `json:"stale_backlog,omitempty"`
	ArchivedBodyReadRequired    bool                              `json:"archived_body_read_required,omitempty"`
	ArchivedBodyReadInstruction string                            `json:"archived_body_read_instruction,omitempty"`
	ReceiverRuntimeContext      *runtimecontext.Summary           `json:"receiver_runtime_context,omitempty"`
	ReceiverRuntimeContextError string                            `json:"receiver_runtime_context_error,omitempty"`
	RuntimeContext              *runtimecontext.Summary           `json:"runtime_context,omitempty"`
	RuntimeContextError         string                            `json:"runtime_context_error,omitempty"`
	PopReceiptPath              string                            `json:"pop_receipt_path,omitempty"`
	PopReceiptAbsolutePath      string                            `json:"pop_receipt_absolute_path,omitempty"`
	SessionDiagnostics          *popSessionDiagnostics            `json:"session_diagnostics,omitempty"`
	SubmitPath                  projection.SubmitPath             `json:"submit_path,omitempty"`
}

type popReceiverContextOptions struct {
	ContextID   string
	SessionName string
	Node        string
}

type popSessionDiagnostics struct {
	Source                       string `json:"source"`
	ActiveTaskCount              int    `json:"active_task_count"`
	UnreadInboxCount             int    `json:"unread_inbox_count"`
	InputRequiredCount           int    `json:"input_required_count"`
	WaitingOnInputCount          int    `json:"waiting_on_input_count"`
	UnclosedRequiredRequestCount int    `json:"unclosed_required_request_count"`
	PostCount                    int    `json:"post_count"`
	DeadLetterCount              int    `json:"dead_letter_count"`
}

const archivedBodyReadInstruction = "Read the complete archived Markdown body from markdown_absolute_path when present, otherwise markdown_path, before any handling, routing, reply, status decision, or no-action or no-op decision; messageType, replyPolicy, and other metadata do not waive this; truncated command output is not a complete read."

func writeEmptyPopOutput(stdout io.Writer, diagnostics *popSessionDiagnostics, submitPath projection.SubmitPath) error {
	return json.NewEncoder(stdout).Encode(popEmptyOutput{Status: "empty", SessionDiagnostics: diagnostics, SubmitPath: submitPath})
}

func writePopMessageOutput(stdout io.Writer, content, filename, markdownPath string, unreadBefore, remaining *int, staleBacklog *projection.PopStaleBacklogNotice, runtimeContextMode string, diagnostics *popSessionDiagnostics, submitPath projection.SubmitPath, receiverOptions popReceiverContextOptions) error {
	return writePopMessageOutputWithOps(stdout, content, filename, markdownPath, unreadBefore, remaining, staleBacklog, runtimeContextMode, diagnostics, submitPath, receiverOptions, osPopReceiptFileOps)
}

type popReceiptFileOps struct {
	mkdirAll  func(string, os.FileMode) error
	writeFile func(string, []byte, os.FileMode) error
}

var osPopReceiptFileOps = popReceiptFileOps{
	mkdirAll:  os.MkdirAll,
	writeFile: os.WriteFile,
}

func writePopMessageOutputWithOps(stdout io.Writer, content, filename, markdownPath string, unreadBefore, remaining *int, staleBacklog *projection.PopStaleBacklogNotice, runtimeContextMode string, diagnostics *popSessionDiagnostics, submitPath projection.SubmitPath, receiverOptions popReceiverContextOptions, receiptOps popReceiptFileOps) error {
	output := parseMessageContent(content, filename)
	output.MarkdownPath = displayMarkdownPath(markdownPath)
	if output.MarkdownPath != markdownPath {
		output.MarkdownAbsolutePath = markdownPath
	}
	if runtimeContextMode == "summary" {
		output.ReceiverRuntimeContext, output.ReceiverRuntimeContextError = receiverRuntimeContextSummaryForPop(sessionDirFromArchivedMarkdownPath(markdownPath), output.MessageID, receiverOptions)
		output.RuntimeContext, output.RuntimeContextError = runtimeContextSummaryForMessage(content, markdownPath)
	}
	output.UnreadBefore = unreadBefore
	output.Remaining = remaining
	output.StaleBacklog = staleBacklog
	output.ArchivedBodyReadRequired = true
	output.ArchivedBodyReadInstruction = archivedBodyReadInstruction
	output.SessionDiagnostics = diagnostics
	output.SubmitPath = submitPath
	receiptPlan := store.PlanPopReceipt(markdownPath)
	receiptPath := receiptPlan.ReceiptPath
	if receiptPath != "" {
		output.PopReceiptPath = displayMarkdownPath(receiptPath)
		if output.PopReceiptPath != receiptPath {
			output.PopReceiptAbsolutePath = receiptPath
		}
	}
	payload, err := json.Marshal(output)
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	if receiptPath != "" {
		if err := writePopReceipt(receiptPlan, payload, receiptOps); err != nil {
			return fmt.Errorf("writing pop receipt: %w", err)
		}
	}
	_, err = stdout.Write(payload)
	return err
}

func writePopReceipt(plan store.PopReceiptPlan, payload []byte, ops popReceiptFileOps) error {
	if plan.ReceiptPath == "" {
		return nil
	}
	if err := ops.mkdirAll(plan.ReadDir, 0o700); err != nil {
		return fmt.Errorf("creating pop receipt directory: %w", err)
	}
	return ops.writeFile(plan.ReceiptPath, payload, 0o600)
}

func popSessionDiagnosticsForSession(sessionDir string) *popSessionDiagnostics {
	queues := collectSessionQueues(sessionDir)
	diagnostics := &popSessionDiagnostics{
		Source:           "filesystem",
		UnreadInboxCount: queues.InboxCount,
		PostCount:        queues.PostCount,
		DeadLetterCount:  queues.DeadLetterCount,
	}

	sessionName := filepath.Base(sessionDir)
	inputRequests, ok := projectedInputRequestCounts(sessionDir, sessionName, time.Now(), projection.DefaultInputRequestStaleAfterSeconds)
	if !ok {
		return diagnostics
	}
	inputRequired := len(inputRequests.InputRequired)
	waitingOnInput := len(inputRequests.WaitingOnInput)
	unclosedRequiredRequests := uniqueOpenInputRequestCount(inputRequests)
	diagnostics.Source = "projection"
	diagnostics.InputRequiredCount = inputRequired
	diagnostics.WaitingOnInputCount = waitingOnInput
	diagnostics.UnclosedRequiredRequestCount = unclosedRequiredRequests
	diagnostics.ActiveTaskCount = diagnostics.UnclosedRequiredRequestCount
	return diagnostics
}

func uniqueOpenInputRequestCount(inputRequests projection.MessageInputRequestState) int {
	seen := make(map[string]struct{})
	add := func(details []projection.InputRequestDetail) {
		for _, detail := range details {
			key := openInputRequestIdentity(detail)
			if key == "" {
				continue
			}
			seen[key] = struct{}{}
		}
	}
	add(inputRequests.InputRequired)
	add(inputRequests.WaitingOnInput)
	return len(seen)
}

func openInputRequestIdentity(detail projection.InputRequestDetail) string {
	if detail.InputRequestID != "" {
		return "exact:" + detail.InputRequestID
	}
	if detail.MessageID == "" && detail.Recipient == "" {
		return ""
	}
	return "fallback:" + detail.MessageID + "\x00" + detail.Recipient
}

func intPtr(value int) *int {
	return &value
}

func parseMessageContent(content, filename string) popMessageOutput {
	result := popMessageOutput{
		Status:      "message",
		MessageID:   filename,
		Frontmatter: frontmatterFromContent(content),
	}
	metadata, err := envelope.ParseMetadata(content)
	if err != nil {
		return result
	}
	result.From = metadata.From
	result.To = metadata.To
	result.MessageID = metadata.MessageID
	if result.MessageID == "" {
		result.MessageID = filename
	}
	result.ReplyPolicy = metadata.ReplyPolicy
	result.ReplyTo = metadata.ReplyTo
	result.MandateID = metadata.MandateID
	result.AuthorityGeneration = metadata.AuthorityGeneration
	result.LaneID = metadata.LaneID
	result.ParentLaneID = metadata.ParentLaneID
	result.AcceptancePredicate = metadata.AcceptancePredicate
	result.SupersessionState = metadata.SupersessionState
	result.TerminalAcceptanceState = metadata.TerminalAcceptanceState
	result.InputRequestID = metadata.InputRequestID
	result.FillsInputRequestID = metadata.FillsInputRequestID
	result.InputRequestSetID = metadata.InputRequestSetID
	result.BranchID = metadata.BranchID
	result.CompletionRule = metadata.CompletionRule
	result.Timestamp = metadata.Timestamp
	return result
}

func runtimeContextSummaryForMessage(content, markdownPath string) (*runtimecontext.Summary, string) {
	metadata, err := envelope.ParseMetadata(content)
	if err != nil || metadata.RuntimeContextID == "" {
		return nil, ""
	}
	if markdownPath == "" {
		return nil, "runtime_context_unavailable: archived message path unavailable"
	}
	sessionDir := sessionDirFromArchivedMarkdownPath(markdownPath)
	if sessionDir == "" {
		return nil, "runtime_context_unavailable: archived message path is not in read/"
	}
	summary, err := runtimecontext.LoadSummary(sessionDir, metadata.RuntimeContextID, time.Now())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "runtime_context_unavailable: referenced snapshot not found"
		}
		return nil, "runtime_context_unavailable: referenced snapshot could not be loaded"
	}
	if metadata.RuntimeContextHash != "" && summary.ContentHash != "" && metadata.RuntimeContextHash != summary.ContentHash {
		return nil, "runtime_context_hash_mismatch: envelope runtimeContextHash does not match archived runtime context content_hash"
	}
	return summary, ""
}

func receiverRuntimeContextSummaryForPop(sessionDir, messageID string, opts popReceiverContextOptions) (*runtimecontext.Summary, string) {
	if sessionDir == "" {
		return nil, "receiver_runtime_context_unavailable: archived message path unavailable"
	}
	now := time.Now()
	paneID := config.GetTmuxPaneID()
	snapshot := runtimecontext.BuildSnapshot(runtimecontext.BuildOptions{
		Now:                        now,
		Scope:                      "receiver",
		ContextID:                  opts.ContextID,
		MessageID:                  messageID,
		TmuxSession:                opts.SessionName,
		Node:                       opts.Node,
		PaneID:                     paneID,
		Runtime:                    runtimecontext.CollectLaunchCommandMetadata(paneID),
		SuppressRuntimeAutoCollect: true,
	})
	saved, err := runtimecontext.SaveSnapshot(sessionDir, snapshot)
	if err != nil {
		return nil, "receiver_runtime_context_unavailable: snapshot could not be saved"
	}
	summary := runtimecontext.SummaryFromSnapshot(saved.Snapshot, saved.Path, saved.SizeBytes, now)
	return &summary, ""
}

func sessionDirFromArchivedMarkdownPath(markdownPath string) string {
	if markdownPath == "" {
		return ""
	}
	parent := filepath.Base(filepath.Dir(markdownPath))
	if parent != "read" {
		return ""
	}
	return filepath.Dir(filepath.Dir(markdownPath))
}

func displayMarkdownPath(markdownPath string) string {
	if markdownPath == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return markdownPath
	}
	rel, err := filepath.Rel(home, markdownPath)
	if rel == "." {
		return "~"
	}
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return markdownPath
	}
	return filepath.Join("~", rel)
}

func frontmatterFromContent(content string) map[string]any {
	frontmatterContent, _, ok, err := envelope.ScanFrontmatter(content)
	if !ok || err != nil {
		return nil
	}
	var frontmatter map[string]any
	if err := yaml.Unmarshal([]byte(frontmatterContent), &frontmatter); err != nil {
		return nil
	}
	return frontmatter
}
