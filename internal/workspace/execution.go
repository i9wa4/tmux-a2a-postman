package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
)

// ApprovalResolver must distinguish positively verified absence ("", nil)
// from unknown/error. It must be bounded, read only and never capture panes.
type ApprovalResolver func(context.Context, Snapshot) (string, error)

// Several approved invocations can contend with receiver POPs and session
// snapshot persistence. Admission waits outside the workspace lock, and remains
// bounded by both this budget and the original approval/caller deadline.
const executionAuditAdmissionBudget = 10 * time.Second

type ApprovalRequest struct {
	Version     int    `json:"version"`
	Command     string `json:"command"`
	OperationID string `json:"operation_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
	CWD         string `json:"cwd,omitempty"`
	Shell       string `json:"shell,omitempty"`
}
type ApprovalDecision struct {
	Version     int    `json:"version"`
	Command     string `json:"command"`
	Decision    string `json:"decision"`
	OperationID string `json:"operation_id,omitempty"`
	Reason      string `json:"reason,omitempty"`
}
type ExecutionResult struct {
	Invocation       *ShellInvocation `json:"invocation,omitempty"`
	Executed         *bool            `json:"executed"`
	ApprovalDecision string           `json:"approval_decision,omitempty"`
	ApprovalReason   string           `json:"approval_reason,omitempty"`
	Version          int              `json:"version"`
	OperationID      string           `json:"operation_id"`
	RequestID        string           `json:"request_id,omitempty"`
	InputRequestID   string           `json:"input_request_id,omitempty"`
	ReplyID          string           `json:"reply_id,omitempty"`
	Command          string           `json:"command,omitempty"`
	Requester        string           `json:"requester"`
	Reviewer         string           `json:"reviewer,omitempty"`
	Notice           *NoticeResult    `json:"notice,omitempty"`
	Status           string           `json:"status"`
	ExitStatus       *int             `json:"exit_status"`
	AsOf             string           `json:"as_of"`
	Reason           string           `json:"reason,omitempty"`
}

// Executor has no resume/thread/grant API. Each call is independent.
// Requests and decisions carry the exact immutable command, without a digest.
// Its result files are audit records, never authorizations for another caller.
type Executor struct {
	Invocation *ShellInvocation
	Notifier   Runtime
	Mail       Mail
	Resolve    ApprovalResolver
	Stdout     io.Writer
	Stderr     io.Writer
	now        func() time.Time
	wait       func(context.Context) error
	run        func(context.Context, string, io.Writer, io.Writer) (int, error)
}
type executionRuntime struct {
	Runtime
	initial  Snapshot
	reviewer Binding
	resolve  ApprovalResolver
	role     string
}

func (r executionRuntime) Snapshot(ctx context.Context) (Snapshot, error) {
	s, err := r.Runtime.Snapshot(ctx)
	if err != nil {
		return s, err
	}
	if _, err = validateSnapshot(s); err != nil || s.Root != r.initial.Root || s.Caller != r.initial.Caller {
		return s, ErrUnknown
	}
	role, err := r.resolve(ctx, s)
	if err != nil || role != r.role {
		return s, ErrUnknown
	}
	if role != "" {
		found := false
		for _, b := range s.Bindings {
			if b == r.reviewer {
				found = true
			}
		}
		if !found {
			return s, ErrUnknown
		}
	}
	return s, nil
}

func (e Executor) clock() time.Time {
	if e.now != nil {
		return e.now()
	}
	return time.Now()
}

func (e Executor) pause(ctx context.Context) error {
	if e.wait != nil {
		return e.wait(ctx)
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func runExecution(ctx context.Context, command string, stdout, stderr io.Writer) (int, error) {
	cmd := exec.CommandContext(ctx, "bash", "-lc", command)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// A separate process group lets cancellation kill Bash and ordinary children
	// together. Descendants that deliberately leave the group are not contained.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// Bound pipe draining if an escaped descendant retains an inherited pipe.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		code := 127
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		return code, ctx.Err()
	}
	if err == nil {
		return 0, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	return 127, err
}

func decodeDecision(body string) (ApprovalDecision, error) {
	var d ApprovalDecision
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF || (d.Version != 1 && d.Version != 2) || (d.Decision != "approved" && d.Decision != "rejected") {
		return d, ErrUnknown
	}
	return d, nil
}

func executionBox(ws *os.Root, role string) (*os.Root, error) {
	// Initialize only the current role's standard directories.
	box, err := mailBox(ws, role, "sent")
	if err != nil {
		return nil, err
	}
	_ = box.Close()
	agents, err := privateDir(ws, "agents", false)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(agents.Close)
	agent, err := privateDir(agents, role, false)
	if err != nil {
		return nil, err
	}
	defer func(closeFn func() error) { _ = closeFn() }(agent.Close)
	records, err := privateDir(agent, "executions", true)
	if err != nil {
		return nil, err
	}
	if err = syncDirectory(agent); err != nil {
		_ = records.Close()
		return nil, err
	}
	return records, nil
}

func (e Executor) save(ctx context.Context, rt Runtime, result ExecutionResult, create bool) error {
	return e.Mail.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		box, err := executionBox(ws, s.Caller.Role)
		if err != nil {
			return err
		}
		defer func(closeFn func() error) { _ = closeFn() }(box.Close)
		if result.Requester != s.Caller.Role || !messageID(result.OperationID) {
			return ErrUnknown
		}
		raw, err := json.Marshal(result)
		if err != nil || len(raw) > 60*1024 {
			return errors.New("execution audit record exceeds bound")
		}
		if create {
			if err = writeJSON(box, result.OperationID+".json", result); err != nil {
				return err
			}
			return syncDirectory(box)
		}
		var prior ExecutionResult
		if readJSON(box, result.OperationID+".json", &prior) != nil || prior.Status != "dispatched" ||
			prior.Command != result.Command || prior.RequestID != result.RequestID ||
			prior.InputRequestID != result.InputRequestID || prior.ReplyID != result.ReplyID ||
			prior.Requester != result.Requester || prior.Reviewer != result.Reviewer || !sameInvocation(prior.Invocation, result.Invocation) {
			return ErrUnknown
		}
		return replaceJSON(box, result.OperationID+".json", result)
	})
}

// Terminal audit uses a fresh bounded context after cancellation. It still
// requires fresh pinned ownership/configuration evidence and never dispatches.
func (e Executor) saveTerminal(ctx context.Context, rt Runtime, result ExecutionResult, create bool) error {
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	return e.save(auditCtx, rt, result, create)
}

// claimDecision scans only this caller's inbox, and archives only an exact,
// unambiguous reply from the pinned reviewer. Unrelated/wrong replies stay unread.
func (e Executor) claimDecision(ctx context.Context, rt Runtime, req Message, command string) (*Message, string, error) {
	var answer *Message
	decision := ""
	err := e.Mail.Store.WithVerified(ctx, rt, func(ws *os.Root, s Snapshot) error {
		if err := e.Mail.recover(ws); err != nil {
			return err
		}
		inbox, err := mailBox(ws, s.Caller.Role, "inbox")
		if err != nil {
			return err
		}
		defer func(closeFn func() error) { _ = closeFn() }(inbox.Close)
		entries, err := boundedEntries(inbox)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			id := strings.TrimSuffix(entry.Name(), ".md")
			if !messageID(id) || entry.Name() != id+".md" {
				return ErrUnknown
			}
			msg, err := readMessage(inbox, entry.Name())
			if err != nil {
				return err
			}
			if msg.ID != id || msg.To != s.Caller.Role {
				return ErrUnknown
			}
			if !exactAnswer(req, msg) {
				continue
			}
			d, err := decodeDecision(msg.Body)
			if err != nil || d.Command != command {
				continue
			}
			if e.Invocation != nil {
				var request ApprovalRequest
				if json.Unmarshal([]byte(req.Body), &request) != nil || req.Kind != "execution-request" || msg.Kind != "execution-decision" || d.Version != 2 || d.OperationID != request.OperationID || strings.TrimSpace(d.Reason) == "" {
					continue
				}
			}
			if answer != nil {
				return errors.New("ambiguous approval replies")
			}
			copy := msg
			answer = &copy
			decision = d.Decision
		}
		if answer == nil {
			return nil
		}
		return e.Mail.begin(ws, mailTransaction{Version: 1, Kind: "claim", Message: *answer, Body: answer.Body, ClaimedAt: e.clock().UTC().Format(time.RFC3339Nano)})
	})
	return answer, decision, err
}

// Execute waits at most 300 seconds for this invocation's exact reply. Only
// verified absence is advisory. Unknown configuration/runtime never runs Bash.
// Waiting and Bash always happen outside the workspace mutation lock.
func (e Executor) Execute(ctx context.Context, rt Runtime, command string) (ExecutionResult, error) {
	trace := diagnostic.From(ctx)
	result := ExecutionResult{Version: 1, Command: command, Status: "unavailable", AsOf: e.clock().UTC().Format(time.RFC3339Nano)}
	no := false
	result.Executed = &no
	result.Invocation = e.Invocation
	if e.Invocation != nil {
		result.Version = 2
		result.Command = ""
		if e.Invocation.Source != command || strings.TrimSpace(e.Invocation.Reason) == "" {
			return result, errors.New("execution source/reason does not match invocation")
		}
	}
	var audit Runtime
	dispatched := false
	fail := func(status string, err error) (ExecutionResult, error) {
		result.Status = status
		result.AsOf = e.clock().UTC().Format(time.RFC3339Nano)
		if err != nil {
			result.Reason = err.Error()
		}
		if audit != nil {
			if saveErr := e.saveTerminal(ctx, audit, result, !dispatched); saveErr != nil {
				result.Reason = strings.TrimSpace(result.Reason + " audit terminal unavailable: " + saveErr.Error())
			}
		}
		return result, err
	}
	if rt == nil || e.Resolve == nil || strings.TrimSpace(command) == "" || strings.ContainsRune(command, 0) {
		return fail("unavailable", ErrUnknown)
	}
	initial, err := rt.Snapshot(ctx)
	if err != nil {
		return fail("unavailable", err)
	}
	if _, err = validateSnapshot(initial); err != nil {
		return fail("unavailable", err)
	}
	role, err := e.Resolve(ctx, initial)
	if err != nil {
		return fail("unavailable", err)
	}
	pinned := executionRuntime{Runtime: rt, initial: initial, resolve: e.Resolve, role: role}
	if role != "" {
		for _, b := range initial.Bindings {
			if b.Role == role && role != initial.Caller.Role {
				pinned.reviewer = b
			}
		}
		if pinned.reviewer.Role == "" {
			return fail("unavailable", ErrUnknown)
		}
	}
	result.Requester, result.Reviewer = initial.Caller.Role, role
	// Bound the entire stored request/audit, including JSON escaping.
	raw, _ := json.Marshal(result)
	if len(raw) > 48*1024 {
		return fail("unavailable", errors.New("command exceeds audit bound"))
	}
	nonce, err := randomName()
	if err != nil {
		return fail("unavailable", err)
	}
	result.OperationID = "m-" + strings.TrimPrefix(nonce, ".new-")
	trace.Note(diagnostic.Facts{OperationID: result.OperationID, Counterparty: role})
	audit = pinned
	dispatchCtx := ctx
	var deadline time.Time
	if role != "" {
		deadline = e.clock().Add(300 * time.Second)
		waiting, cancel := context.WithDeadline(ctx, deadline)
		defer cancel()
		dispatchCtx = waiting
		request := ApprovalRequest{Version: 1, Command: command}
		kind := ""
		if e.Invocation != nil {
			request = ApprovalRequest{Version: 2, Command: command, OperationID: result.OperationID, Reason: e.Invocation.Reason, CWD: e.Invocation.CWD, Shell: e.Invocation.Shell}
			kind = "execution-request"
		}
		requestBody, _ := json.Marshal(request)
		var req Message
		err = retryNoticeAdmission(waiting, func() error {
			var sendErr error
			req, sendErr = e.Mail.sendWithKind(waiting, pinned, role, string(requestBody), "", "", true, kind)
			if req.ID != "" && errors.Is(sendErr, ErrBusy) {
				return errors.New("approval request partially recorded: " + sendErr.Error())
			}
			return sendErr
		})
		result.RequestID, result.InputRequestID = req.ID, req.InputRequestID
		trace.Note(diagnostic.Facts{OperationID: result.OperationID, RequestID: req.ID, InputRequestID: req.InputRequestID, Counterparty: role, Approval: "pending"})
		if err != nil {
			return fail("delivery_failed", err)
		}
		if e.Invocation != nil {
			waiter, err := e.Mail.executionWaiter(waiting, pinned, req)
			if err != nil {
				return fail("unavailable", err)
			}
			defer func() { _ = waiter.Close() }()
			if e.Stderr != nil {
				_, _ = io.WriteString(e.Stderr, "Waiting for execution approval (up to 300 seconds).\n")
			}
		}
		// The request is already durable. A hint failure cannot undo it or
		// authorize a second approval request for this invocation.
		notifier := rt
		if e.Notifier != nil {
			notifier = e.Notifier
		}
		notice := e.Mail.Store.NotifyCommittedAfterCommit(waiting, rt, notifier, req)
		result.Notice = &notice
		approvalSpan := trace.Start("approval_wait")
		defer func() { approvalSpan.End(result.Status) }()
		for {
			if err = waiting.Err(); err != nil || !e.clock().Before(deadline) {
				if ctx.Err() != nil {
					return fail("cancelled", ctx.Err())
				}
				return fail("wait_timeout", context.DeadlineExceeded)
			}
			trace.Add("approval_observations", 1, 0)
			answer, decision, err := e.claimDecision(waiting, pinned, req, command)
			if errors.Is(err, ErrBusy) {
				answer = nil
			} else if err != nil {
				return fail("unavailable", err)
			}
			if answer != nil {
				result.ReplyID = answer.ID
				approvalSpan.End(decision)
				result.ApprovalDecision = decision
				parsed, _ := decodeDecision(answer.Body)
				result.ApprovalReason = parsed.Reason
				if decision == "rejected" {
					return fail("rejected", errors.New("approval rejected"))
				}
				break
			}
			if err = e.pause(waiting); err != nil {
				if ctx.Err() != nil {
					return fail("cancelled", ctx.Err())
				}
				return fail("wait_timeout", err)
			}
		}
	}
	if err = ctx.Err(); err != nil {
		return fail("cancelled", err)
	}
	result.Status = "dispatched"
	// If the process dies after this durable boundary, execution may have
	// started. The audit must not promise executed=false in that interval.
	result.Executed = nil
	result.AsOf = e.clock().UTC().Format(time.RFC3339Nano)
	// Audit is durable BEFORE execution. No recovery path replays this dispatch.
	// A missing reviewer leaves dispatchCtx without a deadline, so bound lock
	// acquisition separately. The reviewer deadline remains the tighter limit.
	auditCtx, auditCancel := context.WithTimeout(dispatchCtx, executionAuditAdmissionBudget)
	for {
		err = e.save(auditCtx, pinned, result, true)
		if !errors.Is(err, ErrBusy) {
			break
		}
		// ErrBusy is returned before the audit mutation lock is acquired,
		// so this attempt could not have written a partial dispatch record.
		if err = e.pause(auditCtx); err != nil {
			break
		}
	}
	auditCancel()
	if err != nil {
		// Do not retry a failed dispatch audit write; a partial record may exist.
		audit = nil
		result.Executed = &no
		return fail("unavailable", fmt.Errorf("execution dispatch audit: %w", err))
	}
	dispatched = true
	if err = ctx.Err(); err != nil {
		result.Executed = &no
		return fail("cancelled", err)
	}
	if role != "" && (!e.clock().Before(deadline) || dispatchCtx.Err() != nil) {
		result.Executed = &no
		return fail("wait_timeout", context.DeadlineExceeded)
	}
	stdout, stderr := e.Stdout, e.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	run := e.run
	if run == nil {
		run = runExecution
	}
	var code int
	var runErr error
	started := true
	if e.Invocation != nil && e.run == nil {
		code, started, runErr = runInteractive(ctx, *e.Invocation, stdout, stderr)
	} else {
		code, runErr = run(ctx, command, stdout, stderr)
	}
	result.Executed = &started
	if started && runErr != nil {
		result.Executed = nil
	}
	result.ExitStatus = &code
	if runErr != nil {
		result.ExitStatus = nil
	}
	result.Status = "executed"
	result.AsOf = e.clock().UTC().Format(time.RFC3339Nano)
	switch {
	case ctx.Err() != nil:
		result.Status = "cancelled"
	case runErr != nil:
		result.Status = "execution_failed"
	case code != 0:
		result.Status = "exited"
	}
	if runErr != nil {
		result.Reason = runErr.Error()
	}
	// Audit failure cannot turn an executed command into a retryable approval.
	if saveErr := e.saveTerminal(ctx, pinned, result, false); saveErr != nil {
		result.Reason = strings.TrimSpace(result.Reason + " audit completion unavailable: " + saveErr.Error())
	}
	return result, runErr
}

func sameInvocation(a, b *ShellInvocation) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
