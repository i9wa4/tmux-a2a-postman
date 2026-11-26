package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// RunWorkspaceHeartbeat sends one durable, reply-required request through the
// ordinary verified local route. The current CLI owns config and mail; daemon
// never calls this function in-process.
func RunWorkspaceHeartbeat(args []string) (retErr error) {
	trace := invocationDiagnostic("heartbeat")
	var c *workspaceCommand
	out := mailOutput{trace: trace, facts: diagnostic.Facts{Mail: "not_attempted", Capture: "not_attempted", Notification: "not_attempted"}}
	defer func() { retErr = finishMail(c, &out, retErr) }()
	f := newWorkspaceFlags("heartbeat")
	source, err := f.source(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 0 {
		return errors.New("heartbeat takes no positional arguments")
	}
	var body string
	if !defaultStdinIsTerminal(os.Stdin) {
		raw, err := io.ReadAll(io.LimitReader(os.Stdin, 60*1024+1))
		if err != nil {
			return err
		}
		if len(raw) > 60*1024 || !utf8.Valid(raw) {
			return errors.New("heartbeat body exceeds 61440 UTF-8 bytes")
		}
		body = string(raw)
	}
	ctx, cancel := context.WithTimeout(diagnostic.With(context.Background(), trace), 10*time.Second)
	defer cancel()
	opened, err := openWorkspaceCommand(ctx, source)
	if err != nil {
		return err
	}
	c = &opened
	c.attachDiagnostic(ctx, trace)
	if body == "" {
		body = c.topology.HeartbeatBody
	}
	if body == "" {
		return errors.New("heartbeat default body is absent from selected postman.md")
	}
	to := c.topology.InterfaceNode
	if to == "" || !c.permits(to) || to == c.caller.Caller.Role {
		return errors.New("heartbeat requires a verified outgoing edge to interface_node")
	}
	if err := c.prepareRecipientContext(ctx, to, false); err != nil {
		return err
	}
	out.RefreshEligible = true
	var msg workspace.Message
	err = retryWorkspaceAdmission(ctx, func() (bool, error) {
		var sendErr error
		msg, sendErr = c.mail.Send(ctx, c.boundRuntime(), to, body, "", true)
		return msg.ID == "", sendErr
	})
	if err == nil {
		notice := c.mail.Store.NotifyCommittedAfterCommit(ctx, c.boundRuntime(), c.runtime, msg)
		out.facts.Notification = notice.Status
		if notice.Status != "accepted" {
			out.Notification = &notice
		}
	}
	noteMessage(trace, msg, c.caller.Caller.Role)
	if err == nil {
		out.facts.Mail = "committed"
	} else {
		out.facts.Mail = "unknown"
	}
	if err != nil && msg.ID != "" {
		out.RecoveryMessageID = msg.ID
	}
	return err
}

type scheduleResult struct {
	Command   string `json:"command"`
	Every     string `json:"every"`
	NextDueAt string `json:"next_due_at"`
}

// RunScheduleNext is the small, versioned query an older daemon asks of the
// current CLI. It does not require a tmux session or send mail.
func RunScheduleNext(args []string) error {
	f := newWorkspaceFlags("schedule-next")
	source, err := f.source(args)
	if err != nil {
		return err
	}
	if f.fs.NArg() != 1 || f.fs.Arg(0) != "heartbeat" {
		return errors.New("schedule-next requires the allowlisted heartbeat command")
	}
	topology, err := source.Load(context.Background())
	if err != nil {
		return err
	}
	if topology.HeartbeatEvery == "" {
		return errors.New("heartbeat interval is absent from selected postman.md")
	}
	every, err := time.ParseDuration(topology.HeartbeatEvery)
	if err != nil || every < minimumDaemonInterval {
		return fmt.Errorf("invalid heartbeat interval %q", topology.HeartbeatEvery)
	}
	return json.NewEncoder(os.Stdout).Encode(scheduleResult{
		Command: "heartbeat", Every: topology.HeartbeatEvery,
		NextDueAt: time.Now().UTC().Add(every).Format(time.RFC3339Nano),
	})
}
