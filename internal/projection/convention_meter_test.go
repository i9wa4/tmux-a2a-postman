package projection

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestProjectConventionMeterStateCountsPerNodeViolations(t *testing.T) {
	sessionDir := t.TempDir()
	contextID := "ctx"
	sessionName := "review"
	now := time.Date(2026, time.July, 13, 8, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, contextID, sessionName, 1, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter() error = %v", err)
	}

	appendConventionMessage(t, writer, "m1.md", "worker", "critic", map[string]string{"verdict": "pass"}, "looks good", now.Add(time.Second))
	appendConventionMessage(t, writer, "m2.md", "worker", "orchestrator", nil, "DONE: implemented\nRemaining blockers: none", now.Add(2*time.Second))
	appendConventionMessage(t, writer, "m3.md", "critic", "worker", map[string]string{"fills_input_request_id": "ireq_1"}, "ACK", now.Add(3*time.Second))
	appendConventionMessage(t, writer, "m4.md", "worker", "critic", map[string]string{"verdict": "pass", "verdictOf": "ireq_2", "replyTo": "request.md"}, "DONE\nTask artifact: .task-artifacts/x.md", now.Add(4*time.Second))

	projected, ok, err := ProjectConventionMeterState(sessionDir, sessionName)
	if err != nil {
		t.Fatalf("ProjectConventionMeterState() error = %v", err)
	}
	if !ok {
		t.Fatal("ProjectConventionMeterState() ok = false, want true")
	}

	worker := projected.Nodes["worker"]
	if worker.CheckedMessages != 3 || worker.ViolationCount != 2 {
		t.Fatalf("worker meter = %#v, want 3 checked / 2 violated", worker)
	}
	if worker.MissingVerdictOfCount != 1 || worker.MissingEvidenceCount != 1 || worker.MissingReplyReferenceCount != 1 {
		t.Fatalf("worker violation counts = %#v, want missing verdict/evidence/reply reference", worker)
	}
	if got, want := worker.ViolationRate, float64(2)/float64(3); got != want {
		t.Fatalf("worker violation rate = %v, want %v", got, want)
	}

	critic := projected.Nodes["critic"]
	if critic.CheckedMessages != 1 || critic.ViolationCount != 1 || critic.MissingReplyReferenceCount != 1 {
		t.Fatalf("critic meter = %#v, want one missing reply reference violation", critic)
	}
}

func appendConventionMessage(t *testing.T, writer *journal.Writer, messageID, from, to string, fields map[string]string, body string, at time.Time) {
	t.Helper()
	content := conventionMessageContent(from, to, messageID, fields, body)
	if _, err := writer.AppendEvent(MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: messageID,
		From:      from,
		To:        to,
		Path:      filepath.Join("inbox", to, messageID),
		Content:   content,
	}, at); err != nil {
		t.Fatalf("AppendEvent(delivered): %v", err)
	}
}

func conventionMessageContent(from, to, messageID string, fields map[string]string, body string) string {
	result := "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n"
	for key, value := range fields {
		result += "  " + key + ": " + value + "\n"
	}
	return result + "---\n\n" + body + "\n"
}
