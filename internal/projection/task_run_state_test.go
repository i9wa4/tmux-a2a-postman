package projection

import (
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func taskRunContent(from, to, messageID string, fields map[string]string, body string) string {
	content := "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n"
	for key, value := range fields {
		content += "  " + key + ": " + value + "\n"
	}
	return content + "---\n\n" + body + "\n"
}

func appendTaskRunMailboxEvent(t *testing.T, writer *journal.Writer, eventType, messageID, from, to, content string, now time.Time) {
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

func TestProjectTaskRunState_ActiveTaskWithOpenInputRequest(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	content := taskRunContent("orchestrator", "worker", "m1.md", map[string]string{
		"task_id":          "TASK-123",
		"run_id":           "run-1",
		"thread_id":        "thr-1",
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, "please work")
	appendTaskRunMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", content, now.Add(time.Second))

	projected, ok, err := ProjectTaskRunState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectTaskRunState: %v", err)
	}
	if !ok {
		t.Fatal("ProjectTaskRunState ok = false, want true")
	}
	if len(projected.Tasks) != 1 {
		t.Fatalf("tasks = %#v, want one", projected.Tasks)
	}
	task := projected.Tasks[0]
	if task.TaskID != "TASK-123" || task.RunID != "run-1" || task.OriginatingMessageID != "m1.md" || task.LatestMessageID != "m1.md" {
		t.Fatalf("unexpected task identity: %#v", task)
	}
	if task.ThreadID != "thr-1" || task.AssignedNode != "worker" || task.State != "waiting_input" {
		t.Fatalf("unexpected task state: %#v", task)
	}
	if len(task.OpenInputRequestIDs) != 1 || task.OpenInputRequestIDs[0] != "ireq_123" {
		t.Fatalf("open input requests = %#v", task.OpenInputRequestIDs)
	}
}

func TestProjectTaskRunState_TerminalReplyPresent(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	request := taskRunContent("orchestrator", "worker", "m1.md", map[string]string{
		"task_id":          "TASK-123",
		"thread_id":        "thr-1",
		"replyPolicy":      "required",
		"input_request_id": "ireq_123",
	}, "please work")
	reply := taskRunContent("worker", "orchestrator", "m2.md", map[string]string{
		"task_id":                "TASK-123",
		"thread_id":              "thr-1",
		"fills_input_request_id": "ireq_123",
	}, "done")
	appendTaskRunMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", request, now.Add(time.Second))
	appendTaskRunMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "worker", "orchestrator", reply, now.Add(2*time.Second))

	projected, ok, err := ProjectTaskRunState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectTaskRunState: %v", err)
	}
	if !ok || len(projected.Tasks) != 1 {
		t.Fatalf("ProjectTaskRunState = %#v ok=%v", projected, ok)
	}
	task := projected.Tasks[0]
	if task.State != "terminal" || task.TerminalMessageID != "m2.md" || len(task.OpenInputRequestIDs) != 0 {
		t.Fatalf("terminal task = %#v", task)
	}
}

func TestProjectTaskRunState_NoTaskMetadataProducesNoProjection(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	content := taskRunContent("orchestrator", "worker", "m1.md", nil, "plain")
	appendTaskRunMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", content, now.Add(time.Second))

	projected, ok, err := ProjectTaskRunState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectTaskRunState: %v", err)
	}
	if ok || len(projected.Tasks) != 0 {
		t.Fatalf("ProjectTaskRunState = %#v ok=%v, want no projection", projected, ok)
	}
}

func TestProjectTaskRunState_DuplicateExternalTaskReportsAmbiguous(t *testing.T) {
	sessionDir := t.TempDir()
	now := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	first := taskRunContent("orchestrator", "worker", "m1.md", map[string]string{
		"task_id":   "TASK-123",
		"thread_id": "thr-1",
	}, "first")
	second := taskRunContent("orchestrator", "critic", "m2.md", map[string]string{
		"task_id":   "TASK-123",
		"thread_id": "thr-2",
	}, "second")
	appendTaskRunMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m1.md", "orchestrator", "worker", first, now.Add(time.Second))
	appendTaskRunMailboxEvent(t, writer, MailboxProjectionDeliveredEventType, "m2.md", "orchestrator", "critic", second, now.Add(2*time.Second))

	projected, ok, err := ProjectTaskRunState(sessionDir, "review")
	if err != nil {
		t.Fatalf("ProjectTaskRunState: %v", err)
	}
	if !ok || len(projected.Tasks) != 1 {
		t.Fatalf("ProjectTaskRunState = %#v ok=%v", projected, ok)
	}
	task := projected.Tasks[0]
	if !task.Ambiguous || task.State != "ambiguous" || task.AmbiguityReason == "" {
		t.Fatalf("ambiguous task = %#v", task)
	}
}
