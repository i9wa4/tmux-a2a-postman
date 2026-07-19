package journal

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncCommandApprovalDecisionHistorySkipsDecisionWithoutMatchingRequest(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	now := time.Date(2026, time.July, 20, 0, 30, 0, 0, time.UTC)
	writer, err := OpenShadowWriter(sessionDir, "ctx-662", "session", os.Getpid(), now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	if _, err := writer.AppendEventWithOptions(
		CommandApprovalDecidedEventType,
		VisibilityOperatorVisible,
		CommandApprovalDecisionPayload{
			Reviewer: "orchestrator",
			Decision: ApprovalDecisionApproved,
			Reason:   "reply session lacks the request",
		},
		AppendOptions{ThreadID: "command-approval-no-request"},
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
	}

	if err := SyncCommandApprovalDecisionHistory(sessionDir); err != nil {
		t.Fatalf("SyncCommandApprovalDecisionHistory() error = %v", err)
	}
	history, err := ListCommandApprovalDecisionHistory(sessionDir)
	if err != nil {
		t.Fatalf("ListCommandApprovalDecisionHistory() error = %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("history entries = %d, want 0 for decision without local request: %#v", len(history), history)
	}
}
