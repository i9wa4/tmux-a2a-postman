package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRunReconcileCommandApprovalReplySlotDryRunAndApply(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)

	args := reconcileCommandApprovalReplySlotArgs(contextID, sessionName, repair)
	stdout, stderr, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(args)
	})
	if err != nil {
		t.Fatalf("dry-run RunReconcileCommandApprovalReplySlot() error = %v stderr=%q", err, stderr)
	}
	var dryRun reconcileCommandApprovalReplySlotOutput
	if err := json.Unmarshal([]byte(stdout), &dryRun); err != nil {
		t.Fatalf("json.Unmarshal(dry-run %q): %v", stdout, err)
	}
	if dryRun.Status != "ready" || dryRun.Applied {
		t.Fatalf("dry-run output = %#v, want ready without apply", dryRun)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 0 {
		t.Fatal("dry-run appended reconciliation event")
	}

	stdout, stderr, err = captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(append(args, "--apply"))
	})
	if err != nil {
		t.Fatalf("apply RunReconcileCommandApprovalReplySlot() error = %v stderr=%q", err, stderr)
	}
	var applied reconcileCommandApprovalReplySlotOutput
	if err := json.Unmarshal([]byte(stdout), &applied); err != nil {
		t.Fatalf("json.Unmarshal(apply %q): %v", stdout, err)
	}
	if applied.Status != "applied" || !applied.Applied || applied.EventID == "" {
		t.Fatalf("apply output = %#v, want applied event", applied)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 1 {
		t.Fatal("apply did not append exactly one reconciliation event")
	}
	projected, ok, err := projection.ProjectMessageInputRequestStateAt(sessionDir, sessionName, now.Add(10*time.Minute), 3600)
	if err != nil || !ok {
		t.Fatalf("ProjectMessageInputRequestStateAt() = (%#v, %v, %v)", projected, ok, err)
	}
	if len(projected.InputRequired) != 0 || len(projected.WaitingOnInput) != 0 {
		t.Fatalf("projection still has open request after apply: %#v", projected)
	}

	stdout, stderr, err = captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(append(args, "--apply"))
	})
	if err != nil {
		t.Fatalf("second apply RunReconcileCommandApprovalReplySlot() error = %v stderr=%q", err, stderr)
	}
	var again reconcileCommandApprovalReplySlotOutput
	if err := json.Unmarshal([]byte(stdout), &again); err != nil {
		t.Fatalf("json.Unmarshal(second apply %q): %v", stdout, err)
	}
	if again.Status != "already_reconciled" || again.Applied {
		t.Fatalf("second apply output = %#v, want already_reconciled without append", again)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 1 {
		t.Fatal("second apply appended duplicate reconciliation event")
	}
}

func TestRunReconcileCommandApprovalReplySlotRejectsMismatchedTuple(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC))
	repair.CommandHash = "sha256:wrong"

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(reconcileCommandApprovalReplySlotArgs(contextID, sessionName, repair))
	})
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("RunReconcileCommandApprovalReplySlot() error = %v, want rejection", err)
	}
	var output reconcileCommandApprovalReplySlotOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout, err)
	}
	if output.Status != "rejected" || output.Plan.Reason == "" {
		t.Fatalf("output = %#v, want rejected plan", output)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 0 {
		t.Fatal("rejected tuple appended reconciliation event")
	}
}

