package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

type mailOutput struct {
	trace             *diagnostic.Recorder
	facts             diagnostic.Facts
	CaptureEligible   bool                    `json:"-"`
	Workspace         string                  `json:"workspace"`
	AsOf              string                  `json:"as_of"`
	Success           bool                    `json:"success"`
	MarkdownPath      **string                `json:"markdown_absolute_path,omitempty"`
	Instruction       string                  `json:"instruction,omitempty"`
	Self              workspace.NodeStatus    `json:"self"`
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
		if out.CaptureEligible {
			captureSpan := trace.Start("session_capture")
			snapshot := c.mail.CaptureSession(diagnostic.With(context.Background(), trace), c.runtime, c.caller.Caller)
			captureResult := "partial"
			if snapshot.Complete {
				captureResult = "complete"
			}
			out.facts.Capture = captureResult
			trace.Note(diagnostic.Facts{Capture: captureResult, ObservationID: snapshot.ID})
			trace.Add("panes_discovered", int64(snapshot.Discovered), 0)
			trace.Add("panes_captured", int64(snapshot.Captured), 0)
			trace.Add("panes_skipped", int64(snapshot.Skipped), 0)
			failed := 0
			for _, pane := range snapshot.Panes {
				if pane.Reason != "" {
					failed++
				}
			}
			trace.Add("panes_failed", int64(failed), 0)
			captureSpan.End(captureResult)
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
	configPath, err := f.parse(args)
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
	out.CaptureEligible = true
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
		if len(id) != 34 || !strings.HasPrefix(id, "m-") || seen[id] {
			return nil, true, errors.New("invalid or duplicate reply ID")
		}
		for _, ch := range id[2:] {
			if !strings.ContainsRune("0123456789abcdef", ch) {
				return nil, true, errors.New("invalid reply ID")
			}
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
	configPath, err := f.parse(args)
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
	if err := c.prepareRecipientContext(ctx); err != nil {
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
		session, role, err := c.topology.InterfaceRoute(caller.Name(), snapshot.Caller.Role, *to)
		if err != nil {
			return err
		}
		out.CaptureEligible = true
		peer, err := c.runtime.FindNamedPeer(ctx, session, role)
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
		out.CaptureEligible = true
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

// Normal nodes omit parent/children entirely. An interface reports exactly its
// own configured parent and children, retaining unavailable peers as unknown.
func RunWorkspaceGetStatus(args []string) error {
	f := newWorkspaceFlags("get-status")
	path, err := f.parse(args)
	if err == nil && f.fs.NArg() != 0 {
		err = errors.New("get-status takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var c workspaceCommand
	if err == nil {
		c, err = openWorkspaceCommand(ctx, path)
	}
	result := map[string]any{"workspace": "", "as_of": time.Now().UTC().Format(time.RFC3339Nano), "nodes": []workspace.NodeStatus{}}
	if err == nil {
		root, at, nodes, readErr := c.mail.Summaries(ctx, c.boundRuntime(), c.topology.Nodes)
		if root == "" {
			root = c.caller.Root
		}
		result["workspace"], result["as_of"], result["nodes"] = root, at, nodes
		err = readErr
		if c.topology.InterfaceNode == c.caller.Caller.Role {
			result["parent"], result["children"] = nil, []any{}
			if len(c.topology.Sessions) > 0 {
				caller, nameErr := c.namedCaller(ctx)
				if nameErr != nil {
					result["reason"] = nameErr.Error()
				} else {
					parent := ""
					for _, session := range c.topology.Sessions {
						if session.Name == caller.Name() {
							parent = session.Parent
						}
					}
					children := []any{}
					for _, session := range c.topology.Sessions {
						if session.Name != parent && session.Parent != caller.Name() {
							continue
						}
						row := map[string]any{"workspace": nil, "session": session.Name}
						status := workspace.NodeStatus{Node: session.InterfaceNode}
						_, _, routeErr := c.topology.InterfaceRoute(caller.Name(), c.caller.Caller.Role, session.Name+":"+session.InterfaceNode)
						if routeErr == nil {
							peer, peerErr := c.runtime.FindNamedPeer(ctx, session.Name, session.InterfaceNode)
							if peerErr == nil {
								root, _, node := c.mail.Summary(ctx, peer, session.InterfaceNode)
								status = node
								if root != "" {
									row["workspace"] = root
								}
							} else {
								status.Reason = peerErr.Error()
							}
						} else {
							status.Reason = routeErr.Error()
						}
						raw, _ := json.Marshal(status)
						var fields map[string]any
						_ = json.Unmarshal(raw, &fields)
						for key, value := range fields {
							row[key] = value
						}
						if session.Name == parent {
							result["parent"] = row
						} else {
							children = append(children, row)
						}
					}
					result["children"] = children
				}
			}
		}
	}
	if c.runtime.Socket != "" {
		final, verifyErr := c.boundRuntime().Snapshot(ctx)
		if verifyErr != nil || final.Root != c.caller.Root || final.Caller != c.caller.Caller {
			if verifyErr == nil {
				verifyErr = workspace.ErrUnknown
			}
			err = verifyErr
			unknown := make([]workspace.NodeStatus, 0, len(c.topology.Nodes))
			for _, node := range c.topology.Nodes {
				unknown = append(unknown, workspace.NodeStatus{Node: node, Reason: verifyErr.Error()})
			}
			result["nodes"] = unknown
			delete(result, "parent")
			delete(result, "children")
		}
	}
	if err != nil {
		result["reason"] = err.Error()
	}
	result["as_of"] = time.Now().UTC().Format(time.RFC3339Nano)
	if writeErr := json.NewEncoder(os.Stdout).Encode(result); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return reportedError{err}
	}
	return nil
}
