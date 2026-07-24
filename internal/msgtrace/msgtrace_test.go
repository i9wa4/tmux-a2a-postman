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
		CorrelationID:         "4d6a0f1e",
		TriggerFamily:         "runtime-auto",
		NodeKey:               "tmux-a2a-postman:worker",
		PaneID:                "%11",
		Runtime:               "codex",
		Trigger:               "codex:context-compaction",
		CaptureScope:          "history",
		MarkerCount:           1,
		MarkerPrefixLines:     1,
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
		"correlation_id=4d6a0f1e",
		"trigger_family=runtime-auto",
		"node_key=tmux-a2a-postman:worker",
		"pane_id=%11",
		"runtime=codex",
		"trigger=codex:context-compaction",
		"capture_scope=history",
		"marker_count=1",
		"marker_prefix_lines=1",
	}
	for _, part := range wantParts {
		if !containsField(got, part) {
			t.Fatalf("Line() = %q, missing %q", got, part)
		}
	}
}

func TestFromContentUsesEnvelopeMetadataWithoutPromotingTraceFields(t *testing.T) {
	content := `---
params:
  contextId: ctx-1
  messageId: envelope-id.md
  from: postman
  to: orchestrator
  messageType: ping
  input_request_id: ireq_123
  replyTo: original.md
  correlation_id: 0123456789abcdef0123456789abcdef
  trigger_family: manual-tui
---

# Message
`
	got := FromContent("filename.md", "read/filename.md", "session-1", content)
	if got.MessageID != "envelope-id.md" {
		t.Fatalf("MessageID = %q, want envelope metadata id", got.MessageID)
	}
	if got.Sender != "postman" || got.Recipient != "orchestrator" || got.ContextID != "ctx-1" {
		t.Fatalf("metadata fields not copied: %+v", got)
	}
	if got.InputRequestID != "ireq_123" || got.ReplyTo != "original.md" {
		t.Fatalf("correlation fields not copied: %+v", got)
	}
	if got.CorrelationID != "" || got.TriggerFamily != "" {
		t.Fatalf("generic parser promoted trace metadata: %+v", got)
	}
}

func TestFromTrustedDaemonPingContentPromotesValidatedTraceMetadata(t *testing.T) {
	content := `---
params:
  contextId: ctx-1
  messageId: envelope-id.md
  from: postman
  to: orchestrator
  messageType: ping
  correlation_id: 0123456789abcdef0123456789abcdef
  trigger_family: manual-tui
---

# Message
`
	got := FromTrustedDaemonPingContent("filename.md", "read/filename.md", "session-1", content)
	if got.CorrelationID != "0123456789abcdef0123456789abcdef" || got.TriggerFamily != "manual-tui" {
		t.Fatalf("trusted daemon PING trace metadata not copied: %+v", got)
	}
}

func TestFromTrustedDaemonPingContentRejectsInvalidTraceMetadata(t *testing.T) {
	content := `---
params:
  contextId: ctx-1
  messageId: envelope-id.md
  from: postman
  to: orchestrator
  messageType: ping
  correlation_id: INVALID
  trigger_family: manual-tui
---

# Message
`
	got := FromTrustedDaemonPingContent("filename.md", "read/filename.md", "session-1", content)
	if got.CorrelationID != "" || got.TriggerFamily != "" {
		t.Fatalf("trusted parser copied invalid trace fields: %+v", got)
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
