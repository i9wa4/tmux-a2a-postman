package journal

import (
	"testing"
	"time"
)

// TestRecordMailboxPayloadIfAbsentUsingCurrentSessionWriter_FallsBackToShadowWriter
// pins the OpenCurrentWriter -> OpenShadowWriter fallback (guardian F-036):
// on a fresh session directory with no bootstrapped session/lease state,
// OpenCurrentWriter must fail and the function must fall back to
// OpenShadowWriter and still durably append the event, exactly like
// appendCommandEvent (internal/cli/execute_bash.go) already does for
// command-approval decision events.
func TestRecordMailboxPayloadIfAbsentUsingCurrentSessionWriter_FallsBackToShadowWriter(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.April, 14, 17, 0, 0, 0, time.UTC)

	if _, err := OpenCurrentWriter(sessionDir); err == nil {
		t.Fatal("OpenCurrentWriter() error = nil on a fresh session dir, want an error so the fallback path is actually exercised")
	}

	payload := MailboxEventPayload{
		MessageID:           "decision-1.md",
		From:                "approver",
		To:                  "worker",
		ThreadID:            "command-approval-abc123",
		FillsInputRequestID: "ireq_abc123",
		Content:             "---\nparams:\n  from: approver\n  to: worker\n---\n\n# Message\n",
	}
	equivalent := func(event Event) (bool, error) {
		return false, nil
	}
	appended, err := RecordMailboxPayloadIfAbsentUsingCurrentSessionWriter(sessionDir, "ctx-main", "main", "mailbox_projection_post_consumed", VisibilityMailboxProjection, payload, equivalent, now)
	if err != nil {
		t.Fatalf("RecordMailboxPayloadIfAbsentUsingCurrentSessionWriter() error = %v, want fallback to OpenShadowWriter to succeed", err)
	}
	if !appended {
		t.Fatal("RecordMailboxPayloadIfAbsentUsingCurrentSessionWriter() appended = false, want true on first call")
	}

	events, err := Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay() error = %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type == "mailbox_projection_post_consumed" && event.ThreadID == "command-approval-abc123" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("replay does not contain the mailbox_projection_post_consumed event written via the shadow-writer fallback: %#v", events)
	}
}
