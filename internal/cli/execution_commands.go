package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

type approvalOutput struct {
	Approver string  `json:"approver"`
	Decision *string `json:"decision"`
	Reason   *string `json:"reason"`
}

type executionOutput struct {
	Workspace           string                  `json:"workspace"`
	AsOf                string                  `json:"as_of"`
	Success             bool                    `json:"success"`
	Executed            *bool                   `json:"executed"`
	ExitCode            *int                    `json:"exit_code"`
	Approval            *approvalOutput         `json:"approval"`
	Self                workspace.NodeStatus    `json:"self"`
	Reason              string                  `json:"reason,omitempty"`
	Notification        *workspace.NoticeResult `json:"notification,omitempty"`
	RecoveryMessageID   string                  `json:"recovery_message_id,omitempty"`
	RecoveryOperationID string                  `json:"recovery_operation_id,omitempty"`
}

type lineBoundaryWriter struct {
	writer io.Writer
	last   byte
	wrote  bool
}

func (w *lineBoundaryWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.last = p[n-1]
		w.wrote = true
	}
	return n, err
}

func RunWorkspaceExecuteHeredoc(args []string) (retErr error) {
	trace := invocationDiagnostic("execute-heredoc")
	facts := diagnostic.Facts{Execution: "not_run", Notification: "not_attempted", Approval: "not_attempted"}
	no := false
	out := executionOutput{Executed: &no}
	var c *workspaceCommand
	boundary := &lineBoundaryWriter{writer: os.Stderr}
	defer func() {
		out.Success = retErr == nil
		out.AsOf = time.Now().UTC().Format(time.RFC3339Nano)
		if c != nil {
			result := c.refreshStatusAfterOperation()
			facts.Capture = result.Status
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			summarySpan := trace.Start("self_summary")
			out.Workspace, out.AsOf, out.Self = c.mail.Summary(diagnostic.With(ctx, trace), c.boundRuntime(), c.caller.Caller.Role)
			summaryOutcome := "ok"
			if out.Self.Reason != "" {
				summaryOutcome = "unknown"
			}
			summarySpan.End(summaryOutcome)
			if out.Workspace == "" {
				out.Workspace = c.caller.Root
			}
		}
		if retErr != nil && out.Reason == "" {
			out.Reason = retErr.Error()
		}
		if boundary.wrote && boundary.last != '\n' {
			_, _ = os.Stderr.Write([]byte{'\n'})
		}
		encodingSpan := trace.Start("result_encoding")
		encoded, encodingErr := json.Marshal(out)
		encodingSpan.Error(encodingErr)
		terminalErr := retErr
		if encodingErr != nil {
			terminalErr = encodingErr
		}
		// End before final JSON: bounded warnings must precede that last line.
		trace.End(diagnostic.ErrorOutcome(terminalErr), facts)
		diagnosticWarning(boundary, trace)
		if encodingErr != nil {
			retErr = encodingErr
		} else {
			encoded = append(encoded, '\n')
			if _, err := os.Stderr.Write(encoded); err != nil {
				retErr = err
			}
		}
		if retErr != nil {
			retErr = reportedError{retErr}
		}
	}()
	f := newWorkspaceFlags("execute-heredoc")
	reason := f.fs.String("reason", "", "why this command should run")
	path, err := f.source(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 || strings.TrimSpace(*reason) == "" || len(*reason) > 4096 {
		return errors.New("execute-heredoc requires --reason (1 to 4096 bytes) and a quoted heredoc; no positional arguments")
	}
	source, err := readHeredoc(os.Stdin)
	trace.Add("body_bytes", int64(len(source)), 0)
	if err != nil {
		return err
	}
	if strings.TrimSpace(source) == "" || strings.ContainsRune(source, 0) {
		return errors.New("execution source must contain a command and cannot contain NUL")
	}
	ctx, cancel := defaultInterruptContext()
	ctx = diagnostic.With(ctx, trace)
	defer cancel()
	opened, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	c = &opened
	c.attachDiagnostic(ctx, trace)
	if err := c.prepareRecipientContext(ctx); err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	shell, err := c.runtime.ResolveShell(ctx)
	if err != nil {
		return err
	}
	invocation := workspace.ShellInvocation{Source: source, Reason: *reason, CWD: cwd, Shell: shell}
	executor := workspace.Executor{Mail: c.mail, Resolve: workspace.SourceApprovalResolver(path), Stdout: os.Stdout, Stderr: boundary, Invocation: &invocation, Notifier: c.runtime}
	executionSpan := trace.Start("execution")
	result, err := executor.Execute(ctx, c.boundRuntime(), source)
	executionSpan.Error(err)
	facts = diagnostic.Facts{RequestID: result.RequestID, ReplyID: result.ReplyID, OperationID: result.OperationID, InputRequestID: result.InputRequestID, Counterparty: result.Reviewer, Executed: result.Executed, ChildExitCode: result.ExitStatus, Execution: result.Status, Approval: result.ApprovalDecision, Notification: "not_attempted"}
	if result.Reviewer == "" && result.OperationID != "" {
		facts.Approval = "advisory"
	}
	if result.Reviewer != "" && result.ApprovalDecision == "" {
		facts.Approval = "pending"
	}
	if result.Notice != nil {
		facts.Notification = result.Notice.Status
	}
	out.Executed, out.ExitCode, out.Reason = result.Executed, result.ExitStatus, result.Reason
	if result.Executed == nil {
		out.RecoveryOperationID = result.OperationID
	}
	if result.Notice != nil && result.Notice.Status != "accepted" {
		out.Notification = result.Notice
	}
	if result.Reviewer != "" {
		out.Approval = &approvalOutput{Approver: result.Reviewer}
		if result.ApprovalDecision != "" {
			out.Approval.Decision = &result.ApprovalDecision
			out.Approval.Reason = &result.ApprovalReason
		}
	}
	if result.Status == "delivery_failed" {
		out.RecoveryMessageID = result.RequestID
	}
	if err != nil {
		return err
	}
	if result.ExitStatus != nil && *result.ExitStatus != 0 {
		return commandExitError{code: *result.ExitStatus}
	}
	if result.Executed == nil || !*result.Executed || result.Status != "executed" {
		return errors.New(result.Status + ": " + result.Reason)
	}
	return nil
}

func RunWorkspaceApproveExecution(args []string) error { return runExecutionDecision(args, true) }
func RunWorkspaceDeclineExecution(args []string) error { return runExecutionDecision(args, false) }

func runExecutionDecision(args []string, approve bool) (retErr error) {
	name := "decline-execution"
	if approve {
		name = "approve-execution"
	}
	trace := invocationDiagnostic(name)
	var c *workspaceCommand
	out := mailOutput{trace: trace, facts: diagnostic.Facts{Mail: "not_attempted", Notification: "not_attempted", Capture: "not_attempted"}}
	defer func() { retErr = finishMail(c, &out, retErr) }()
	f := newWorkspaceFlags(name)
	to := f.fs.String("to", "", "own POP'd execution request ID")
	reason := f.fs.String("reason", "", "reason for this decision")
	path, err := f.source(args)
	if err != nil {
		return err
	}
	ids, isReply, err := replyTarget(*to)
	if err != nil || !isReply || len(ids) != 1 || f.fs.NArg() != 0 || strings.TrimSpace(*reason) == "" {
		return errors.New(name + " requires --to <one POP'd execution request ID> and --reason")
	}
	ctx, cancel := context.WithTimeout(diagnostic.With(context.Background(), trace), 10*time.Second)
	defer cancel()
	if !defaultStdinIsTerminal(os.Stdin) {
		data, err := io.ReadAll(io.LimitReader(os.Stdin, 1))
		if err != nil {
			return err
		}
		if len(data) > 0 {
			return errors.New(name + " does not accept stdin; use --reason")
		}
	}
	opened, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	c = &opened
	c.attachDiagnostic(ctx, trace)
	if c.topology.Reviewer != c.caller.Caller.Role {
		return errors.New("caller is not the configured command approver")
	}
	sources, err := c.mail.ReplySources(ctx, c.boundRuntime(), ids)
	if err != nil {
		return err
	}
	if !c.permits(sources[0].From) {
		return errors.New("requester is not a configured outgoing edge")
	}
	if err := c.prepareRecipientContext(ctx); err != nil {
		return err
	}
	var msg workspace.Message
	err = retryWorkspaceAdmission(ctx, func() (bool, error) {
		var err error
		msg, err = c.mail.DecideExecution(ctx, c.boundRuntime(), ids[0], *reason, approve)
		return msg.ID == "", err
	})
	if err != nil {
		out.RecoveryMessageID = msg.ID
		return err
	}
	noteMessage(trace, msg, c.caller.Caller.Role)
	out.facts.Mail = "committed"
	if approve {
		out.facts.Approval = "approved"
	} else {
		out.facts.Approval = "rejected"
	}
	out.RefreshEligible = true
	if !c.mail.WaitingExecutionReply(ctx, c.boundRuntime(), msg) {
		notice := c.mail.Store.NotifyCommittedAfterCommit(ctx, c.boundRuntime(), c.runtime, msg)
		out.facts.Notification = notice.Status
		if notice.Status != "accepted" {
			out.Notification = &notice
		}
	}
	return nil
}

type reportedError struct{ error }

func (e reportedError) Reported() bool { return true }
func (e reportedError) ExitCode() int {
	var code interface{ ExitCode() int }
	if errors.As(e.error, &code) {
		return code.ExitCode()
	}
	return 1
}
