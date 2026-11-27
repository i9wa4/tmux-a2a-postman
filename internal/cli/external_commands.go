package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func (c workspaceCommand) externalBinding(ctx context.Context) (workspace.NamedRuntime, workspace.ExternalBinding, error) {
	rt, err := c.namedCaller(ctx)
	b := workspace.ExternalBinding{Root: c.topology.ExternalRoot, Workspace: c.topology.ExternalWorkspace, Session: c.topology.ExternalSession, Node: c.topology.InterfaceNode}
	if err != nil {
		return rt, b, err
	}
	if b.Root == "" || b.Node != c.caller.Caller.Role || b.Workspace != c.caller.Root || b.Session != rt.Name() {
		return rt, b, errors.New("external channel is not configured for this interface workspace/session")
	}
	return rt, b, nil
}

func RunWorkspaceChannelSync(args []string) (retErr error) {
	result := workspace.ExternalSync{Accepted: []workspace.ExternalIntake{}, Published: []string{}, Pending: []workspace.ExternalPending{}}
	var c *workspaceCommand
	verified := false
	defer func() {
		out := map[string]any{"success": retErr == nil, "as_of": time.Now().UTC().Format(time.RFC3339Nano), "channel": result}
		if verified {
			root, at, self := collectPostOperationSelf(c, nil)
			out["workspace"], out["as_of"], out["self"] = root, at, self
			if instruction := resultGuidance(self, "channel-sync", retErr == nil, false, "", len(result.Pending) > 0, result.More); instruction != "" {
				out["instruction"] = instruction
			}
		}
		if retErr != nil {
			out["reason"] = retErr.Error()
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			retErr = err
		} else if retErr != nil {
			retErr = reportedError{retErr}
		}
	}()
	f := newWorkspaceFlags("channel-sync")
	path, err := f.source(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("channel-sync takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	opened, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	c = &opened
	defer c.refreshStatusAfterOperation()
	rt, b, err := c.externalBinding(ctx)
	if err != nil {
		return err
	}
	verified = true
	if err := c.prepareRecipientContext(ctx, c.topology.InterfaceNode, false); err != nil {
		return err
	}
	retErr = retryWorkspaceAdmission(ctx, func() (bool, error) {
		result, err = c.mail.SyncExternal(ctx, rt, b)
		return len(result.Accepted) == 0 && len(result.Published) == 0, err
	})
	return retErr
}

func RunWorkspaceChannelReport(args []string) (retErr error) {
	out := map[string]any{}
	var c *workspaceCommand
	verified := false
	pendingReport := false
	defer func() {
		out["success"] = retErr == nil
		out["as_of"] = time.Now().UTC().Format(time.RFC3339Nano)
		if verified {
			root, at, self := collectPostOperationSelf(c, nil)
			out["workspace"], out["as_of"], out["self"] = root, at, self
			existing := ""
			if pendingReport {
				existing = "This pending external report requests an exact external decision; retain its scope and follow the channel decision procedure. It does not grant execution approval."
			}
			if instruction := resultGuidance(self, "channel-report-heredoc", retErr == nil, false, existing, false, false); instruction != "" {
				out["instruction"] = instruction
			}
		}
		if retErr != nil {
			out["reason"] = retErr.Error()
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			retErr = err
		} else if retErr != nil {
			retErr = reportedError{retErr}
		}
	}()
	f := newWorkspaceFlags("channel-report-heredoc")
	to := f.fs.String("to", "", "one claimed external message ID")
	state := f.fs.String("state", "", "done, blocked or pending")
	scope := f.fs.String("scope", "", "SHA256 of the exact pending action and context")
	path, err := f.source(args)
	if err != nil {
		return err
	}
	if *to == "" || f.fs.NArg() != 0 {
		return errors.New("channel-report-heredoc requires --to, --state and heredoc stdin")
	}
	body, err := readHeredoc(os.Stdin)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opened, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	c = &opened
	defer c.refreshStatusAfterOperation()
	rt, b, err := c.externalBinding(ctx)
	if err != nil {
		return err
	}
	verified = true
	var msg workspace.Message
	err = retryWorkspaceAdmission(ctx, func() (bool, error) {
		msg, err = c.mail.ReportExternal(ctx, rt, b, *to, *state, *scope, body)
		return msg.ID == "", err
	})
	if msg.ID != "" {
		out["message_id"] = msg.ID
		out["exchange_id"] = msg.External.ExchangeID
		pendingReport = *state == "pending"
	}
	out["publication"] = "local outbox availability; human receipt is unknown"
	return err
}
