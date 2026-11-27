package cli

import (
	"context"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/diagnostic"
	"github.com/i9wa4/tmux-a2a-postman/internal/workspace"
)

// collectPostOperationSelf uses the existing bounded, role-bound summary. It
// never changes the result of the operation whose facts it supplements.
func collectPostOperationSelf(c *workspaceCommand, trace *diagnostic.Recorder) (string, string, workspace.NodeStatus) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var span *diagnostic.Span
	if trace != nil {
		ctx = diagnostic.With(ctx, trace)
		span = trace.Start("self_summary")
	}
	root, at, self := c.mail.Summary(ctx, c.boundRuntime(), c.caller.Caller.Role)
	if span != nil {
		outcome := "ok"
		if self.Reason != "" {
			outcome = "unknown"
		}
		span.End(outcome)
	}
	if root == "" {
		root = c.caller.Root
	}
	return root, at, self
}
