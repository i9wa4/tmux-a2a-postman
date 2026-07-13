package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestComputeAuditReviewProbability_ColdStartIsFullReview(t *testing.T) {
	got := ComputeAuditReviewProbability(0, 0, 0.05)
	if got != 1 {
		t.Fatalf("ComputeAuditReviewProbability(0, 0) = %v, want 1", got)
	}
}

func TestComputeAuditReviewProbability_UsesConfigurableNonzeroFloor(t *testing.T) {
	got := ComputeAuditReviewProbability(1_000_000, 0, 0.2)
	if got != 0.2 {
		t.Fatalf("ComputeAuditReviewProbability(perfect history, floor 0.2) = %v, want 0.2", got)
	}
	if got := NormalizeAuditReviewProbabilityFloor(0); got != DefaultAuditReviewProbability {
		t.Fatalf("NormalizeAuditReviewProbabilityFloor(0) = %v, want default %v", got, DefaultAuditReviewProbability)
	}
}

func TestBuildAuditDrawPayload_LinksCrossSessionOriginatingRequest(t *testing.T) {
	sessionDir := t.TempDir()
	sessionName := "main"
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", sessionName, 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	appendAuditMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "ireq_cross-request.md", auditProjectionContent("main:orchestrator", "review:worker", "ireq_cross-request.md", "required", "ireq_cross", "", "", "all"), now)
	appendAuditMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "ireq_cross-fill.md", auditProjectionContent("review:worker", "main:orchestrator", "ireq_cross-fill.md", "none", "", "ireq_cross", "", ""), now.Add(time.Second))

	draw, ok, err := BuildAuditDrawPayload(sessionDir, sessionName, VerdictEventPayload{
		SchemaVersion:    1,
		VerdictMessageID: "m3.md",
		Verdict:          "pass",
		VerdictOf:        "ireq_cross",
		Requester:        "main:orchestrator",
		Recipient:        "review:worker",
		RecordedAt:       now.Add(2 * time.Second).Format(time.RFC3339),
	}, now.Add(2*time.Second), 0.05)
	if err != nil {
		t.Fatalf("BuildAuditDrawPayload: %v", err)
	}
	if !ok {
		t.Fatal("BuildAuditDrawPayload ok = false, want cross-session request linked")
	}
	if draw.Identity != "review:worker" || draw.WorkClass != "all" || draw.AcceptedFillMessageID != "ireq_cross-fill.md" {
		t.Fatalf("draw = %#v, want linked cross-session review:worker/all/fill", draw)
	}
}

func TestBuildAuditDrawPayload_AuditCaughtFailureUsesMultiplier(t *testing.T) {
	sessionDir := t.TempDir()
	sessionName := "review"
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", sessionName, 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	appendAuditMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "r1.md", auditProjectionContent("orchestrator", "worker", "r1.md", "required", "ireq_one", "", "", "all"), now)
	appendAuditMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "f1.md", auditProjectionContent("worker", "orchestrator", "f1.md", "none", "", "ireq_one", "", ""), now.Add(time.Second))
	appendAuditEvent(t, writer, VerdictEventType, VerdictEventPayload{SchemaVersion: 1, VerdictMessageID: "v1.md", Verdict: "pass", VerdictOf: "ireq_one", Requester: "orchestrator", Recipient: "worker"}, now.Add(2*time.Second))
	appendAuditEvent(t, writer, AuditDrawEventType, AuditDrawPayload{SchemaVersion: 1, VerdictMessageID: "v1.md", VerdictOf: "ireq_one", Identity: "worker", WorkClass: "all", Sampled: true, AuditRequestID: "ireq_audit_one"}, now.Add(3*time.Second))
	appendAuditEvent(t, writer, VerdictEventType, VerdictEventPayload{SchemaVersion: 1, VerdictMessageID: "audit-v1.md", Verdict: "fail", VerdictOf: "ireq_audit_one", Requester: "critic", Recipient: "postman"}, now.Add(4*time.Second))
	appendAuditMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "r2.md", auditProjectionContent("orchestrator", "worker", "r2.md", "required", "ireq_two", "", "", "all"), now.Add(5*time.Second))
	appendAuditMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "f2.md", auditProjectionContent("worker", "orchestrator", "f2.md", "none", "", "ireq_two", "", ""), now.Add(6*time.Second))

	draw, ok, err := BuildAuditDrawPayload(sessionDir, sessionName, VerdictEventPayload{
		SchemaVersion:    1,
		VerdictMessageID: "v2.md",
		Verdict:          "pass",
		VerdictOf:        "ireq_two",
		Requester:        "orchestrator",
		Recipient:        "worker",
	}, now.Add(7*time.Second), 0.05)
	if err != nil {
		t.Fatalf("BuildAuditDrawPayload: %v", err)
	}
	if !ok {
		t.Fatal("BuildAuditDrawPayload ok = false, want true")
	}
	if draw.PassCount != 1 || draw.FailCount != DefaultAuditFailureMultiplier {
		t.Fatalf("draw counts = pass %d fail %d, want pass 1 fail %d", draw.PassCount, draw.FailCount, DefaultAuditFailureMultiplier)
	}
}

func appendAuditMailboxEvent(t *testing.T, writer *journal.Writer, eventType, messageID, content string, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: messageID,
		Content:   content,
	}, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}

func appendAuditEvent(t *testing.T, writer *journal.Writer, eventType string, payload interface{}, now time.Time) {
	t.Helper()
	if _, err := writer.AppendEvent(eventType, journal.VisibilityOperatorVisible, payload, now); err != nil {
		t.Fatalf("AppendEvent(%s): %v", eventType, err)
	}
}

func auditProjectionContent(from, to, messageID, replyPolicy, inputRequestID, fillsInputRequestID, verdict, completionRule string) string {
	body := "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n"
	if inputRequestID != "" {
		body += "  input_request_id: " + inputRequestID + "\n"
	}
	if fillsInputRequestID != "" {
		body += "  fills_input_request_id: " + fillsInputRequestID + "\n"
	}
	if verdict != "" {
		body += "  verdict: " + verdict + "\n  verdictOf: " + inputRequestID + "\n"
	}
	if completionRule != "" {
		body += "  completion_rule: " + completionRule + "\n"
	}
	return body + "---\n\nbody\n"
}