func TestRunReconcileCommandApprovalReplySlotRejectsPositionalOperands(t *testing.T) {
	stdout, _, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot([]string{"unexpected"})
	})
	if err == nil || !strings.Contains(err.Error(), "unexpected positional arguments") {
		t.Fatalf("RunReconcileCommandApprovalReplySlot() error = %v, want positional rejection", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
}

func TestAppendCommandApprovalReplySlotReconciliationRevalidatesCurrentOpenSlot(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		t.Fatalf("OpenCurrentWriter() error = %v", err)
	}
	reply := "---\nparams:\n" +
		"  from: approver\n" +
		"  to: worker\n" +
		"  messageId: decision.md\n" +
		"  replyPolicy: none\n" +
		"  thread_id: " + repair.ThreadID + "\n" +
		"  command_hash: " + repair.CommandHash + "\n" +
		"  fills_input_request_id: " + repair.InputRequestID + "\n" +
		"---\n\nordinary closure\n"
	appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionDeliveredEventType, "decision.md", "approver", "worker", reply, now.Add(4*time.Second))
	appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionPostConsumedEventType, "decision.md", "approver", "worker", reply, now.Add(4*time.Second))

	_, appended, plan, err := appendCommandApprovalReplySlotReconciliation(sessionDir, sessionName, repair, now.Add(5*time.Second))
	if err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Fatalf("appendCommandApprovalReplySlotReconciliation() error = %v, want rejection", err)
	}
	if appended || plan.Status != "rejected" || !strings.Contains(plan.Reason, "not currently open") {
		t.Fatalf("append result appended=%v plan=%#v, want rejected current-open validation", appended, plan)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 0 {
		t.Fatal("fresh validation failure appended reconciliation event")
	}
}

func TestAppendCommandApprovalReplySlotReconciliationReportsDedupedAsAlreadyReconciled(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)
	writer, err := journal.OpenCurrentWriter(sessionDir)
	if err != nil {
		t.Fatalf("OpenCurrentWriter() error = %v", err)
	}
	existing, err := writer.AppendEventWithOptions(journal.CommandApprovalReplySlotReconciledEventType, journal.VisibilityOperatorVisible, repair, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("AppendEventWithOptions(reconciliation): %v", err)
	}

	event, appended, plan, err := appendCommandApprovalReplySlotReconciliation(sessionDir, sessionName, repair, now.Add(5*time.Second))
	if err != nil {
		t.Fatalf("appendCommandApprovalReplySlotReconciliation() error = %v", err)
	}
	if appended || plan.Status != "already_reconciled" || plan.ExistingEventID != existing.EventID || event.EventID != existing.EventID {
		t.Fatalf("append result event=%#v appended=%v plan=%#v, want existing already_reconciled", event, appended, plan)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 1 {
		t.Fatal("deduped append created duplicate reconciliation event")
	}
}

func TestRunReconcileCommandApprovalReplySlotApplyRejectsSlotClosedAfterInitialValidation(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)
	args := reconcileCommandApprovalReplySlotArgs(contextID, sessionName, repair)

	reconcileCommandApprovalReplySlotBeforeAppendHook = func() error {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
		writer, err := journal.OpenCurrentWriter(sessionDir)
		if err != nil {
			return err
		}
		reply := "---\nparams:\n" +
			"  from: approver\n" +
			"  to: worker\n" +
			"  messageId: decision.md\n" +
			"  replyPolicy: none\n" +
			"  thread_id: " + repair.ThreadID + "\n" +
			"  command_hash: " + repair.CommandHash + "\n" +
			"  fills_input_request_id: " + repair.InputRequestID + "\n" +
			"---\n\nordinary closure\n"
		appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionDeliveredEventType, "decision.md", "approver", "worker", reply, now.Add(4*time.Second))
		appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionPostConsumedEventType, "decision.md", "approver", "worker", reply, now.Add(4*time.Second))
		return nil
	}
	defer func() {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
	}()

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(append(args, "--apply"))
	})
	if err == nil || !strings.Contains(err.Error(), "not currently open") {
		t.Fatalf("RunReconcileCommandApprovalReplySlot() error = %v, want in-fence closed-slot rejection", err)
	}
	if strings.Contains(stdout, `"applied"`) {
		t.Fatalf("stdout = %q, want no applied report", stdout)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 0 {
		t.Fatal("closed-slot race appended reconciliation event")
	}
}

