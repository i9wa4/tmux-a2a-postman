package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type inspectCommandApprovalsOutput struct {
	Status      string                                      `json:"status"`
	ContextID   string                                      `json:"context_id,omitempty"`
	SessionName string                                      `json:"session_name,omitempty"`
	Threads     map[string]projection.CommandApprovalThread `json:"threads"`
}

func RunInspectCommandApprovals(args []string) error {
	fs := flag.NewFlagSet("inspect-command-approvals", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sessionDir, resolvedContextID, resolvedSessionName, err := resolveInspectMessageSessionDir(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	state, ok, err := projection.ProjectCommandApprovalState(sessionDir, time.Now())
	if err != nil {
		return fmt.Errorf("projecting command approvals: %w", err)
	}
	output := inspectCommandApprovalsOutput{
		Status:      "ok",
		ContextID:   resolvedContextID,
		SessionName: resolvedSessionName,
		Threads:     map[string]projection.CommandApprovalThread{},
	}
	if ok {
		output.Threads = state.Threads
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}
