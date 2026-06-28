package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestCommandApprovalProjectionStatuses(t *testing.T) {
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		name       string
		write      func(t *testing.T, writer *journal.Writer, now time.Time, threadID string)
		projectNow time.Time
		want       CommandApprovalStatus
	}{
		{
			name: "approved",
			write: func(t *testing.T, writer *journal.Writer, now time.Time, threadID string) {
				appendCommandApprovalRequestForTest(t, writer, threadID, "orchestrator", now.Add(15*time.Minute), now.Add(time.Second))
				appendCommandApprovalDecisionForTest(t, writer, threadID, "orchestrator", journal.ApprovalDecisionApproved, now.Add(2*time.Second))
			},
			projectNow: now.Add(3 * time.Second),
			want:       CommandApprovalStatusApproved,
		},
		{
			name: "rejected",
			write: func(t *testing.T, writer *journal.Writer, now time.Time, threadID string) {
				appendCommandApprovalRequestForTest(t, writer, threadID, "orchestrator", now.Add(15*time.Minute), now.Add(time.Second))
				appendCommandApprovalDecisionForTest(t, writer, threadID, "orchestrator", journal.ApprovalDecisionRejected, now.Add(2*time.Second))
			},
			projectNow: now.Add(3 * time.Second),
			want:       CommandApprovalStatusRejected,
		},
		{
			name: "wrong reviewer",
			write: func(t *testing.T, writer *journal.Writer, now time.Time, threadID string) {
				appendCommandApprovalRequestForTest(t, writer, threadID, "orchestrator", now.Add(15*time.Minute), now.Add(time.Second))
				appendCommandApprovalDecisionForTest(t, writer, threadID, "critic", journal.ApprovalDecisionApproved, now.Add(2*time.Second))
			},
			projectNow: now.Add(3 * time.Second),
			want:       CommandApprovalStatusWrongReviewer,
		},
		{
			name: "expired",
			write: func(t *testing.T, writer *journal.Writer, now time.Time, threadID string) {
				appendCommandApprovalRequestForTest(t, writer, threadID, "orchestrator", now.Add(time.Minute), now.Add(time.Second))
				appendCommandApprovalDecisionForTest(t, writer, threadID, "orchestrator", journal.ApprovalDecisionApproved, now.Add(2*time.Second))
			},
			projectNow: now.Add(2 * time.Minute),
			want:       CommandApprovalStatusExpired,
		},
		{
			name: "stale decision",
			write: func(t *testing.T, writer *journal.Writer, now time.Time, threadID string) {
				appendCommandApprovalDecisionForTest(t, writer, threadID, "orchestrator", journal.ApprovalDecisionApproved, now.Add(time.Second))
			},
			projectNow: now.Add(2 * time.Second),
			want:       CommandApprovalStatusStale,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sessionDir := t.TempDir()
			writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
			if err != nil {
				t.Fatalf("OpenShadowWriter() error = %v", err)
			}
			threadID := "command-approval-test"
			tc.write(t, writer, now, threadID)

			got, ok, err := ProjectCommandApprovalState(sessionDir, tc.projectNow)
			if err != nil {
				t.Fatalf("ProjectCommandApprovalState() error = %v", err)
			}
			if !ok {
				t.Fatal("ProjectCommandApprovalState() ok = false, want true")
			}
			thread, ok := got.Threads[threadID]
			if !ok {
				t.Fatalf("missing thread %q in %#v", threadID, got.Threads)
			}
			if thread.Status != tc.want {
				t.Fatalf("thread status = %q, want %q", thread.Status, tc.want)
			}
		})
	}
}

func appendCommandApprovalRequestForTest(t *testing.T, writer *journal.Writer, threadID, reviewer string, expiresAt, now time.Time) {
	t.Helper()

	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalRequestPayload{
			Requester:   "worker",
			Reviewer:    reviewer,
			Mode:        "blocking",
			Label:       "nix-build",
			Category:    "verification",
			CommandHash: "sha256:test",
			Reason:      "verify build",
			ExpiresAt:   expiresAt.Format(time.RFC3339Nano),
		},
		journal.AppendOptions{ThreadID: threadID},
		now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(request): %v", err)
	}
}

func appendCommandApprovalDecisionForTest(t *testing.T, writer *journal.Writer, threadID, reviewer string, decision journal.ApprovalDecision, now time.Time) {
	t.Helper()

	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalDecidedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalDecisionPayload{
			Reviewer: reviewer,
			Decision: decision,
			Reason:   "reviewed",
		},
		journal.AppendOptions{ThreadID: threadID},
		now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}
}