func TestRunReconcileCommandApprovalReplySlotApplyRejectsConflictingDecisionAfterInitialValidation(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)
	args := reconcileCommandApprovalReplySlotArgs(contextID, sessionName, repair)

	reconcileCommandApprovalReplySlotBeforeAppendHook = func() error {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
		writer, err := journal.OpenCurrentWriter(sessionDir)
		if err != nil {
			return err
		}
		_, err = writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
			Reviewer:         "approver",
			ReviewerAddress:  repair.ApproverAddress,
			RequesterAddress: repair.RequesterAddress,
			Decision:         journal.ApprovalDecisionRejected,
			MessageID:        "decision-2.md",
			InputRequestID:   repair.InputRequestID,
			CommandHash:      repair.CommandHash,
		}, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(4*time.Second))
		return err
	}
	defer func() {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
	}()

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(append(args, "--apply"))
	})
	if err == nil || !strings.Contains(err.Error(), "expected exactly one effective terminal command approval decision") {
		t.Fatalf("RunReconcileCommandApprovalReplySlot() error = %v, want in-fence conflicting-decision rejection", err)
	}
	if strings.Contains(stdout, `"applied"`) {
		t.Fatalf("stdout = %q, want no applied report", stdout)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 0 {
		t.Fatal("conflicting-decision race appended reconciliation event")
	}
}

func TestRunReconcileCommandApprovalReplySlotApplyRejectsDedupedConflictingDecisionAfterInitialValidation(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)
	args := reconcileCommandApprovalReplySlotArgs(contextID, sessionName, repair)

	reconcileCommandApprovalReplySlotBeforeAppendHook = func() error {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
		writer, err := journal.OpenCurrentWriter(sessionDir)
		if err != nil {
			return err
		}
		if _, err := writer.AppendEventWithOptions(journal.CommandApprovalReplySlotReconciledEventType, journal.VisibilityOperatorVisible, repair, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(4*time.Second)); err != nil {
			return err
		}
		_, err = writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
			Reviewer:         "approver",
			ReviewerAddress:  repair.ApproverAddress,
			RequesterAddress: repair.RequesterAddress,
			Decision:         journal.ApprovalDecisionRejected,
			MessageID:        "decision-2.md",
			InputRequestID:   repair.InputRequestID,
			CommandHash:      repair.CommandHash,
		}, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(5*time.Second))
		return err
	}
	defer func() {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
	}()

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(append(args, "--apply"))
	})
	if err == nil || !strings.Contains(err.Error(), "expected exactly one effective terminal command approval decision") {
		t.Fatalf("RunReconcileCommandApprovalReplySlot() error = %v, want in-fence deduped conflicting-decision rejection", err)
	}
	if strings.Contains(stdout, `"already_reconciled"`) || strings.Contains(stdout, `"applied"`) {
		t.Fatalf("stdout = %q, want no false dedupe or applied report", stdout)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 1 {
		t.Fatal("deduped conflicting-decision race appended a new reconciliation event")
	}
}

func TestRunReconcileCommandApprovalReplySlotApplyRejectsDedupedSlotClosedAfterInitialValidation(t *testing.T) {
	baseDir := t.TempDir()
	contextID := "ctx-reconcile"
	sessionName := "review"
	sessionDir := filepath.Join(baseDir, contextID, sessionName)
	t.Setenv("POSTMAN_HOME", baseDir)
	now := time.Date(2026, time.May, 10, 12, 0, 0, 0, time.UTC)
	repair := appendCLIReconcileableCommandApprovalSlot(t, sessionDir, now)
	args := reconcileCommandApprovalReplySlotArgs(contextID, sessionName, repair)

	reconcileCommandApprovalReplySlotBeforeAppendHook = func() error {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
		writer, err := journal.OpenCurrentWriter(sessionDir)
		if err != nil {
			return err
		}
		if _, err := writer.AppendEventWithOptions(journal.CommandApprovalReplySlotReconciledEventType, journal.VisibilityOperatorVisible, repair, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(4*time.Second)); err != nil {
			return err
		}
		reply := "---\nparams:\n" +
			"  from: approver\n" +
			"  to: worker\n" +
			"  messageId: decision.md\n" +
			"  replyPolicy: none\n" +
			"  thread_id: " + repair.ThreadID + "\n" +
			"  command_hash: " + repair.CommandHash + "\n" +
			"  fills_input_request_id: " + repair.InputRequestID + "\n" +
			"---\n\nordinary closure\n"
		appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionDeliveredEventType, "decision.md", "approver", "worker", reply, now.Add(5*time.Second))
		appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionPostConsumedEventType, "decision.md", "approver", "worker", reply, now.Add(5*time.Second))
		return nil
	}
	defer func() {
		reconcileCommandApprovalReplySlotBeforeAppendHook = nil
	}()

	stdout, _, err := captureCommandOutput(t, func() error {
		return RunReconcileCommandApprovalReplySlot(append(args, "--apply"))
	})
	if err == nil || !strings.Contains(err.Error(), "not currently open") {
		t.Fatalf("RunReconcileCommandApprovalReplySlot() error = %v, want in-fence deduped closed-slot rejection", err)
	}
	if strings.Contains(stdout, `"already_reconciled"`) || strings.Contains(stdout, `"applied"`) {
		t.Fatalf("stdout = %q, want no false dedupe or applied report", stdout)
	}
	if countCommandApprovalReplySlotReconciledEvents(t, sessionDir) != 1 {
		t.Fatal("deduped closed-slot race appended a new reconciliation event")
	}
}

