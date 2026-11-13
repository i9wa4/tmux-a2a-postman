package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/messageid"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

type mailOutput struct {
	trace             *diagnostic.Recorder
	facts             diagnostic.Facts
	RefreshEligible   bool                    `json:"-"`
	Workspace         string                  `json:"workspace"`
	AsOf              string                  `json:"as_of"`
	Success           bool                    `json:"success"`
	Self              workspace.NodeStatus    `json:"self"`
	MarkdownPath      **string                `json:"markdown_absolute_path,omitempty"`
	Instruction       string                  `json:"instruction,omitempty"`
	Notification      *workspace.NoticeResult `json:"notification,omitempty"`
	RecoveryMessageID string                  `json:"recovery_message_id,omitempty"`
	Reason            string                  `json:"reason,omitempty"`
}

func finishMail(c *workspaceCommand, out *mailOutput, operationErr error) (retErr error) {
	trace := out.trace
	defer func() { trace.End(diagnostic.ErrorOutcome(retErr), out.facts); diagnosticWarning(os.Stderr, trace) }()
	out.Success = operationErr == nil
	out.AsOf = time.Now().UTC().Format(time.RFC3339Nano)
	if c != nil {
		if out.RefreshEligible {
			result := c.refreshStatusAfterOperation()
			out.facts.Capture = result.Status
		}

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
	} else if operationErr != nil {
		out.Self.Reason = operationErr.Error()
	}
	if operationErr != nil {
		out.Reason = operationErr.Error()
	}
	if out.Success && out.MarkdownPath != nil && *out.MarkdownPath != nil && out.Self.UnreadCount != nil && *out.Self.UnreadCount > 0 {
		out.Instruction += fmt.Sprintf(" Remaining unread messages after this POP: %d (self.unread_count). After fully reading and handling this archive, run `tmux-a2a-postman pop` again without waiting for another notification. Continue only while the latest operation reports a positive self.unread_count; stop on zero, unknown, or an empty POP. Do not poll an empty inbox.", *out.Self.UnreadCount)
	}
	outputSpan := trace.Start("result_output")
	writeErr := json.NewEncoder(os.Stdout).Encode(out)
	outputSpan.Error(writeErr)
	if err := writeErr; err != nil {
		return err
	}
	if operationErr != nil {
		return reportedError{operationErr}
	}
	return nil
}

