package msgtrace

import "testing"

func TestLineIncludesStableLifecycleFields(t *testing.T) {
	got := Line("delivery_result", Fields{
		MessageID:             "20260625-from-worker-to-orchestrator.md",
		MessagePath:           "inbox/orchestrator/20260625-from-worker-to-orchestrator.md",
		Sender:                "worker",
		Recipient:             "orchestrator",
		ContextID:             "ctx-1",
		TmuxSession:           "tmux-a2a-postman",
		InputRequestID:        "ireq_123",
		ReplyTo:               "original.md",
		DeliveryAttempt:       1,
		DaemonSubmitRequestID: "20260625-r0001",
		DaemonSubmitCommand:   "send",
		SubmitPath:            "daemon-submit",
		Result:                "delivered",
		Reason:                "ok",
	})

	wantParts := []string{
		"component=message_lifecycle",
		"event=delivery_result",
		"message_id=20260625-from-worker-to-orchestrator.md",
		"message_path=inbox/orchestrator/20260625-from-worker-to-orchestrator.md",
		"sender=worker",
		"recipient=orchestrator",
		"context_id=ctx-1",
		"tmux_session=tmux-a2a-postman",
		"input_request_id=ireq_123",
		"reply_to=original.md",
		"delivery_attempt=1",
		"daemon_submit_request_id=20260625-r0001",
		"daemon_submit_command=send",
		"submit_path=daemon-submit",
		"result=delivered",
		"reason=ok",
	}
	for _, part := range wantParts {
		if !containsField(got, part) {
			t.Fatalf("Line() = %q, missing %q", got, part)
		}
	}
}

func TestFromContentUsesEnvelopeMetadata(t *testing.T) {
	content := `---
params:
  contextId: ctx-1
  messageId: envelope-id.md
  from: worker
  to: orchestrator
  input_request_id: ireq_123
  replyTo: original.md
---

# Message
`
	got := FromContent("filename.md", "read/filename.md", "session-1", content)
	if got.MessageID != "envelope-id.md" {
		t.Fatalf("MessageID = %q, want envelope metadata id", got.MessageID)
	}
	if got.Sender != "worker" || got.Recipient != "orchestrator" || got.ContextID != "ctx-1" {
		t.Fatalf("metadata fields not copied: %+v", got)
	}
	if got.InputRequestID != "ireq_123" || got.ReplyTo != "original.md" {
		t.Fatalf("correlation fields not copied: %+v", got)
	}
}

func TestLineQuotesWhitespaceValues(t *testing.T) {
	got := Line("projection_sync", Fields{Reason: "sync failed"})
	if !containsField(got, `reason="sync failed"`) {
		t.Fatalf("Line() = %q, want quoted reason", got)
	}
}

func containsField(line, field string) bool {
	for _, part := range splitFields(line) {
		if part == field {
			return true
		}
	}
	return false
}

func splitFields(line string) []string {
	fields := []string{}
	start := 0
	inQuote := false
	escaped := false
	for i, r := range line {
		switch {
		case escaped:
			escaped = false
		case r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case r == ' ' && !inQuote:
			if start < i {
				fields = append(fields, line[start:i])
			}
			start = i + 1
		}
	}
	if start < len(line) {
		fields = append(fields, line[start:])
	}
	return fields
}
