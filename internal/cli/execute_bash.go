package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/nodeaddr"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

const (
	commandApprovalModeAdvisory = "advisory"
	commandApprovalModeWarnOnly = "warn-only"
	commandApprovalModeBlocking = "blocking"
	defaultCommandApprovalTTL   = 15 * time.Minute

	// defaultCommandApprovalWaitTimeoutSeconds bounds --wait when the caller
	// does not pass --wait-timeout-seconds explicitly.
	defaultCommandApprovalWaitTimeoutSeconds = 300.0
	// commandApprovalPollInterval is how often --wait re-checks the approval
	// projection while waiting for the approver's decision.
	commandApprovalPollInterval = 500 * time.Millisecond
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

// commandApprovalOutcomeError carries a distinguishable status/exit code for
// every way a blocking-mode call can end without running the command
// (#823 acceptance criteria: "Rejection, expiry, cancellation, and approver
// loss produce distinguishable final statuses, reasons, and exit codes").
//
// IMPORTANT (#823 F-006): these exit codes are NOT guaranteed distinct from
// an executed command's own exit status. main.go forwards ExitCode()
// directly as the process's exit code for both this type and
// commandExitError (the executed command's own status, 0-255) — a command
// that itself exits 10 is numerically indistinguishable, on the bare exit
// code alone, from a rejected approval that also exits 10. The
// authoritative way to tell "the approval was rejected/expired/..." apart
// from "the command ran and happened to exit with that same number" is the
// --json wrapper metadata's `status` field: one of the decision names below
// (never running the command) versus "exited" with `exit_status` set (the
// command ran). Do not rely on the bare numeric exit code alone when that
// distinction matters; parse --json stderr output instead.
type commandApprovalOutcomeError struct {
	status string
	reason string
}

func (e commandApprovalOutcomeError) Error() string {
	return e.reason
}

func (e commandApprovalOutcomeError) ExitCode() int {
	switch e.status {
	case "rejected":
		return 10
	case "expired":
		return 11
	case "wait_timeout":
		return 12
	case "cancelled":
		return 13
	case "digest_mismatch":
		return 14
	case "wrong_reviewer":
		return 15
	case "stale":
		return 16
	case "historical_only":
		return 17
	case "requester_mismatch":
		return 18
	case "delivery_failed":
		return 19
	case "approver_lost":
		return 20
	case "session_changed":
		return 21
	case "already_executed":
		return 22
	default:
		return 1
	}
}

// commandApprovalBlockedStatus maps an evaluation's decision to the
// executeBashResult.Status value reported for a blocked/non-executed
// outcome. It is deliberately independent of decisionForPolicy's "blocked"
// audit-trail decision label (recorded in the durable journal and asserted
// by existing tests) — this only refines the JSON wrapper metadata surfaced
// to the caller.
func commandApprovalBlockedStatus(decision string) string {
	switch decision {
	case "rejected", "expired", "digest_mismatch", "wrong_reviewer", "stale", "historical_only",
		"requester_mismatch", "delivery_failed", "approver_lost", "session_changed":
		return decision
	case "wait_timeout":
		return "wait_timeout"
	case "wait_cancelled":
		return "cancelled"
	default:
		return "blocked"
	}
}

type resolvedCommandApprovalPolicy struct {
	Requester string
	Reviewer  string
	Mode      string
	Label     string
	Category  string
	TTL       time.Duration
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
	noWait := fs.Bool("no-wait", false, "in blocking mode, return the current pending/blocked result immediately instead of waiting for the matching approval thread to resolve")
	waitTimeoutSeconds := fs.Float64("wait-timeout-seconds", defaultCommandApprovalWaitTimeoutSeconds, "how long blocking mode waits for a decision before giving up, unless --no-wait is set")
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

	if *recordDecision != "" {
		return recordExecuteBashDecision(ctx, executeBashDecisionOptions{
			sessionDir:       sessionDir,
			contextID:        resolvedContextID,
			sessionName:      resolvedSessionName,
			threadID:         *threadID,
			decision:         *recordDecision,
			reason:           *reason,
			storeCommandText: *storeCommandText,
			commandText:      *command,
		})
	}

	resolvedRequester, err := resolveExecuteBashRequester(ctx, *requester)
	if err != nil {
		return err
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
	if err := validateCommandApprovalThreadID(resolvedThreadID); err != nil {
		return err
	}
	if resolvedThreadID == "" {
		resolvedThreadID = commandApprovalThreadID(policy, commandHash)
	}
	expiresAt := ctx.now().Add(policy.TTL).UTC().Format(time.RFC3339Nano)
	commandApproverNode, validReviewer := cfg.ResolveCommandApproverNode()
	approverConfigured := strings.TrimSpace(cfg.CommandApproverNode) != ""
	requesterAddress := nodeaddr.Full(policy.Requester, resolvedSessionName)
	commandApproverAddress := nodeaddr.Full(commandApproverNode, resolvedSessionName)

	evaluation, err := evaluateCommandApproval(sessionDir, policy, resolvedThreadID, commandHash, requesterAddress, commandApproverAddress, approverConfigured, validReviewer, ctx.now())
	if err != nil {
		return err
	}
	// #823 rework-2 (F-002/F-012/F-013): one unified request lifecycle for
	// every non-allowed, valid-reviewer decision evaluateCommandApproval can
	// return:
	//   - "absent": no request exists yet for this deterministic thread id;
	//     atomically create one (only a config-resolved reviewer may
	//     receive a trusted request; a configured-but-unresolvable blocking
	//     reviewer never reaches here and stays locally blocked below).
	//   - "pending": a request already exists and is undecided; reuse its
	//     exact correlation and idempotently re-deliver it. This recovers a
	//     request whose original delivery failed (#823 F-013) without
	//     minting a second request event, and is harmless to repeat when
	//     the original delivery actually succeeded.
	//   - "rejected"/"expired"/"stale"/"historical_only"/"wrong_reviewer":
	//     the thread id is deterministic (requester+reviewer+label+
	//     category+command_hash), so without this branch any of these
	//     terminal outcomes would permanently block every future retry of
	//     the identical command (#823 F-012 regression). Atomically mint a
	//     fresh request that supersedes the SPECIFIC terminal request this
	//     call observed.
	// Every other decision (digest_mismatch, requester_mismatch,
	// unresolved_command_approver, approved) falls through unchanged: those
	// are refusals, or an already-decided approval that F-001's claim below
	// governs, and this lifecycle must never mint a new request over them.
	originalInputRequestID := ""
	if evaluation.Thread != nil {
		originalInputRequestID = evaluation.Thread.InputRequestID
	}
	if !evaluation.Allowed && validReviewer {
		mint := false
		supersedes := ""
		redeliver := false
		// needsPendingRelabel is true only when the PRIOR decision was a
		// stale terminal one (rejected/expired/stale/historical_only/
		// wrong_reviewer) that a fresh mint has now superseded: leaving
		// evaluation as that stale terminal decision would both misreport
		// the outcome (a fresh request is now pending, not still rejected)
		// and, for "rejected"/etc., fail to satisfy shouldWaitForCommandApproval's
		// switch, which only treats "absent"/"pending" as engageable. The
		// plain first-time "absent" case is deliberately left untouched
		// (evaluation keeps reporting "absent") to preserve every existing
		// caller's exact status/reason text for that path -- "absent" is
		// already engageable, so no relabel is needed there either.
		needsPendingRelabel := false
		switch evaluation.Decision {
		case "absent":
			mint = true
		case "pending":
			redeliver = true
		case "rejected", "expired", "stale", "historical_only", "wrong_reviewer":
			mint = true
			supersedes = originalInputRequestID
			needsPendingRelabel = true
		}
		if mint {
			outcome, err := atomicCreateOrReplaceRequest(sessionDir, resolvedContextID, resolvedSessionName, resolvedThreadID, policy, commandApproverNode, commandHash, *reason, expiresAt, commandText, *storeCommandText, supersedes, ctx.now())
			if err != nil {
				return err
			}
			originalInputRequestID = outcome.inputRequestID
			redeliver = true
		}
		if redeliver {
			if err := deliverCommandApprovalRequest(cfg, baseDir, resolvedContextID, resolvedSessionName, policy, commandApproverNode, resolvedThreadID, originalInputRequestID, commandHash, *reason, *storeCommandText, ctx.now()); err != nil {
				// #823 F-003/F-013: delivery failure fails fast with a
				// distinct outcome instead of silently overwriting the
				// reason and then wasting the full wait timeout on a
				// request the approver never saw. A later call against this
				// same still-pending correlation retries delivery without
				// minting a new request event.
				evaluation = commandApprovalEvaluation{Decision: "delivery_failed", Reason: err.Error()}
			} else if needsPendingRelabel {
				evaluation = commandApprovalEvaluation{Decision: "pending", Reason: "approval is pending"}
			}
		}
	}

	// #823 F-004/F-005: waitCtx and the starting session identity/deadline
	// are kept in outer scope, and cancelWait is deliberately NOT called
	// right after the wait returns — it stays alive through the F-001 claim
	// below via waitInterruption's re-check there, so a SIGINT/SIGTERM,
	// deadline expiry, or session/context change landing in the gap between
	// the wait's return and the claim can still be observed instead of
	// silently claiming and running anyway.
	var (
		waitCtx                  context.Context
		cancelWait               = func() {}
		waited                   bool
		waitStartSessionKey      string
		waitStartGeneration      int
		waitSessionIdentityKnown bool
		waitDeadline             time.Time
	)
	if evaluation.Decision != "delivery_failed" && shouldWaitForCommandApproval(policy.Mode, evaluation, validReviewer, approverConfigured, *noWait, *waitTimeoutSeconds) {
		waited = true
		waitStartSessionKey, waitStartGeneration, waitSessionIdentityKnown = projection.CurrentSessionIdentity(sessionDir)
		waitDeadline = ctx.now().Add(time.Duration(*waitTimeoutSeconds * float64(time.Second)))
		waitCtx, cancelWait = ctx.newInterruptContext()
		waitedEvaluation, err := waitForCommandApprovalDecision(waitCtx, ctx, commandApprovalWaitParams{
			sessionDir:             sessionDir,
			baseDir:                baseDir,
			contextID:              resolvedContextID,
			sessionName:            resolvedSessionName,
			threadID:               resolvedThreadID,
			commandHash:            commandHash,
			requesterAddress:       requesterAddress,
			commandApproverNode:    commandApproverNode,
			commandApproverAddress: commandApproverAddress,
			originalInputRequestID: originalInputRequestID,
			policyLabel:            policy.Label,
			policyCategory:         policy.Category,
			policyMode:             policy.Mode,
			deadline:               waitDeadline,
		})
		if err != nil {
			cancelWait()
			return err
		}
		evaluation = waitedEvaluation
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
		result.Status = commandApprovalBlockedStatus(evaluation.Decision)
		result.Reason = blockReason
		_ = writeExecuteBashMetadata(ctx.stderr, result)
		return commandApprovalOutcomeError{status: result.Status, reason: blockReason}
	}
	if policy.Mode == commandApprovalModeWarnOnly && *overrideApproval && !evaluation.Allowed {
		_, _ = fmt.Fprintf(ctx.stderr, "postman: warning: command approval absent; continuing because --override-approval was set (thread=%s)\n", resolvedThreadID)
	}
	if policy.Mode == commandApprovalModeAdvisory && !evaluation.Allowed {
		_, _ = fmt.Fprintf(ctx.stderr, "postman: advisory: command approval is not approved yet (thread=%s); continuing\n", resolvedThreadID)
	}
	// #823 F-001: a real, blocking-mode, thread-backed approval is single-use.
	// Claim it atomically before running the command — concurrent waiters on
	// the same thread, and a repeated call while the approval is still
	// within its TTL, must each observe the claim and run the command at
	// most once in total. Fail-open (auto_approved_no_reviewer, no Thread)
	// and non-blocking modes are unaffected: there is no scarce human
	// decision to consume there.
	if policy.Mode == commandApprovalModeBlocking && evaluation.Decision == "approved" && evaluation.Thread != nil {
		if waited {
			// #823 F-004/F-005: one more re-check, using the SAME waitCtx
			// (still alive) and the SAME starting session identity/deadline
			// the wait itself used, immediately before claiming.
			if interruption := commandApprovalWaitInterruption(waitCtx, ctx, commandApprovalWaitParams{sessionDir: sessionDir, deadline: waitDeadline}, waitStartSessionKey, waitStartGeneration, waitSessionIdentityKnown); interruption != nil {
				cancelWait()
				status := commandApprovalBlockedStatus(interruption.Decision)
				result.Status = status
				result.Reason = interruption.Reason
				_ = writeExecuteBashMetadata(ctx.stderr, result)
				return commandApprovalOutcomeError{status: status, reason: interruption.Reason}
			}
		}
		claimInputRequestID := evaluation.Thread.InputRequestID
		claimed, claimErr := claimCommandExecution(sessionDir, resolvedContextID, resolvedSessionName, resolvedThreadID, claimInputRequestID, commandHash, policy.Requester, ctx.now())
		cancelWait()
		if claimErr != nil {
			return claimErr
		}
		if !claimed {
			result.Status = "already_executed"
			result.Reason = "this approval has already been claimed and executed once; approvals are single-use"
			_ = writeExecuteBashMetadata(ctx.stderr, result)
			return commandApprovalOutcomeError{status: result.Status, reason: result.Reason}
		}
	} else {
		cancelWait()
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
	decision         string
	reason           string
	storeCommandText bool
	commandText      string
}

// recordCommandApprovalAutoFillFn is a seam over
// journal.RecordMailboxPayloadIfAbsentUsingCurrentSessionWriter: tests
// override this var to inject a failure on one call and restore it via
// t.Cleanup, so the F-034 already-decided-retry path can be exercised without
// needing a real journal write failure.
var recordCommandApprovalAutoFillFn = journal.RecordMailboxPayloadIfAbsentUsingCurrentSessionWriter

func recordExecuteBashDecision(ctx commandContext, opts executeBashDecisionOptions) error {
	if strings.TrimSpace(opts.threadID) == "" {
		return fmt.Errorf("--thread-id is required with --record-decision")
	}
	if err := validateCommandApprovalThreadID(strings.TrimSpace(opts.threadID)); err != nil {
		return err
	}
	decision := strings.TrimSpace(opts.decision)
	switch decision {
	case string(journal.ApprovalDecisionApproved), string(journal.ApprovalDecisionRejected):
	default:
		return fmt.Errorf("--record-decision must be approved or rejected")
	}

	// #626 B1-residual: the decision's reviewer identity is the CALLER's
	// own authenticated identity (tmux pane title), never a flag.
	// --reviewer must never influence whether a decision is accepted — a
	// requester could otherwise pass --reviewer <command_approver_node_name>,
	// trivially readable from postman.toml or get-status, and self-approve
	// exactly as before. The caller is accepted only when this
	// authenticated identity matches the thread's own CommandApproverNode, the
	// trusted, config-resolved value captured once at request time (#626
	// B1) — never re-resolved from current config, so a decision can't be
	// laundered through a config change between request and decision time
	// either.
	authenticatedCaller := strings.TrimSpace(ctx.getTmuxPaneName())
	if authenticatedCaller == "" {
		return fmt.Errorf("--record-decision requires a resolvable tmux pane title identity; run inside tmux")
	}
	state, ok, err := projection.ProjectCommandApprovalState(opts.sessionDir, ctx.now())
	if err != nil {
		return err
	}
	var thread projection.CommandApprovalThread
	if ok {
		if projectedThread, found := state.Threads[opts.threadID]; found {
			thread = projectedThread
		}
	}
	authenticatedCallerAddress := nodeaddr.Full(authenticatedCaller, opts.sessionName)
	if thread.CommandApproverAddress == "" || authenticatedCallerAddress != thread.CommandApproverAddress {
		return fmt.Errorf("--record-decision refused: caller %q is not the configured command_approver_node for thread %q", authenticatedCaller, opts.threadID)
	}
	if thread.InputRequestID == "" || thread.CommandHash == "" {
		return fmt.Errorf("--record-decision refused: thread %q is missing exact command approval correlation metadata", opts.threadID)
	}

	// #786/F-034: once a thread is Approved/Rejected,
	// applyCommandApprovalDecision (projection/command_approval.go) ignores
	// every later decision event for it and keeps thread.DecisionMessageID
	// fixed at the FIRST accepted decision's message id. The reply-slot
	// resolver only accepts a fill whose MessageID equals that fixed
	// DecisionMessageID. A freshly minted decisionMessageID on every call
	// therefore can never match after the first decision: if the very first
	// auto-fill append below failed (e.g. a transient journal write error)
	// after the decision event itself had already landed, every retry would
	// mint a new id that could never resolve the slot, permanently stranding
	// it -- worse than the pre-fix bug, since the old manual mail-reply
	// escape hatch mints its own new message id too and has the identical
	// problem. Detecting an already-decided thread and reusing its recorded
	// DecisionMessageID (rather than deciding again) makes retries actually
	// converge.
	alreadyDecided := thread.Status == projection.CommandApprovalStatusApproved || thread.Status == projection.CommandApprovalStatusRejected
	var decisionMessageID string
	if alreadyDecided {
		if thread.DecisionMessageID == "" {
			return fmt.Errorf("--record-decision refused: thread %q is already decided but has no recorded decision message id; cannot safely retry the auto-fill", opts.threadID)
		}
		decisionMessageID = thread.DecisionMessageID
		decision = string(thread.Status)
	} else {
		decisionMessageID, err = message.GenerateFilename(ctx.now().Format("20060102-150405"), authenticatedCaller, thread.Requester, opts.sessionName)
		if err != nil {
			return fmt.Errorf("generating command approval decision message id: %w", err)
		}

		payload := journal.CommandApprovalDecisionPayload{
			Reviewer:         authenticatedCaller,
			ReviewerAddress:  authenticatedCallerAddress,
			RequesterAddress: thread.RequesterAddress,
			Decision:         journal.ApprovalDecision(decision),
			Reason:           opts.reason,
			MessageID:        decisionMessageID,
			InputRequestID:   thread.InputRequestID,
			CommandHash:      thread.CommandHash,
		}
		if err := appendCommandEvent(opts.sessionDir, opts.contextID, opts.sessionName, journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, payload, opts.threadID, ctx.now()); err != nil {
			return err
		}
	}
	// Auto-fill the paired mailbox input_request directly instead of requiring a
	// separate --fills-input-request-id mail reply from the approver. The
	// caller's tmux pane identity checked above is already stronger
	// authentication than a mail reply's spoofable envelope `from:` field, so
	// routing this through the mail-reply trust check
	// (isTrustedCommandApprovalDecision) would add no security while making
	// approver's ability to close a decision depend on having a postman.md
	// topology edge to every possible requester. This synthesizes a
	// same-session mailbox-projection event in opts.sessionDir, which is
	// correct wherever approver and the requester share one tmux session
	// (this fleet's current topology); a genuinely cross-session requester
	// (future diplomat_node relay) is out of scope until that feature exists.
	fillContent := fmt.Sprintf(`---
params:
  messageId: %s
  from: %s
  to: %s
  thread_id: %s
  command_hash: %s
  fills_input_request_id: %s
---

# Message
`, decisionMessageID, authenticatedCaller, thread.Requester, opts.threadID, thread.CommandHash, thread.InputRequestID)
	fillPayload := journal.MailboxEventPayload{
		MessageID:           decisionMessageID,
		From:                authenticatedCaller,
		To:                  thread.Requester,
		ThreadID:            opts.threadID,
		FillsInputRequestID: thread.InputRequestID,
		Content:             fillContent,
	}
	// #786/F-035: match on the full correlation the reply-slot resolver
	// actually keys on (message id, thread id, command hash, from, to, fills
	// id), not just FillsInputRequestID alone. A narrower predicate could
	// make AppendCurrentSessionEventIfAbsent wrongly treat an unrelated
	// stale/mismatched event that happens to share only the fill id as
	// equivalent, silently skipping the one fill that would actually have
	// resolved the slot. Command hash isn't a MailboxEventPayload struct
	// field, so it's compared via the parsed envelope Content, same as the
	// resolver itself does.
	fillEquivalent := func(event journal.Event) (bool, error) {
		if event.Type != projection.MailboxProjectionPostConsumedEventType {
			return false, nil
		}
		var got journal.MailboxEventPayload
		if err := json.Unmarshal(event.Payload, &got); err != nil {
			return false, err
		}
		if got.MessageID != fillPayload.MessageID ||
			got.ThreadID != fillPayload.ThreadID ||
			got.From != fillPayload.From ||
			got.To != fillPayload.To ||
			got.FillsInputRequestID != fillPayload.FillsInputRequestID {
			return false, nil
		}
		gotMeta, err := envelope.ParseMetadata(got.Content)
		if err != nil {
			return false, nil
		}
		return gotMeta.CommandHash == thread.CommandHash, nil
	}
	if _, err := recordCommandApprovalAutoFillFn(opts.sessionDir, opts.contextID, opts.sessionName, projection.MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, fillPayload, fillEquivalent, ctx.now()); err != nil {
		return fmt.Errorf("recording auto-fill for input request %q: %w", thread.InputRequestID, err)
	}
	if !alreadyDecided {
		if err := journal.SyncCommandApprovalDecisionHistory(opts.sessionDir); err != nil {
			_, _ = fmt.Fprintf(ctx.stderr, "postman: warning: command approval decision history sync failed after recording decision: %v\n", err)
		}
	}
	result := executeBashResult{
		Status:         "decision_recorded",
		Reviewer:       authenticatedCaller,
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
		Mode:      commandApprovalModeBlocking,
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

var commandApprovalThreadIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validateCommandApprovalThreadID rejects a --thread-id containing anything
// outside a conservative safe charset (#626 M1). resolvedThreadID gets
// interpolated directly into hand-built YAML frontmatter in
// deliverCommandApprovalRequest via fmt.Sprintf, and that frontmatter is
// parsed by a naive line scanner (internal/envelope) — a newline plus
// indentation in an otherwise-unvalidated --thread-id could inject or
// override other params fields (for example forcing replyPolicy: none to
// hide the delivered request). An empty value is fine here; the caller then
// derives a fresh, safe thread id via commandApprovalThreadID.
func validateCommandApprovalThreadID(threadID string) error {
	if threadID == "" {
		return nil
	}
	if !commandApprovalThreadIDPattern.MatchString(threadID) {
		return fmt.Errorf("--thread-id %q contains characters outside [A-Za-z0-9._-]", threadID)
	}
	return nil
}

// commandApprovalDecisionAutoApprovedNoReviewer is the distinct decision
// label used whenever the unified fail-open rule (#626) applies: no valid
// command_approver_node is configured, so the command is approved regardless of
// mode. This must never be conflated with an actual recorded approval.
const commandApprovalDecisionAutoApprovedNoReviewer = "auto_approved_no_reviewer"

func evaluateCommandApproval(sessionDir string, policy resolvedCommandApprovalPolicy, threadID, commandHash, requesterAddress, commandApproverAddress string, approverConfigured, validReviewer bool, now time.Time) (commandApprovalEvaluation, error) {
	if !validReviewer {
		if approverConfigured && policy.Mode == "blocking" {
			return commandApprovalEvaluation{Decision: "unresolved_command_approver", Allowed: false, Reason: "configured command_approver_node is not resolvable; blocking approval fails closed"}, nil
		}
		// An unset approver, or a non-blocking policy with an unresolvable one,
		// fails open. A configured unresolvable blocking approver returned above
		// fails closed. This is evaluated before any projection lookup so an
		// unresolved command_approver_node never depends on prior approval state.
		return commandApprovalEvaluation{
			Decision: commandApprovalDecisionAutoApprovedNoReviewer,
			Allowed:  true,
			Reason:   "no valid command_approver_node configured; command approval fails open",
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
			// #823 F-002: a thread found via an explicit --thread-id
			// override may belong to a different requester, a different
			// trusted approver, or a different policy even with a matching
			// digest. Never let that thread's approval authorize THIS
			// invocation unless the full correlation matches. Both sides
			// are required to be non-empty here (unlike the stricter
			// wait-loop check) because an empty thread.RequesterAddress/
			// CommandApproverAddress is the deliberate legacy-addressless
			// signal evaluationForThread's HistoricalOnly branch already
			// handles distinctly (#626) — this is a single evaluation at
			// call time, not a value that can regress mid-wait.
			if thread.RequesterAddress != "" && requesterAddress != "" && thread.RequesterAddress != requesterAddress {
				return commandApprovalEvaluation{
					Decision: "requester_mismatch",
					Allowed:  false,
					Reason:   "approval thread belongs to a different requester",
					Thread:   &thread,
				}, nil
			}
			if thread.CommandApproverAddress != "" && commandApproverAddress != "" && thread.CommandApproverAddress != commandApproverAddress {
				return commandApprovalEvaluation{
					Decision: "requester_mismatch",
					Allowed:  false,
					Reason:   "approval thread was requested from a different trusted approver",
					Thread:   &thread,
				}, nil
			}
			// Guarded on thread.Mode != "" (always set for a real recorded
			// request, never for a stale/legacy placeholder thread — see
			// applyCommandApprovalDecision's decision-without-a-matching-
			// request branch, which fills in only ThreadID/Reviewer/
			// Status/Reason/DecidedAt): a stale placeholder must still
			// reach evaluationForThread's "stale" diagnosis below, not get
			// misclassified here as a policy mismatch.
			if thread.Mode != "" && (thread.Label != policy.Label || thread.Category != policy.Category || thread.Mode != policy.Mode) {
				return commandApprovalEvaluation{
					Decision: "requester_mismatch",
					Allowed:  false,
					Reason:   "approval thread belongs to a different policy (label/category/mode)",
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

// shouldWaitForCommandApproval reports whether blocking mode should engage
// the polling loop instead of returning the current blocked/pending result
// immediately (#823: waiting is the default for blocking mode; --no-wait
// opts back into the legacy immediate-return behavior). It only ever
// applies to blocking mode with a trusted, resolvable reviewer (the same
// gate evaluateCommandApproval uses to decide whether to deliver a request
// at all) and only when the decision is not already terminal, so it never
// changes fail-open (#626) or fail-closed (unresolvable reviewer) behavior,
// and never re-waits on an already-rejected/expired/digest-mismatched
// thread.
func shouldWaitForCommandApproval(mode string, evaluation commandApprovalEvaluation, validReviewer, approverConfigured, noWait bool, waitTimeoutSeconds float64) bool {
	if noWait || mode != commandApprovalModeBlocking || waitTimeoutSeconds <= 0 {
		return false
	}
	if !validReviewer || !approverConfigured {
		return false
	}
	switch evaluation.Decision {
	case "absent", "pending":
		return true
	default:
		return false
	}
}

// commandApprovalWaitParams pins the correlation waitForCommandApprovalDecision
// re-checks on every poll: the exact thread/command-digest/requester it
// started with (#823 F-002), plus enough to detect mid-wait approver loss
// (#823 F-003) and a context/session-generation change (#823 F-004).
type commandApprovalWaitParams struct {
	sessionDir             string
	baseDir                string
	contextID              string
	sessionName            string
	threadID               string
	commandHash            string
	requesterAddress       string
	commandApproverNode    string
	commandApproverAddress string
	originalInputRequestID string
	policyLabel            string
	policyCategory         string
	policyMode             string
	deadline               time.Time
}

// approverLivenessCheckEveryNPolls bounds how often waitForCommandApprovalDecision
// re-verifies the approver is still discoverable (#823 F-003): cheap enough
// to run every poll would be fine, but checking every few polls keeps the
// dominant cost the same lightweight projection read while still detecting
// a lost approver within a few seconds.
const approverLivenessCheckEveryNPolls = 4

// commandApprovalWaitInterruption checks, in order, whether waitCtx has been
// cancelled, whether the deadline has passed, and whether the session's
// context/generation has changed since the wait started. It returns a
// non-nil terminal evaluation the instant any of these fired, and nil
// otherwise. waitForCommandApprovalDecision calls this both at the top of
// every poll iteration and again immediately before accepting an approved
// decision (#823 F-004/F-005), so a cancellation, deadline, or session
// change landing in the narrow window between reading the projection and
// returning can never be raced by a same-instant approval — and the second
// call site is reused verbatim, right before claimCommandExecution, in
// runExecuteBashWithContext.
func commandApprovalWaitInterruption(waitCtx context.Context, ctx commandContext, p commandApprovalWaitParams, startSessionKey string, startGeneration int, sessionIdentityKnown bool) *commandApprovalEvaluation {
	select {
	case <-waitCtx.Done():
		return &commandApprovalEvaluation{
			Decision: "wait_cancelled",
			Reason:   fmt.Sprintf("approval wait was cancelled: %v", waitCtx.Err()),
		}
	default:
	}
	if !ctx.now().Before(p.deadline) {
		return &commandApprovalEvaluation{
			Decision: "wait_timeout",
			Reason:   "approval wait timed out before a decision was recorded",
		}
	}
	// A context or session-generation change ends the wait with a distinct
	// terminal status. Without this, projection.ProjectCommandApprovalState
	// silently filters out every event from the OLD generation once the
	// current session state moves to a new one, so `ok` would just go false
	// and the loop would poll on, uninformatively, toward a generic timeout
	// that masks what actually happened.
	if sessionIdentityKnown {
		curKey, curGeneration, curOK := projection.CurrentSessionIdentity(p.sessionDir)
		if !curOK || curKey != startSessionKey || curGeneration != startGeneration {
			return &commandApprovalEvaluation{
				Decision: "session_changed",
				Reason:   "the session's context or generation changed while waiting for approval",
			}
		}
	}
	return nil
}

// waitForCommandApprovalDecision polls the command approval projection until
// the thread reaches a terminal decision, the deadline passes, or waitCtx is
// cancelled (SIGINT/SIGTERM via ctx.newInterruptContext, or a caller-injected
// context in tests). It never re-executes or resubmits the command itself;
// on a terminal "approved" result the caller falls through to the existing
// ctx.runBash call in the same invocation, so the requester never has to
// reconstruct the original command (#823).
func waitForCommandApprovalDecision(waitCtx context.Context, ctx commandContext, p commandApprovalWaitParams) (commandApprovalEvaluation, error) {
	startSessionKey, startGeneration, sessionIdentityKnown := projection.CurrentSessionIdentity(p.sessionDir)
	pollCount := 0
	for {
		// #823 F-004/F-005: cancellation, the deadline, and session identity
		// are checked BEFORE accepting any approval discovered this
		// iteration, so an approval that lands after cancellation, after
		// the deadline has already passed, or after the session/context
		// changed is never honored.
		if interruption := commandApprovalWaitInterruption(waitCtx, ctx, p, startSessionKey, startGeneration, sessionIdentityKnown); interruption != nil {
			return *interruption, nil
		}
		state, ok, err := projection.ProjectCommandApprovalState(p.sessionDir, ctx.now())
		if err != nil {
			return commandApprovalEvaluation{}, err
		}
		if ok {
			if thread, found := state.Threads[p.threadID]; found {
				if thread.CommandHash != "" && thread.CommandHash != p.commandHash {
					return commandApprovalEvaluation{
						Decision: "digest_mismatch",
						Reason:   "approval exists for a different command digest",
						Thread:   &thread,
					}, nil
				}
				// #823 F-002: bind every poll to the FULL original
				// correlation this wait started with — requester, the
				// trusted config-resolved approver address, and the policy
				// identity (label/category/mode) — not just requesterAddress.
				// A thread that matches the threadID and command digest but
				// diverges on any of these belongs to a different
				// request/policy and must never release this waiter, even
				// via an explicit --thread-id reuse.
				// Each check below compares against the ORIGINAL value this
				// wait started with only when that original was known
				// (non-empty); once known, ANY divergence is a mismatch —
				// including the current field going empty. Guarding only on
				// "both sides non-empty" would let a field that regresses to
				// empty silently pass as a non-mismatch and fall through to
				// evaluationForThread, which could then accept an approval
				// that no longer carries the correlation this waiter
				// actually started with.
				if p.requesterAddress != "" && thread.RequesterAddress != p.requesterAddress {
					return commandApprovalEvaluation{
						Decision: "requester_mismatch",
						Reason:   "approval thread belongs to a different requester",
						Thread:   &thread,
					}, nil
				}
				if p.commandApproverAddress != "" && thread.CommandApproverAddress != p.commandApproverAddress {
					return commandApprovalEvaluation{
						Decision: "requester_mismatch",
						Reason:   "approval thread was requested from a different trusted approver",
						Thread:   &thread,
					}, nil
				}
				// Guarded on thread.Mode != "" for the same reason as the
				// immediate-path check: a stale placeholder thread has no
				// recorded policy fields and must reach evaluationForThread's
				// "stale" diagnosis instead of being misclassified here.
				if thread.Mode != "" && (thread.Label != p.policyLabel || thread.Category != p.policyCategory || thread.Mode != p.policyMode) {
					return commandApprovalEvaluation{
						Decision: "requester_mismatch",
						Reason:   "approval thread belongs to a different policy (label/category/mode)",
						Thread:   &thread,
					}, nil
				}
				// #823 F-002: the ORIGINAL input_request_id this wait
				// started with must still match. A retry (whether from this
				// process or another) that somehow changed the thread's
				// InputRequestID mid-wait means the correlation this waiter
				// began with is no longer the one a decision could resolve —
				// treat that as a distinct mismatch rather than silently
				// waiting on, or accepting a decision against, a
				// correlation that moved out from under it. This also
				// catches the field going empty, not only changing to a
				// different value.
				if p.originalInputRequestID != "" && thread.InputRequestID != p.originalInputRequestID {
					return commandApprovalEvaluation{
						Decision: "requester_mismatch",
						Reason:   "approval thread's input_request_id changed since this wait started",
						Thread:   &thread,
					}, nil
				}
				if evaluation := evaluationForThread(thread); evaluation.Decision != "pending" {
					if evaluation.Decision == "approved" {
						// #823 F-005: re-check immediately before accepting
						// the approval too, not only at the top of this
						// iteration — a cancellation, deadline, or session
						// change landing between the top-of-loop check and
						// this projection read must still win.
						if interruption := commandApprovalWaitInterruption(waitCtx, ctx, p, startSessionKey, startGeneration, sessionIdentityKnown); interruption != nil {
							return *interruption, nil
						}
					}
					return evaluation, nil
				}
			}
		}
		pollCount++
		if p.commandApproverNode != "" && pollCount%approverLivenessCheckEveryNPolls == 0 {
			if !commandApproverStillReachable(p.baseDir, p.contextID, p.sessionName, p.commandApproverNode) {
				return commandApprovalEvaluation{
					Decision: "approver_lost",
					Reason:   "the configured command approver is no longer discoverable",
				}, nil
			}
		}
		ctx.sleep(waitCtx, commandApprovalPollInterval)
	}
}

// commandApproverStillReachable reuses the exact discovery seam
// deliverCommandApprovalRequest already relies on (discoverNodesForCommandApprovalDeliveryFn,
// overridden in tests) so mid-wait approver-loss detection exercises the
// same node-resolution path as the original delivery, not a parallel one.
func commandApproverStillReachable(baseDir, contextID, sessionName, commandApproverNode string) bool {
	nodes, _, err := discoverNodesForCommandApprovalDeliveryFn(baseDir, contextID, sessionName)
	if err != nil {
		return false
	}
	resolved := discovery.ResolveNodeName(commandApproverNode, sessionName, nodes)
	_, ok := nodes[resolved]
	return ok
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
		if thread.HistoricalOnly {
			evaluation.Decision = "historical_only"
			evaluation.Reason = "approval is historical audit-only and cannot authorize live execution"
		} else {
			evaluation.Decision = "approved"
			evaluation.Allowed = true
			evaluation.Reason = "approval is approved"
		}
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

// atomicRequestOutcome reports which input_request_id ended up recorded for
// a thread after atomicCreateOrReplaceRequest, and whether THIS call is the
// one that actually created it (as opposed to losing a create/replace race
// and adopting another caller's request).
type atomicRequestOutcome struct {
	inputRequestID string
	created        bool
}

// atomicCreateOrReplaceRequest is the single request-minting primitive for
// the #823 rework-2 request lifecycle (F-002, F-012). It reuses the same
// journal.Writer.AppendCurrentSessionEventIfAbsent fence claimCommandExecution
// relies on for F-001, so two processes racing to mint a request for the
// same thread can never both succeed — exactly one request event lands, and
// every other racer adopts its correlation instead of creating a duplicate.
//
// supersedesInputRequestID distinguishes the two cases this lifecycle needs:
//   - "" (the "absent" case): no request exists for this thread yet, so ANY
//     request event found for it during replay is a race winner to defer to.
//   - non-empty (the terminal-retryable case: rejected/expired/stale/
//     historical_only/wrong_reviewer, #823 F-012): the specific terminal
//     request being replaced. Only a request whose InputRequestID differs
//     from that one counts as "already superseded" — a freshly minted,
//     randomly generated id can never collide with it, so exactly one racer
//     wins the replace and the rest adopt that winner's new request instead
//     of each minting their own.
func atomicCreateOrReplaceRequest(sessionDir, contextID, sessionName, threadID string, policy resolvedCommandApprovalPolicy, commandApproverNode, commandHash, reason, expiresAt, commandText string, storeCommandText bool, supersedesInputRequestID string, now time.Time) (atomicRequestOutcome, error) {
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		writer, err = journal.OpenShadowWriter(sessionDir, contextID, sessionName, os.Getpid(), now)
		if err != nil {
			return atomicRequestOutcome{}, err
		}
	}
	candidateInputRequestID, err := generateInputRequestID()
	if err != nil {
		return atomicRequestOutcome{}, fmt.Errorf("generating command approval input request id: %w", err)
	}
	payload := journal.CommandApprovalRequestPayload{
		Requester:              policy.Requester,
		RequesterAddress:       nodeaddr.Full(policy.Requester, sessionName),
		Reviewer:               policy.Reviewer,
		CommandApproverNode:    commandApproverNode,
		CommandApproverAddress: nodeaddr.Full(commandApproverNode, sessionName),
		Mode:                   policy.Mode,
		Label:                  policy.Label,
		Category:               policy.Category,
		CommandHash:            commandHash,
		InputRequestID:         candidateInputRequestID,
		Reason:                 reason,
		ExpiresAt:              expiresAt,
	}
	if storeCommandText {
		payload.CommandText = commandText
	}
	equivalent := func(event journal.Event) (bool, error) {
		if event.Type != journal.CommandApprovalRequestedEventType || event.ThreadID != threadID {
			return false, nil
		}
		var existing journal.CommandApprovalRequestPayload
		if err := json.Unmarshal(event.Payload, &existing); err != nil {
			return false, err
		}
		if supersedesInputRequestID == "" {
			return true, nil
		}
		return existing.InputRequestID != supersedesInputRequestID, nil
	}
	writtenEvent, appended, err := writer.AppendCurrentSessionEventIfAbsent(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, payload, journal.AppendOptions{ThreadID: threadID}, now, equivalent)
	if err != nil {
		return atomicRequestOutcome{}, err
	}
	if appended {
		return atomicRequestOutcome{inputRequestID: candidateInputRequestID, created: true}, nil
	}
	var existing journal.CommandApprovalRequestPayload
	if err := json.Unmarshal(writtenEvent.Payload, &existing); err != nil {
		return atomicRequestOutcome{}, err
	}
	return atomicRequestOutcome{inputRequestID: existing.InputRequestID, created: false}, nil
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

// claimCommandExecution atomically claims the right to run an approved
// command exactly once (#823 F-001), keyed by (thread, input_request_id,
// command_hash). It reuses journal.Writer.AppendCurrentSessionEventIfAbsent —
// the same cross-process idempotent-append primitive
// recordCommandApprovalAutoFillFn already relies on for exactly-once
// mailbox fills — so two processes racing to execute the same approved
// thread can never both win the claim. Returns claimed=false when an
// equivalent claim already exists (someone else already ran, or is
// running, this exact approval).
func claimCommandExecution(sessionDir, contextID, sessionName, threadID, inputRequestID, commandHash, requester string, now time.Time) (bool, error) {
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		writer, err = journal.OpenShadowWriter(sessionDir, contextID, sessionName, os.Getpid(), now)
		if err != nil {
			return false, err
		}
	}
	payload := journal.CommandExecutionClaimPayload{
		Requester:      requester,
		ApprovalThread: threadID,
		InputRequestID: inputRequestID,
		CommandHash:    commandHash,
	}
	equivalent := func(event journal.Event) (bool, error) {
		if event.Type != journal.CommandExecutionClaimedEventType {
			return false, nil
		}
		var got journal.CommandExecutionClaimPayload
		if err := json.Unmarshal(event.Payload, &got); err != nil {
			return false, err
		}
		return got.ApprovalThread == threadID && got.InputRequestID == inputRequestID && got.CommandHash == commandHash, nil
	}
	_, claimed, err := writer.AppendCurrentSessionEventIfAbsent(journal.CommandExecutionClaimedEventType, journal.VisibilityOperatorVisible, payload, journal.AppendOptions{ThreadID: threadID}, now, equivalent)
	return claimed, err
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