func RunWorkspacePop(args []string) (retErr error) {
	trace := invocationDiagnostic("pop")
	var c *workspaceCommand
	var path *string
	out := mailOutput{MarkdownPath: &path, trace: trace, facts: diagnostic.Facts{Mail: "not_attempted", Capture: "not_attempted", Notification: "not_attempted"}}
	defer func() { retErr = finishMail(c, &out, retErr) }()
	f := newWorkspaceFlags("pop")
	configPath, err := f.source(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("pop takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(diagnostic.With(context.Background(), trace), 10*time.Second)
	defer cancel()
	opened, err := openWorkspaceCommand(ctx, configPath)
	if err != nil {
		return err
	}
	c = &opened
	c.attachDiagnostic(ctx, trace)
	out.RefreshEligible = true
	mailSpan := trace.Start("mail_operation")
	var claim *workspace.Claimed
	err = retryWorkspaceAdmission(ctx, func() (bool, error) {
		var err error
		claim, err = c.mail.Claim(ctx, c.boundRuntime(), "")
		return claim == nil, err
	})
	mailSpan.Error(err)
	out.facts.Mail = "empty"
	if err != nil {
		out.facts.Mail = "unknown"
	}
	if claim != nil {
		out.facts.Mail = "claimed"
		noteMessage(trace, claim.Message, c.caller.Caller.Role)
		path = &claim.Path
		out.Instruction = "Read the entire Markdown file at markdown_absolute_path through EOF before processing, replying, or deciding no action is needed. Do not substitute a truncated view, summary, notification, metadata, or reply guidance for the full file."
	}
	return err
}

func readHeredoc(stdin io.Reader) (string, error) {
	if defaultStdinIsTerminal(stdin) {
		return "", sendHeredocBodySourceError()
	}
	body, err := io.ReadAll(io.LimitReader(stdin, 60*1024+1))
	if err != nil {
		return "", err
	}
	if len(body) == 0 || len(body) > 60*1024 {
		return "", errors.New("quoted heredoc body must contain 1 to 61440 bytes")
	}
	if !utf8.Valid(body) {
		return "", errors.New("heredoc body must be valid UTF-8")
	}
	return string(body), nil
}

func replyTarget(value string) ([]string, bool, error) {
	if !strings.HasPrefix(value, "m-") && !strings.Contains(value, ",") {
		return nil, false, nil
	}
	ids := strings.Split(value, ",")
	if len(ids) > workspace.MaxCombinedReplies {
		return nil, true, errors.New("at most 16 reply IDs are supported")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !messageid.Valid(id) || seen[id] {
			return nil, true, errors.New("invalid or duplicate reply ID")
		}
		seen[id] = true
	}
	return ids, true, nil
}

func RunWorkspaceSendHeredoc(args []string) (retErr error) {
	trace := invocationDiagnostic("send-heredoc")
	var c *workspaceCommand
	out := mailOutput{trace: trace, facts: diagnostic.Facts{Mail: "not_attempted", Capture: "not_attempted", Notification: "not_attempted"}}
	defer func() { retErr = finishMail(c, &out, retErr) }()
	f := newWorkspaceFlags("send-heredoc")
	to := f.fs.String("to", "", "recipient node, interface route, or claimed message IDs")
	configPath, err := f.source(args)
	if err != nil {
		return err
	}
	if *to == "" || f.fs.NArg() != 0 {
		return errors.New("send-heredoc requires --to and no positional arguments")
	}
	ids, isReply, err := replyTarget(*to)
	if err != nil {
		return err
	}
	body, err := readHeredoc(os.Stdin)
	trace.Add("body_bytes", int64(len(body)), 0)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(diagnostic.With(context.Background(), trace), 10*time.Second)
	defer cancel()
	opened, err := openWorkspaceCommand(ctx, configPath)
	if err != nil {
		return err
	}
	c = &opened
	c.attachDiagnostic(ctx, trace)
	if isReply {
		sources, err := c.mail.ReplySources(ctx, c.boundRuntime(), ids)
		if err != nil {
			return err
		}
		first := sources[0]
		*to = first.From
		if first.FromSession != "" {
			*to = first.FromSession + ":" + first.From
		}
	}
	recipient := *to
	if _, role, cross := strings.Cut(*to, ":"); cross {
		recipient = role
	}
	if err := c.prepareRecipientContext(ctx, recipient, strings.Contains(*to, ":")); err != nil {
		return err
	}
	mailSpan := trace.Start("mail_operation")
	defer func() { mailSpan.Error(retErr) }()
	var msg workspace.Message
	if strings.Contains(*to, ":") {
		caller, err := c.namedCaller(ctx)
		if err != nil {
			return err
		}
		snapshot, err := caller.Snapshot(ctx)
		if err != nil {
			return err
		}
		var session, role string
		var peer workspace.NamedRuntime
		if len(c.topology.Sessions) != 0 {
			session, role, err = c.topology.InterfaceRoute(caller.Name(), snapshot.Caller.Role, *to)
			if err != nil {
				return err
			}
			out.RefreshEligible = true
			peer, err = c.runtime.FindNamedPeer(ctx, session, role)
		} else {
			if strings.Count(*to, ":") != 1 || snapshot.Caller.Role != c.topology.InterfaceNode {
				return errors.New("recipient is not a directory interface")
			}
			session, role, _ = strings.Cut(*to, ":")
			if role != c.topology.InterfaceNode {
				return errors.New("recipient is not a directory interface")
			}
			interfaces, routeErr := c.runtime.DirectoryInterfaces(ctx, role)
			if routeErr != nil {
				return routeErr
			}
			selected, routeErr := workspace.DirectDirectoryPeer(interfaces, caller.Name(), session)
			if routeErr != nil {
				return routeErr
			}
			peer = selected.Runtime.WithDirectoryRoute()
			out.RefreshEligible = true
		}
		if err != nil {
			recoveryCtx, recoveryCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer recoveryCancel()
			candidate, stageErr := c.mail.StageUnavailableCross(recoveryCtx, caller, session, role, ids, body, err)
			out.RecoveryMessageID = candidate.ID
			return stageErr
		}
		err = retryWorkspaceAdmission(ctx, func() (bool, error) {
			var err error
			if isReply {
				msg, err = c.mail.SendCrossReply(ctx, caller, peer, ids, body)
			} else {
				msg, err = c.mail.SendCrossWithReply(ctx, caller, peer, body, "", "", true)
			}
			return msg.ID == "", err
		})
		if err == nil {
			notice := c.mail.Store.NotifyCommittedAfterCommit(ctx, caller, peer, msg)
			out.facts.Notification = notice.Status
			if notice.Status != "accepted" {
				out.Notification = &notice
			}
		}
		retErr = err
	} else {
		if !c.permits(*to) {
			return errors.New("recipient is not a configured outgoing edge")
		}
		out.RefreshEligible = true
		err = retryWorkspaceAdmission(ctx, func() (bool, error) {
			var err error
			if isReply {
				msg, err = c.mail.SendReply(ctx, c.boundRuntime(), ids, body)
			} else {
				msg, err = c.mail.Send(ctx, c.boundRuntime(), *to, body, "", true)
			}
			return msg.ID == "", err
		})
		if err == nil {
			notice := c.mail.Store.NotifyCommittedAfterCommit(ctx, c.boundRuntime(), c.runtime, msg)
			out.facts.Notification = notice.Status
			if notice.Status != "accepted" {
				out.Notification = &notice
			}
		}
		retErr = err
	}
	noteMessage(trace, msg, c.caller.Caller.Role)
	if retErr == nil {
		out.facts.Mail = "committed"
	} else {
		out.facts.Mail = "unknown"
	}
	if retErr != nil && msg.ID != "" {
		out.RecoveryMessageID = msg.ID
	}
	return retErr
}
