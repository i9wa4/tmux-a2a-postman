package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

type reconcileCommandApprovalReplySlotOutput struct {
	Status      string                                                `json:"status"`
	Applied     bool                                                  `json:"applied"`
	ContextID   string                                                `json:"context_id,omitempty"`
	SessionName string                                                `json:"session_name,omitempty"`
	EventID     string                                                `json:"event_id,omitempty"`
	Plan        projection.CommandApprovalReplySlotReconciliationPlan `json:"plan"`
}

var reconcileCommandApprovalReplySlotBeforeAppendHook func() error

func RunReconcileCommandApprovalReplySlot(args []string) error {
	fs := flag.NewFlagSet("reconcile-command-approval-reply-slot", flag.ContinueOnError)
	cliutil.SetUsageWithoutContextID(fs)
	contextID := fs.String("context-id", "", "Context ID (optional, auto-resolved from tmux session)")
	configPath := fs.String("config", "", "Config file path")
	sessionName := fs.String("session", "", "tmux session name (optional, defaults to current tmux session)")
	apply := fs.Bool("apply", false, "append the audited reconciliation event")
	repair := journal.CommandApprovalReplySlotReconciledPayload{}
	fs.StringVar(&repair.InputRequestID, "input-request-id", "", "input request id to reconcile")
	fs.StringVar(&repair.ThreadID, "thread-id", "", "command approval thread id")
	fs.StringVar(&repair.CommandHash, "command-hash", "", "approved command digest")
	fs.StringVar(&repair.Requester, "requester", "", "requester node name")
	fs.StringVar(&repair.RequesterAddress, "requester-address", "", "fully-qualified requester address")
	fs.StringVar(&repair.Approver, "approver", "", "approver node name")
	fs.StringVar(&repair.ApproverAddress, "approver-address", "", "fully-qualified approver address")
	fs.StringVar(&repair.DecisionEventID, "decision-event-id", "", "recorded terminal decision event id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", fs.Args())
	}

	sessionDir, resolvedContextID, resolvedSessionName, err := resolveInspectMessageSessionDir(*contextID, *sessionName, *configPath)
	if err != nil {
		return err
	}
	now := time.Now()
	plan, ok, err := projection.ValidateCommandApprovalReplySlotReconciliation(sessionDir, resolvedSessionName, repair, now)
	if err != nil {
		return fmt.Errorf("validating command approval reply slot reconciliation: %w", err)
	}
	if !ok {
		return fmt.Errorf("command approval reply slot reconciliation unavailable: %s", plan.Reason)
	}
	output := reconcileCommandApprovalReplySlotOutput{
		Status:      plan.Status,
		ContextID:   resolvedContextID,
		SessionName: resolvedSessionName,
		Plan:        plan,
	}
	if plan.Status != "ready" && plan.Status != "already_reconciled" {
		if err := writeReconcileCommandApprovalReplySlotOutput(output); err != nil {
			return err
		}
		return fmt.Errorf("command approval reply slot reconciliation rejected: %s", plan.Reason)
	}
	if *apply && plan.Status == "ready" {
		event, appended, appendPlan, err := appendCommandApprovalReplySlotReconciliation(sessionDir, resolvedSessionName, repair, now)
		if err != nil {
			return err
		}
		output.Plan = appendPlan
		output.EventID = event.EventID
		if appended {
			output.Status = "applied"
			output.Applied = true
		} else {
			output.Status = appendPlan.Status
			output.Applied = false
		}
	}
	return writeReconcileCommandApprovalReplySlotOutput(output)
}

func appendCommandApprovalReplySlotReconciliation(sessionDir, sessionName string, repair journal.CommandApprovalReplySlotReconciledPayload, now time.Time) (journal.Event, bool, projection.CommandApprovalReplySlotReconciliationPlan, error) {
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		return journal.Event{}, false, projection.CommandApprovalReplySlotReconciliationPlan{}, err
	}
	if reconcileCommandApprovalReplySlotBeforeAppendHook != nil {
		if err := reconcileCommandApprovalReplySlotBeforeAppendHook(); err != nil {
			return journal.Event{}, false, projection.CommandApprovalReplySlotReconciliationPlan{}, err
		}
	}
	var plan projection.CommandApprovalReplySlotReconciliationPlan
	event, appended, err := writer.AppendCurrentSessionEventIfAbsentValidated(
		journal.CommandApprovalReplySlotReconciledEventType,
		journal.VisibilityOperatorVisible,
		repair,
		journal.AppendOptions{ThreadID: repair.ThreadID},
		now,
		func() error {
			currentPlan, ok, err := projection.ValidateCommandApprovalReplySlotReconciliation(sessionDir, sessionName, repair, now)
			if err != nil {
				return fmt.Errorf("fresh validation before append: %w", err)
			}
			plan = currentPlan
			if !ok {
				return fmt.Errorf("command approval reply slot reconciliation unavailable: %s", plan.Reason)
			}
			if plan.Status != "ready" {
				return fmt.Errorf("command approval reply slot reconciliation rejected: %s", plan.Reason)
			}
			return nil
		},
		func(event journal.Event) (bool, error) {
			if event.Type != journal.CommandApprovalReplySlotReconciledEventType {
				return false, nil
			}
			var existing journal.CommandApprovalReplySlotReconciledPayload
			if err := json.Unmarshal(event.Payload, &existing); err != nil {
				return false, err
			}
			return existing == repair, nil
		},
	)
	if err != nil {
		return journal.Event{}, false, plan, err
	}
	if !appended {
		plan.Status = "already_reconciled"
		plan.Reason = "matching reconciliation event already exists"
		plan.ExistingEventID = event.EventID
	}
	return event, appended, plan, nil
}

func writeReconcileCommandApprovalReplySlotOutput(output reconcileCommandApprovalReplySlotOutput) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}
