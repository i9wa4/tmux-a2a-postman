package cli

import (
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
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimecontext"
	"gopkg.in/yaml.v3"
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
			return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir))
		}
		remaining := response.UnreadBefore - 1
		if remaining < 0 {
			remaining = 0
		}
		markdownPath := response.MarkdownPath
		if markdownPath == "" && response.Filename != "" {
			markdownPath = filepath.Join(sessionDir, "read", response.Filename)
		}
		return writePopMessageOutput(ctx.stdout, response.Content, response.Filename, markdownPath, intPtr(response.UnreadBefore), intPtr(remaining), *runtimeContextMode, popSessionDiagnosticsForSession(sessionDir))
	}

	msgs := message.ScanInboxMessages(inboxPath)
	if len(msgs) == 0 {
		return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir))
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
				return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir))
			}
			sort.Slice(msgs, func(i, j int) bool {
				return msgs[i].Filename < msgs[j].Filename
			})
			abs = filepath.Join(inboxPath, msgs[0].Filename)
			data, err = os.ReadFile(abs)
			if err != nil {
				if os.IsNotExist(err) {
					return writeEmptyPopOutput(ctx.stdout, popSessionDiagnosticsForSession(sessionDir))
				}
				return fmt.Errorf("reading message: %w", err)
			}
		} else {
			return fmt.Errorf("reading message: %w", err)
		}
	}

	remaining := len(msgs)
	readPath, err := archivePoppedMessage(abs, msgs[0].Filename)
	if err != nil {
		return err
	}
	remaining--
	return writePopMessageOutput(ctx.stdout, string(data), msgs[0].Filename, readPath, intPtr(len(msgs)), intPtr(remaining), *runtimeContextMode, popSessionDiagnosticsForSession(sessionDir))
}

func archivePoppedMessage(absPath, filename string) (string, error) {
	return message.ArchiveInboxMessage(absPath, filename)
}

type popEmptyOutput struct {
	Status             string                 `json:"status"`
	SessionDiagnostics *popSessionDiagnostics `json:"session_diagnostics,omitempty"`
}

type popMessageOutput struct {
	Status                      string                  `json:"status"`
	MessageID                   string                  `json:"message_id,omitempty"`
	MarkdownPath                string                  `json:"markdown_path,omitempty"`
	MarkdownAbsolutePath        string                  `json:"markdown_absolute_path,omitempty"`
	Frontmatter                 map[string]any          `json:"frontmatter,omitempty"`
	From                        string                  `json:"from"`
	To                          string                  `json:"to"`
	ReplyPolicy                 string                  `json:"reply_policy,omitempty"`
	ReplyTo                     string                  `json:"reply_to,omitempty"`
	InputRequestID              string                  `json:"input_request_id,omitempty"`
	FillsInputRequestID         string                  `json:"fills_input_request_id,omitempty"`
	InputRequestSetID           string                  `json:"input_request_set_id,omitempty"`
	BranchID                    string                  `json:"branch_id,omitempty"`
	CompletionRule              string                  `json:"completion_rule,omitempty"`
	Timestamp                   string                  `json:"timestamp"`
	UnreadBefore                *int                    `json:"unread_before,omitempty"`
	Remaining                   *int                    `json:"remaining,omitempty"`
	ArchivedBodyReadRequired    bool                    `json:"archived_body_read_required,omitempty"`
	ArchivedBodyReadInstruction string                  `json:"archived_body_read_instruction,omitempty"`
	RuntimeContext              *runtimecontext.Summary `json:"runtime_context,omitempty"`
	RuntimeContextError         string                  `json:"runtime_context_error,omitempty"`
	PopReceiptPath              string                  `json:"pop_receipt_path,omitempty"`
	PopReceiptAbsolutePath      string                  `json:"pop_receipt_absolute_path,omitempty"`
	SessionDiagnostics          *popSessionDiagnostics  `json:"session_diagnostics,omitempty"`
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

func writeEmptyPopOutput(stdout io.Writer, diagnostics *popSessionDiagnostics) error {
	return json.NewEncoder(stdout).Encode(popEmptyOutput{Status: "empty", SessionDiagnostics: diagnostics})
}

func writePopMessageOutput(stdout io.Writer, content, filename, markdownPath string, unreadBefore, remaining *int, runtimeContextMode string, diagnostics *popSessionDiagnostics) error {
	output := parseMessageContent(content, filename)
	output.MarkdownPath = displayMarkdownPath(markdownPath)
	if output.MarkdownPath != markdownPath {
		output.MarkdownAbsolutePath = markdownPath
	}
	if runtimeContextMode == "summary" {
		output.RuntimeContext, output.RuntimeContextError = runtimeContextSummaryForMessage(content, markdownPath)
	}
	output.UnreadBefore = unreadBefore
	output.Remaining = remaining
	output.ArchivedBodyReadRequired = true
	output.ArchivedBodyReadInstruction = archivedBodyReadInstruction
	output.SessionDiagnostics = diagnostics
	receiptPath, err := popReceiptPath(markdownPath)
	if err != nil {
		return err
	}
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
		if err := os.WriteFile(receiptPath, payload, 0o600); err != nil {
			return fmt.Errorf("writing pop receipt: %w", err)
		}
	}
	_, err = stdout.Write(payload)
	return err
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
	inputRequests, ok := projectedInputRequestCounts(sessionDir, sessionName)
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

func popReceiptPath(markdownPath string) (string, error) {
	if markdownPath == "" {
		return "", nil
	}
	dir := filepath.Dir(markdownPath)
	if filepath.Base(dir) != "read" {
		return "", nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("creating pop receipt directory: %w", err)
	}
	filename := filepath.Base(markdownPath)
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	if stem == "" {
		stem = filename
	}
	return filepath.Join(dir, stem+".pop.json"), nil
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
