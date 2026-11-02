package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/version"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

func invocationDiagnostic(command string) *diagnostic.Recorder {
	return diagnostic.New(command, version.Version, version.Commit)
}

func (c *workspaceCommand) attachDiagnostic(ctx context.Context, r *diagnostic.Recorder) {
	c.trace = r
	c.mail.Diagnostics = r
	if r == nil || r.Failed() {
		return
	}
	span := r.Start("diagnostic_destination")
	// The storage validation itself must not recursively log its own locks.
	storageCtx, cancel := context.WithTimeout(diagnostic.With(ctx, nil), time.Second)
	defer cancel()
	var sink io.WriteCloser
	var err error
	for {
		sink, err = c.mail.Store.OpenDiagnostic(storageCtx, c.boundRuntime(), r.Date(), r.ID())
		if !errors.Is(err, workspace.ErrBusy) {
			break
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-storageCtx.Done():
			timer.Stop()
			err = storageCtx.Err()
		case <-timer.C:
		}
		if storageCtx.Err() != nil {
			break
		}
	}
	if err != nil {
		span.Error(err)
		r.MarkFailed()
		return
	}
	raw, _ := json.Marshal(c.topology)
	sum := sha256.Sum256(raw)
	identity := diagnostic.Identity{
		PanePID: c.caller.Caller.PanePID, Workspace: c.caller.Root, Node: c.caller.Caller.Role,
		ConfigFingerprint: "sha256:" + hex.EncodeToString(sum[:]), RuntimeFingerprint: workspace.DiagnosticRuntimeFingerprint(c.caller.Caller.Owner), Session: c.caller.Caller.Owner.Session, Pane: c.caller.Caller.Owner.Pane,
	}
	r.Attach(sink, identity)
	span.End("ok")
}

func diagnosticWarning(w io.Writer, r *diagnostic.Recorder) {
	if r.Failed() {
		_, _ = io.WriteString(w, "postman: invocation diagnostics incomplete; operation result is unchanged.\n")
	}
}

func noteMessage(r *diagnostic.Recorder, msg workspace.Message, actor string) {
	counterparty := msg.To
	if msg.To == actor {
		counterparty = msg.From
	}
	ids := []string{}
	if msg.ReplyTo != "" {
		ids = append(ids, msg.ReplyTo)
	}
	for _, ref := range msg.Replies {
		ids = append(ids, ref.MessageID)
	}
	r.Note(diagnostic.Facts{MessageID: msg.ID, InputRequestID: msg.InputRequestID, ReplyToIDs: ids, Counterparty: counterparty})
}