func appendCLIReconcileableCommandApprovalSlot(t *testing.T, sessionDir string, now time.Time) journal.CommandApprovalReplySlotReconciledPayload {
	t.Helper()
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-reconcile", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	repair := journal.CommandApprovalReplySlotReconciledPayload{
		InputRequestID:   "ireq_reconcile_cli",
		ThreadID:         "command-approval-reconcile-cli",
		CommandHash:      "sha256:reconcile-cli",
		Requester:        "worker",
		RequesterAddress: "review:worker",
		Approver:         "approver",
		ApproverAddress:  "review:approver",
	}
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
		Requester:              repair.Requester,
		RequesterAddress:       repair.RequesterAddress,
		Reviewer:               "orchestrator",
		CommandApproverNode:    repair.Approver,
		CommandApproverAddress: repair.ApproverAddress,
		Mode:                   "blocking",
		Label:                  "protected",
		CommandHash:            repair.CommandHash,
		InputRequestID:         repair.InputRequestID,
		ExpiresAt:              now.Add(time.Minute).Format(time.RFC3339Nano),
	}, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(request): %v", err)
	}
	request := "---\nparams:\n" +
		"  from: worker\n" +
		"  to: approver\n" +
		"  messageId: request.md\n" +
		"  replyPolicy: required\n" +
		"  thread_id: " + repair.ThreadID + "\n" +
		"  input_request_id: " + repair.InputRequestID + "\n" +
		"  command_hash: " + repair.CommandHash + "\n" +
		"---\n\napproval requested\n"
	appendInputRequestMailboxEventForCLI(t, writer, projection.MailboxProjectionDeliveredEventType, "request.md", "worker", "approver", request, now.Add(2*time.Second))
	decision, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
		Reviewer:         "approver",
		ReviewerAddress:  repair.ApproverAddress,
		RequesterAddress: repair.RequesterAddress,
		Decision:         journal.ApprovalDecisionApproved,
		MessageID:        "decision.md",
		InputRequestID:   repair.InputRequestID,
		CommandHash:      repair.CommandHash,
	}, journal.AppendOptions{ThreadID: repair.ThreadID}, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}
	repair.DecisionEventID = decision.EventID
	return repair
}

func appendInputRequestMailboxEventForCLI(t *testing.T, writer *journal.Writer, eventType string, messageID, from, to, content string, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: messageID,
		From:      from,
		To:        to,
		Content:   content,
	}, now); err != nil {
		t.Fatalf("AppendEvent(%s, %s): %v", eventType, messageID, err)
	}
}

func reconcileCommandApprovalReplySlotArgs(contextID, sessionName string, repair journal.CommandApprovalReplySlotReconciledPayload) []string {
	return []string{
		"--context-id", contextID,
		"--session", sessionName,
		"--input-request-id", repair.InputRequestID,
		"--thread-id", repair.ThreadID,
		"--command-hash", repair.CommandHash,
		"--requester", repair.Requester,
		"--requester-address", repair.RequesterAddress,
		"--approver", repair.Approver,
		"--approver-address", repair.ApproverAddress,
		"--decision-event-id", repair.DecisionEventID,
	}
}

func countCommandApprovalReplySlotReconciledEvents(t *testing.T, sessionDir string) int {
	t.Helper()
	count := 0
	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	for _, event := range events {
		if event.Type == journal.CommandApprovalReplySlotReconciledEventType {
			count++
		}
	}
	return count
}
