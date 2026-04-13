package projection

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestThreadIDRequiredForApprovalProjection(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 20, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	_, err = writer.AppendEvent(
		journal.ApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.ApprovalRequestPayload{
			Requester: "orchestrator",
			Reviewer:  "critic",
			MessageID: "20260414-052001-r1111-from-orchestrator-to-critic.md",
		},
		now.Add(time.Second),
	)
	if err == nil {
		t.Fatal("AppendEvent() error = nil, want thread_id requirement")
	}
	if !strings.Contains(err.Error(), "thread_id") {
		t.Fatalf("AppendEvent() error = %v, want thread_id requirement", err)
	}
}

func TestApprovalProjectionReplay(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 25, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	threadID := "thread-review-01"
	if _, err := writer.AppendEventWithOptions(
		journal.ApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.ApprovalRequestPayload{
			Requester: "orchestrator",
			Reviewer:  "critic",
			MessageID: "20260414-052501-r1111-from-orchestrator-to-critic.md",
		},
		journal.AppendOptions{ThreadID: threadID},
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendEventWithOptions(request): %v", err)
	}
	if _, err := writer.AppendEventWithOptions(
		journal.ApprovalDecidedEventType,
		journal.VisibilityOperatorVisible,
		journal.ApprovalDecisionPayload{
			Reviewer:  "critic",
			Decision:  journal.ApprovalDecisionApproved,
			MessageID: "20260414-052502-r2222-from-critic-to-orchestrator.md",
		},
		journal.AppendOptions{ThreadID: threadID},
		now.Add(2*time.Second),
	); err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}

	got, ok, err := ProjectThreadApproval(sessionDir)
	if err != nil {
		t.Fatalf("ProjectThreadApproval() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectThreadApproval() ok = false, want true")
	}

	thread, ok := got.Threads[threadID]
	if !ok {
		t.Fatalf("ProjectThreadApproval() missing thread %q in %#v", threadID, got.Threads)
	}
	if thread.Status != ApprovalStatusApproved {
		t.Fatalf("thread status = %q, want %q", thread.Status, ApprovalStatusApproved)
	}
	if thread.Requester != "orchestrator" {
		t.Fatalf("thread requester = %q, want orchestrator", thread.Requester)
	}
	if thread.Reviewer != "critic" {
		t.Fatalf("thread reviewer = %q, want critic", thread.Reviewer)
	}
	if thread.RequestMessageID != "20260414-052501-r1111-from-orchestrator-to-critic.md" {
		t.Fatalf("thread request message = %q", thread.RequestMessageID)
	}
	if thread.DecisionMessageID != "20260414-052502-r2222-from-critic-to-orchestrator.md" {
		t.Fatalf("thread decision message = %q", thread.DecisionMessageID)
	}
}

func TestApprovalProjectionFailsClosedWithoutThreadID(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 5, 30, 0, 0, time.UTC)

	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	event, err := writer.AppendEventWithOptions(
		journal.ApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.ApprovalRequestPayload{
			Requester: "orchestrator",
			Reviewer:  "critic",
			MessageID: "20260414-053001-r1111-from-orchestrator-to-critic.md",
		},
		journal.AppendOptions{ThreadID: "thread-review-02"},
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(request): %v", err)
	}

	recordPath, err := approvalEventRecordPath(sessionDir, event.Sequence)
	if err != nil {
		t.Fatalf("approvalEventRecordPath(): %v", err)
	}

	data, err := os.ReadFile(recordPath)
	if err != nil {
		t.Fatalf("ReadFile(record): %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json.Unmarshal(record): %v", err)
	}
	delete(raw, "thread_id")

	mutated, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("json.Marshal(record): %v", err)
	}
	if err := os.WriteFile(recordPath, mutated, 0o600); err != nil {
		t.Fatalf("WriteFile(record): %v", err)
	}

	got, ok, err := ProjectThreadApproval(sessionDir)
	if err == nil {
		t.Fatalf("ProjectThreadApproval() error = nil, want thread_id failure with %#v", got)
	}
	if ok {
		t.Fatalf("ProjectThreadApproval() ok = true, want false with error %v", err)
	}
	if !strings.Contains(err.Error(), "thread_id") {
		t.Fatalf("ProjectThreadApproval() error = %v, want thread_id failure", err)
	}
}

func approvalEventRecordPath(sessionDir string, sequence int) (string, error) {
	entries, err := os.ReadDir(journal.RecordsDir(sessionDir))
	if err != nil {
		return "", err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		if strings.HasPrefix(name, formatSequencePrefix(sequence)) {
			return filepath.Join(journal.RecordsDir(sessionDir), name), nil
		}
	}
	return "", os.ErrNotExist
}

func formatSequencePrefix(sequence int) string {
	return fmt.Sprintf("%012d-", sequence)
}
