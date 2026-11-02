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
	if len(c.topology.Sessions) > 0 {
		rootInterface := false
		for _, session := range c.topology.Sessions {
			if session.Name == rt.Name() && session.Parent == "" && session.InterfaceNode == b.Node {
				rootInterface = true
			}
		}
		if !rootInterface {
			return rt, b, errors.New("external channel belongs only to a declared top-level interface")
		}
	}
	return rt, b, nil
}

func RunWorkspaceChannelSync(args []string) (retErr error) {
	result := workspace.ExternalSync{Accepted: []workspace.ExternalIntake{}, Published: []string{}, Pending: []workspace.ExternalPending{}}
	defer func() {
		out := map[string]any{"success": retErr == nil, "as_of": time.Now().UTC().Format(time.RFC3339Nano), "channel": result}
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
	path, err := f.parse(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("channel-sync takes no positional arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	rt, b, err := c.externalBinding(ctx)
	if err != nil {
		return err
	}
	if err := c.prepareRecipientContext(ctx); err != nil {
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
	defer func() {
		out["success"] = retErr == nil
		out["as_of"] = time.Now().UTC().Format(time.RFC3339Nano)
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
	path, err := f.parse(args)
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
	c, err := openWorkspaceCommand(ctx, path)
	if err != nil {
		return err
	}
	rt, b, err := c.externalBinding(ctx)
	if err != nil {
		return err
	}
	var msg workspace.Message
	err = retryWorkspaceAdmission(ctx, func() (bool, error) {
		msg, err = c.mail.ReportExternal(ctx, rt, b, *to, *state, *scope, body)
		return msg.ID == "", err
	})
	if msg.ID != "" {
		out["message_id"] = msg.ID
		out["exchange_id"] = msg.External.ExchangeID
	}
	out["publication"] = "local outbox availability; human receipt is unknown"
	return err
}
