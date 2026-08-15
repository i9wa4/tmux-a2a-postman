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
			want:       CommandApprovalStatusPending,
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

func TestCommandApprovalProjectionPreservesLegacyPreAddressTerminalDecision(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}
	threadID := "command-approval-legacy"
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalRequestedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalRequestPayload{
		Requester:           "worker",
		Reviewer:            "orchestrator",
		CommandApproverNode: "orchestrator",
		Mode:                "blocking",
		Label:               "legacy",
		CommandHash:         "sha256:legacy",
		InputRequestID:      "ireq_legacy",
	}, journal.AppendOptions{ThreadID: threadID}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(request) error = %v", err)
	}
	if _, err := writer.AppendEventWithOptions(journal.CommandApprovalDecidedEventType, journal.VisibilityOperatorVisible, journal.CommandApprovalDecisionPayload{
		Reviewer:       "orchestrator",
		Decision:       journal.ApprovalDecisionRejected,
		Reason:         "legacy rejection",
		InputRequestID: "ireq_legacy",
		CommandHash:    "sha256:legacy",
	}, journal.AppendOptions{ThreadID: threadID}, now.Add(2*time.Second)); err != nil {
		t.Fatalf("AppendEventWithOptions(decision) error = %v", err)
	}

	got, ok, err := ProjectCommandApprovalState(sessionDir, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("ProjectCommandApprovalState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectCommandApprovalState() ok = false, want true")
	}
	thread := got.Threads[threadID]
	if thread.Status != CommandApprovalStatusRejected || thread.Reason != "legacy rejection" {
		t.Fatalf("legacy thread = %#v, want rejected historical decision", thread)
	}
	if !thread.HistoricalOnly {
		t.Fatalf("legacy thread = %#v, want historical-only disposition", thread)
	}
}

func appendCommandApprovalRequestForTest(t *testing.T, writer *journal.Writer, threadID, reviewer string, expiresAt, now time.Time) {
	t.Helper()

	_, err := writer.AppendEventWithOptions(
		journal.CommandApprovalRequestedEventType,
		journal.VisibilityOperatorVisible,
		journal.CommandApprovalRequestPayload{
			Requester:        "worker",
			RequesterAddress: "review:worker",
			Reviewer:         reviewer,
			// #626 B1: CommandApproverNode is the trusted field decisions are
			// actually validated against now; mirroring the plain Reviewer
			// label here keeps this test's existing approved/wrong-reviewer
			// intent intact (a decision claiming to be "orchestrator"
			// matches, "critic" does not).
			CommandApproverNode:    reviewer,
			CommandApproverAddress: "review:" + reviewer,
			Mode:                   "blocking",
			Label:                  "nix-build",
			Category:               "verification",
			CommandHash:            "sha256:test",
			Reason:                 "verify build",
			ExpiresAt:              expiresAt.Format(time.RFC3339Nano),
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
			Reviewer:         reviewer,
			ReviewerAddress:  "review:" + reviewer,
			RequesterAddress: "review:worker",
			Decision:         decision,
			Reason:           "reviewed",
		},
		journal.AppendOptions{ThreadID: threadID},
		now,
	)
	if err != nil {
		t.Fatalf("AppendEventWithOptions(decision): %v", err)
	}
}
