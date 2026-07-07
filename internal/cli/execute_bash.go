package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

const (
	commandApprovalModeAdvisory = "advisory"
	commandApprovalModeWarnOnly = "warn-only"
	commandApprovalModeBlocking = "blocking"
	defaultCommandApprovalTTL   = 15 * time.Minute
)

type executeBashResult struct {
	Status         string `json:"status"`
	Mode           string `json:"mode,omitempty"`
	Requester      string `json:"requester,omitempty"`
	Reviewer       string `json:"reviewer,omitempty"`
	Label          string `json:"label,omitempty"`
	Category       string `json:"category,omitempty"`
	ThreadID       string `json:"thread_id,omitempty"`
	CommandHash    string `json:"command_hash,omitempty"`
	Decision       string `json:"decision,omitempty"`
	Reason         string `json:"reason,omitempty"`
	ExitStatus     int    `json:"exit_status,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	CommandTextSet bool   `json:"command_text_set,omitempty"`
}

type commandExitError struct {
	code int
}

func (e commandExitError) Error() string {
	return fmt.Sprintf("bash command exited with status %d", e.code)
}

func (e commandExitError) ExitCode() int {
	return e.code
}

type resolvedCommandApprovalPolicy struct {
	Requester            string
	Reviewer             string
	Mode                 string
	Label                string
	Category             string
	TTL                  time.Duration
	ReviewerNodeOverride string // per-policy reviewer_node override, if any (#626)
}

type commandApprovalEvaluation struct {
	Decision string
	Allowed  bool
	Reason   string
	Thread   *projection.CommandApprovalThread
}

func RunExecuteBash(args []string) error {
	return runExecuteBashWithContext(defaultCommandContext(), args)
}

func runExecuteBashWithContext(ctx commandContext, args []string) error {
	ctx = ctx.withDefaults()
	fs := flag.NewFlagSet("execute-bash", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	requester := fs.String("requester", "", "requester node name (optional, defaults to current tmux pane title)")
	reviewer := fs.String("reviewer", "", "reviewer node override")
	label := fs.String("label", "", "command label (required for execution)")
	category := fs.String("category", "", "command category")
	mode := fs.String("mode", "", "approval mode override: advisory, warn-only, or blocking")
	reason := fs.String("reason", "", "reason shown in command approval request")
	command := fs.String("command", "", "bash command string to execute")
	overrideApproval := fs.Bool("override-approval", false, "explicitly continue warn-only execution without approval")
	storeCommandText := fs.Bool("store-command-text", false, "store full command text in durable audit events")
	threadID := fs.String("thread-id", "", "approval thread id override or decision thread id")
	recordDecision := fs.String("record-decision", "", "record an approval decision for --thread-id: approved or rejected")
	ttlSeconds := fs.Float64("approval-ttl-seconds", 0, "approval request expiry in seconds")
	jsonOutput := fs.Bool("json", false, "write wrapper metadata as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := ctx.loadConfig(*configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	baseDir := config.ResolveBaseDir(cfg.BaseDir)
	resolvedSessionName, err := resolveExecuteBashSessionName(ctx, *sessionName)
	if err != nil {
		return err
	}
	resolvedContextID, err := resolveExecuteBashContextID(baseDir, resolvedSessionName, *contextID)
	if err != nil {
		return err
	}
	sessionDir := filepath.Join(baseDir, resolvedContextID, resolvedSessionName)
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		return fmt.Errorf("creating session directories: %w", err)
	}

	resolvedRequester, err := resolveExecuteBashRequester(ctx, *requester)
	if err != nil {
		return err
	}
	if *recordDecision != "" {
		return recordExecuteBashDecision(ctx, executeBashDecisionOptions{
			sessionDir:       sessionDir,
			contextID:        resolvedContextID,
			sessionName:      resolvedSessionName,
			threadID:         *threadID,
			reviewer:         *reviewer,
			fallbackReviewer: resolvedRequester,
			decision:         *recordDecision,
			reason:           *reason,
			storeCommandText: *storeCommandText,
			commandText:      *command,
		})
	}

	if strings.TrimSpace(*label) == "" {
		return fmt.Errorf("--label is required")
	}
	commandText := strings.TrimSpace(*command)
	if commandText == "" {
		commandText = strings.TrimSpace(strings.Join(fs.Args(), " "))
	}
	if commandText == "" {
		return fmt.Errorf("--command or trailing bash command is required")
	}

	policy, err := resolveCommandApprovalPolicy(cfg, resolvedRequester, strings.TrimSpace(*label), strings.TrimSpace(*category), *reviewer, *mode, *ttlSeconds)
	if err != nil {
		return err
	}
	commandHash := commandDigest(commandText)
	resolvedThreadID := strings.TrimSpace(*threadID)
	if resolvedThreadID == "" {
		resolvedThreadID = commandApprovalThreadID(policy, commandHash)
	}
	expiresAt := ctx.now().Add(policy.TTL).UTC().Format(time.RFC3339Nano)
	reviewerNode, validReviewer := cfg.ResolveReviewerNode(policy.ReviewerNodeOverride)

	evaluation, err := evaluateCommandApproval(sessionDir, policy, resolvedThreadID, commandHash, validReviewer, ctx.now())
	if err != nil {
		return err
	}
	if !evaluation.Allowed {
		if err := recordCommandApprovalRequest(sessionDir, resolvedContextID, resolvedSessionName, resolvedThreadID, policy, commandHash, *reason, expiresAt, commandText, *storeCommandText, ctx.now()); err != nil {
			return err
		}
		if validReviewer {
			deliverCommandApprovalRequest(cfg, baseDir, resolvedContextID, resolvedSessionName, policy, reviewerNode, resolvedThreadID, commandHash, *reason, *storeCommandText, ctx.now())
		}
	}

	decision := decisionForPolicy(policy.Mode, evaluation, *overrideApproval)
	if err := recordCommandExecutionDecision(sessionDir, resolvedContextID, resolvedSessionName, resolvedThreadID, policy, commandHash, decision, evaluation.Reason, *overrideApproval, commandText, *storeCommandText, ctx.now()); err != nil {
		return err
	}

	resultExpiresAt := expiresAt
	if evaluation.Thread != nil && evaluation.Thread.ExpiresAt != "" {
		resultExpiresAt = evaluation.Thread.ExpiresAt
	}
	result := executeBashResult{
		Status:         "pending",
		Mode:           policy.Mode,
		Requester:      policy.Requester,
		Reviewer:       policy.Reviewer,
		Label:          policy.Label,
		Category:       policy.Category,
		ThreadID:       resolvedThreadID,
		CommandHash:    commandHash,
		Decision:       decision,
		Reason:         evaluation.Reason,
		ExpiresAt:      resultExpiresAt,
		CommandTextSet: *storeCommandText,
	}

	if blockReason := blockedCommandApprovalReason(policy.Mode, evaluation, *overrideApproval); blockReason != "" {
		result.Status = "blocked"
		result.Reason = blockReason
		_ = writeExecuteBashMetadata(ctx.stderr, result)
		return fmt.Errorf("%s", blockReason)
	}
	if policy.Mode == commandApprovalModeWarnOnly && *overrideApproval && !evaluation.Allowed {
		_, _ = fmt.Fprintf(ctx.stderr, "postman: warning: command approval absent; continuing because --override-approval was set (thread=%s)\n", resolvedThreadID)
	}
	if policy.Mode == commandApprovalModeAdvisory && !evaluation.Allowed {
		_, _ = fmt.Fprintf(ctx.stderr, "postman: advisory: command approval is not approved yet (thread=%s); continuing\n", resolvedThreadID)
	}
	if *jsonOutput {
		result.Status = "executing"
		_ = writeExecuteBashMetadata(ctx.stderr, result)
	}

	startedAt := ctx.now()
	exitStatus, runErr := ctx.runBash(commandText, ctx.stdout, ctx.stderr)
	completedAt := ctx.now()
	if runErr != nil {
		result.Status = "error"
		result.ExitStatus = exitStatus
		result.Reason = runErr.Error()
		_ = recordCommandExecutionCompleted(sessionDir, resolvedContextID, resolvedSessionName, resolvedThreadID, policy, commandHash, commandText, *storeCommandText, startedAt, completedAt, exitStatus)
		_ = writeExecuteBashMetadata(ctx.stderr, result)
		return runErr
	}
	if err := recordCommandExecutionCompleted(sessionDir, resolvedContextID, resolvedSessionName, resolvedThreadID, policy, commandHash, commandText, *storeCommandText, startedAt, completedAt, exitStatus); err != nil {
		return err
	}
	if exitStatus != 0 {
		result.Status = "exited"
		result.ExitStatus = exitStatus
		_ = writeExecuteBashMetadata(ctx.stderr, result)
		return commandExitError{code: exitStatus}
	}
	if *jsonOutput {
		result.Status = "executed"
		result.ExitStatus = exitStatus
		_ = writeExecuteBashMetadata(ctx.stderr, result)
	}
	return nil
}

type executeBashDecisionOptions struct {
	sessionDir       string
	contextID        string
	sessionName      string
	threadID         string
	reviewer         string
	fallbackReviewer string
	decision         string
	reason           string
	storeCommandText bool
	commandText      string
}

func recordExecuteBashDecision(ctx commandContext, opts executeBashDecisionOptions) error {
	if strings.TrimSpace(opts.threadID) == "" {
		return fmt.Errorf("--thread-id is required with --record-decision")
	}
	reviewer := strings.TrimSpace(opts.reviewer)
	if reviewer == "" {
		reviewer = strings.TrimSpace(opts.fallbackReviewer)
	}
	if reviewer == "" {
		return fmt.Errorf("--reviewer is required with --record-decision when requester auto-detection is unavailable")
	}
	decision := strings.TrimSpace(opts.decision)
	switch decision {
	case string(journal.ApprovalDecisionApproved), string(journal.ApprovalDecisionRejected):
	default:
		return fmt.Errorf("--record-decision must be approved or rejected")
	}
	payload := journal.CommandApprovalDecisionPayload{
		Reviewer: reviewer,
		Decision: journal.ApprovalDecision(decision),
		Reason:   opts.reason,
	}
	if err := appendCommandEvent(opts.sessionDir, opts.contextID, opts.sessionName, journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, payload, opts.threadID, ctx.now()); err != nil {
		return err
	}
	result := executeBashResult{
		Status:         "decision_recorded",
		Reviewer:       reviewer,
		ThreadID:       opts.threadID,
		Decision:       decision,
		Reason:         opts.reason,
		CommandTextSet: opts.storeCommandText && opts.commandText != "",
	}
	return writeExecuteBashMetadata(ctx.stdout, result)
}

func resolveExecuteBashSessionName(ctx commandContext, flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return config.ValidateSessionName(strings.TrimSpace(flagValue))
	}
	sessionName := ctx.getTmuxSessionName()
	if sessionName == "" {
		return "", fmt.Errorf("tmux session name required: run inside tmux or pass --session")
	}
	return config.ValidateSessionName(sessionName)
}

func resolveExecuteBashContextID(baseDir, sessionName, flagValue string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		return config.ResolveContextID(strings.TrimSpace(flagValue))
	}
	return config.ResolveContextIDFromSession(baseDir, sessionName)
}

func resolveExecuteBashRequester(ctx commandContext, flagValue string) (string, error) {
	requester := strings.TrimSpace(flagValue)
	if requester == "" {
		requester = strings.TrimSpace(ctx.getTmuxPaneName())
	}
	if requester == "" {
		return "", fmt.Errorf("requester node required: set tmux pane title or pass --requester")
	}
	if err := cliutil.ValidateOutboundNodeName("requester", requester); err != nil {
		return "", err
	}
	return requester, nil
}

func resolveCommandApprovalPolicy(cfg *config.Config, requester, label, category, reviewerFlag, modeFlag string, ttlSeconds float64) (resolvedCommandApprovalPolicy, error) {
	policy := resolvedCommandApprovalPolicy{
		Requester: requester,
		Reviewer:  "unassigned",
		Mode:      commandApprovalModeAdvisory,
		Label:     label,
		Category:  category,
		TTL:       defaultCommandApprovalTTL,
	}
	for _, candidate := range cfg.CommandApproval {
		if !commandPolicyMatches(candidate.Requester, requester) || !commandPolicyMatches(candidate.Label, label) || !commandPolicyMatches(candidate.Category, category) {
			continue
		}
		if strings.TrimSpace(candidate.Reviewer) != "" {
			policy.Reviewer = strings.TrimSpace(candidate.Reviewer)
		}
		if strings.TrimSpace(candidate.Mode) != "" {
			policy.Mode = strings.TrimSpace(candidate.Mode)
		}
		if candidate.ApprovalTTLSeconds > 0 {
			policy.TTL = time.Duration(candidate.ApprovalTTLSeconds * float64(time.Second))
		}
		policy.ReviewerNodeOverride = strings.TrimSpace(candidate.ReviewerNode)
		break
	}
	if strings.TrimSpace(reviewerFlag) != "" {
		policy.Reviewer = strings.TrimSpace(reviewerFlag)
	}
	if strings.TrimSpace(modeFlag) != "" {
		policy.Mode = strings.TrimSpace(modeFlag)
	}
	if ttlSeconds > 0 {
		policy.TTL = time.Duration(ttlSeconds * float64(time.Second))
	}
	switch policy.Mode {
	case commandApprovalModeAdvisory, commandApprovalModeWarnOnly, commandApprovalModeBlocking:
	default:
		return resolvedCommandApprovalPolicy{}, fmt.Errorf("unsupported command approval mode %q", policy.Mode)
	}
	if policy.TTL <= 0 {
		policy.TTL = defaultCommandApprovalTTL
	}
	return policy, nil
}

func commandPolicyMatches(pattern, value string) bool {
	pattern = strings.TrimSpace(pattern)
	return pattern == "" || pattern == "*" || pattern == value
}

func commandDigest(commandText string) string {
	sum := sha256.Sum256([]byte(commandText))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func commandApprovalThreadID(policy resolvedCommandApprovalPolicy, commandHash string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{policy.Requester, policy.Reviewer, policy.Label, policy.Category, commandHash}, "\x00")))
	return "command-approval-" + hex.EncodeToString(sum[:8])
}

// commandApprovalDecisionAutoApprovedNoReviewer is the distinct decision
// label used whenever the unified fail-open rule (#626) applies: no valid
// reviewer_node is configured, so the command is approved regardless of
// mode. This must never be conflated with an actual recorded approval.
const commandApprovalDecisionAutoApprovedNoReviewer = "auto_approved_no_reviewer"

func evaluateCommandApproval(sessionDir string, policy resolvedCommandApprovalPolicy, threadID, commandHash string, validReviewer bool, now time.Time) (commandApprovalEvaluation, error) {
	if !validReviewer {
		// #626 decided requirement 1 (unified fail-open rule): unless a
		// valid reviewer_node is configured, every command is treated as
		// approved across all three modes, including blocking. This is
		// evaluated before any projection lookup so a missing/unresolvable
		// reviewer_node never depends on prior approval state.
		return commandApprovalEvaluation{
			Decision: commandApprovalDecisionAutoApprovedNoReviewer,
			Allowed:  true,
			Reason:   "no valid reviewer_node configured; command approval fails open",
		}, nil
	}
	state, ok, err := projection.ProjectCommandApprovalState(sessionDir, now)
	if err != nil {
		return commandApprovalEvaluation{}, err
	}
	if ok {
		if thread, found := state.Threads[threadID]; found {
			if thread.CommandHash != "" && thread.CommandHash != commandHash {
				return commandApprovalEvaluation{
					Decision: "digest_mismatch",
					Allowed:  false,
					Reason:   "approval exists for a different command digest",
					Thread:   &thread,
				}, nil
			}
			return evaluationForThread(thread), nil
		}
		for _, thread := range state.Threads {
			if sameCommandApprovalKey(thread, policy) && thread.CommandHash != commandHash && thread.Status == projection.CommandApprovalStatusApproved {
				return commandApprovalEvaluation{
					Decision: "digest_mismatch",
					Allowed:  false,
					Reason:   "approval exists for a different command digest",
					Thread:   &thread,
				}, nil
			}
		}
	}
	return commandApprovalEvaluation{
		Decision: "absent",
		Allowed:  false,
		Reason:   "approval is absent",
	}, nil
}

func sameCommandApprovalKey(thread projection.CommandApprovalThread, policy resolvedCommandApprovalPolicy) bool {
	return thread.Requester == policy.Requester &&
		thread.Reviewer == policy.Reviewer &&
		thread.Label == policy.Label &&
		thread.Category == policy.Category
}

func evaluationForThread(thread projection.CommandApprovalThread) commandApprovalEvaluation {
	evaluation := commandApprovalEvaluation{Thread: &thread}
	switch thread.Status {
	case projection.CommandApprovalStatusApproved:
		evaluation.Decision = "approved"
		evaluation.Allowed = true
		evaluation.Reason = "approval is approved"
	case projection.CommandApprovalStatusRejected:
		evaluation.Decision = "rejected"
		evaluation.Reason = "approval is rejected"
	case projection.CommandApprovalStatusExpired:
		evaluation.Decision = "expired"
		evaluation.Reason = "approval is expired"
	case projection.CommandApprovalStatusWrongReviewer:
		evaluation.Decision = "wrong_reviewer"
		evaluation.Reason = "approval decision reviewer does not match policy reviewer"
	case projection.CommandApprovalStatusStale:
		evaluation.Decision = "stale"
		evaluation.Reason = "approval decision is stale or has no matching request"
	default:
		evaluation.Decision = "pending"
		evaluation.Reason = "approval is pending"
	}
	return evaluation
}

func decisionForPolicy(mode string, evaluation commandApprovalEvaluation, override bool) string {
	if evaluation.Decision == commandApprovalDecisionAutoApprovedNoReviewer {
		return evaluation.Decision
	}
	if evaluation.Allowed {
		return "approved"
	}
	switch mode {
	case commandApprovalModeAdvisory:
		return "advisory_unapproved"
	case commandApprovalModeWarnOnly:
		if override {
			return "warn_override"
		}
		return "warn_missing_approval"
	case commandApprovalModeBlocking:
		return "blocked"
	default:
		return "blocked"
	}
}

func blockedCommandApprovalReason(mode string, evaluation commandApprovalEvaluation, override bool) string {
	if evaluation.Allowed || mode == commandApprovalModeAdvisory || (mode == commandApprovalModeWarnOnly && override) {
		return ""
	}
	switch mode {
	case commandApprovalModeWarnOnly:
		return evaluation.Reason + "; warn-only mode requires --override-approval to continue"
	case commandApprovalModeBlocking:
		return evaluation.Reason + "; blocking mode refuses wrapper-mediated execution"
	default:
		return evaluation.Reason
	}
}

func recordCommandApprovalRequest(sessionDir, contextID, sessionName, threadID string, policy resolvedCommandApprovalPolicy, commandHash, reason, expiresAt, commandText string, storeCommandText bool, now time.Time) error {
	payload := journal.CommandApprovalRequestPayload{
		Requester:   policy.Requester,
		Reviewer:    policy.Reviewer,
		Mode:        policy.Mode,
		Label:       policy.Label,
		Category:    policy.Category,
		CommandHash: commandHash,
		Reason:      reason,
		ExpiresAt:   expiresAt,
	}
	if storeCommandText {
		payload.CommandText = commandText
	}
	return appendCommandEvent(sessionDir, contextID, sessionName, journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, payload, threadID, now)
}

func recordCommandExecutionDecision(sessionDir, contextID, sessionName, threadID string, policy resolvedCommandApprovalPolicy, commandHash, decision, reason string, override bool, commandText string, storeCommandText bool, now time.Time) error {
	payload := journal.CommandExecutionDecisionPayload{
		Requester:      policy.Requester,
		Reviewer:       policy.Reviewer,
		Mode:           policy.Mode,
		Label:          policy.Label,
		Category:       policy.Category,
		CommandHash:    commandHash,
		Decision:       decision,
		Reason:         reason,
		Override:       override,
		ApprovalThread: threadID,
	}
	if storeCommandText {
		payload.CommandText = commandText
	}
	return appendCommandEvent(sessionDir, contextID, sessionName, journal.CommandExecutionDecidedEventType, journal.VisibilityOperatorVisible, payload, threadID, now)
}

func recordCommandExecutionCompleted(sessionDir, contextID, sessionName, threadID string, policy resolvedCommandApprovalPolicy, commandHash, commandText string, storeCommandText bool, startedAt, completedAt time.Time, exitStatus int) error {
	payload := journal.CommandExecutionCompletedPayload{
		Requester:      policy.Requester,
		Reviewer:       policy.Reviewer,
		Mode:           policy.Mode,
		Label:          policy.Label,
		Category:       policy.Category,
		CommandHash:    commandHash,
		ApprovalThread: threadID,
		StartedAt:      startedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt:    completedAt.UTC().Format(time.RFC3339Nano),
		DurationMillis: completedAt.Sub(startedAt).Milliseconds(),
		ExitStatus:     exitStatus,
	}
	if storeCommandText {
		payload.CommandText = commandText
	}
	return appendCommandEvent(sessionDir, contextID, sessionName, journal.CommandExecutionCompletedEventType, journal.VisibilityOperatorVisible, payload, threadID, completedAt)
}

func appendCommandEvent(sessionDir, contextID, sessionName, eventType string, visibility journal.Visibility, payload interface{}, threadID string, now time.Time) error {
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		writer, err = journal.OpenShadowWriter(sessionDir, contextID, sessionName, os.Getpid(), now)
		if err != nil {
			return err
		}
	}
	_, err = writer.AppendEventWithOptions(eventType, visibility, payload, journal.AppendOptions{ThreadID: threadID}, now)
	return err
}

func writeExecuteBashMetadata(w io.Writer, result executeBashResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
