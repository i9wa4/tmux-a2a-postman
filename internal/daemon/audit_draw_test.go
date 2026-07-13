package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func TestRecordMailboxProjectionPayload_JournalsAuditDrawAfterPassVerdict(t *testing.T) {
	originalTarget := auditTarget
	auditTarget = ""
	t.Cleanup(func() { auditTarget = originalTarget })

	sessionDir := t.TempDir()
	sessionName := "review"
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	manager := journal.NewManager("ctx-main", 4242)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(sessionDir, sessionName, now); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	request := auditDrawTestContent("orchestrator", "worker", "m1.md", "required", "ireq_audit", "", "", "all")
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "m1.md",
		From:      "orchestrator",
		To:        "worker",
		Content:   request,
	})
	fill := auditDrawTestContent("worker", "orchestrator", "m2.md", "none", "", "ireq_audit", "", "")
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "m2.md",
		From:      "worker",
		To:        "orchestrator",
		Content:   fill,
	})
	verdict := auditDrawTestContent("orchestrator", "worker", "m3.md", "none", "", "", "pass", "")
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "m3.md",
		From:      "orchestrator",
		To:        "worker",
		Content:   verdict,
	})

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if len(events) < 1 {
		t.Fatal("Replay returned no events")
	}
	var sawVerdict bool
	last := events[len(events)-1]
	for _, event := range events {
		if event.Type == projection.VerdictEventType {
			sawVerdict = true
		}
	}
	if !sawVerdict {
		t.Fatal("missing verdict_event before audit_draw_event")
	}
	if last.Type != projection.AuditDrawEventType {
		t.Fatalf("last event type = %q, want %s", last.Type, projection.AuditDrawEventType)
	}
	var payload projection.AuditDrawPayload
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("Unmarshal audit draw payload: %v", err)
	}
	if payload.PReview != 1 || !payload.Sampled {
		t.Fatalf("payload PReview/Sampled = %v/%v, want cold-start p_review=1 and sampled", payload.PReview, payload.Sampled)
	}
	if payload.Identity != "worker" || payload.WorkClass != "all" || payload.VerdictOf != "ireq_audit" {
		t.Fatalf("payload = %#v, want worker/all/ireq_audit", payload)
	}
}

func TestRecordMailboxProjectionPayload_SampledAuditDrawEnqueuesReviewRequiredMail(t *testing.T) {
	originalTarget := auditTarget
	auditTarget = "critic"
	t.Cleanup(func() { auditTarget = originalTarget })

	sessionDir := t.TempDir()
	sessionName := "review"
	now := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
	manager := journal.NewManager("ctx-main", 4242)
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(sessionDir, sessionName, now); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	request := auditDrawTestContent("orchestrator", "worker", "m1.md", "required", "ireq_audit", "", "", "all")
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "m1.md",
		From:      "orchestrator",
		To:        "worker",
		Content:   request,
	})
	fill := auditDrawTestContent("worker", "orchestrator", "m2.md", "none", "", "ireq_audit", "", "")
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "m2.md",
		From:      "worker",
		To:        "orchestrator",
		Content:   fill,
	})
	verdict := auditDrawTestContent("orchestrator", "worker", "m3.md", "none", "", "", "pass", "")
	recordMailboxProjectionPayload(sessionDir, sessionName, projection.MailboxProjectionPostConsumedEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: "m3.md",
		From:      "orchestrator",
		To:        "worker",
		Content:   verdict,
	})

	entries, err := os.ReadDir(filepath.Join(sessionDir, "post"))
	if err != nil {
		t.Fatalf("ReadDir(post): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("post entries = %d, want sampled audit request", len(entries))
	}
	content, err := os.ReadFile(filepath.Join(sessionDir, "post", entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(audit post): %v", err)
	}
	for _, want := range []string{
		"to: critic",
		"replyPolicy: required",
		"input_request_id: ireq_",
		"messageType: audit_review_request",
		"Accepted fill:",
	} {
		if !strings.Contains(string(content), want) {
			t.Fatalf("audit request missing %q:\n%s", want, string(content))
		}
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	var draw projection.AuditDrawPayload
	for _, event := range events {
		if event.Type == projection.AuditDrawEventType {
			if err := json.Unmarshal(event.Payload, &draw); err != nil {
				t.Fatalf("Unmarshal audit draw: %v", err)
			}
		}
	}
	if draw.AuditTarget != "critic" || draw.AuditRequestID == "" || draw.AuditMessageID == "" {
		t.Fatalf("draw audit routing fields = %#v, want target/request/message", draw)
	}
}

func TestAuditTargetFromConfig_UsesExplicitTargetThenCommandApprover(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.AuditTarget = "guardian"
	cfg.CommandApproverNode = "critic"
	cfg.Nodes["critic"] = config.NodeConfig{Role: "critic"}
	if got := auditTargetFromConfig(cfg); got != "guardian" {
		t.Fatalf("auditTargetFromConfig(explicit) = %q, want guardian", got)
	}

	cfg.AuditTarget = ""
	if got := auditTargetFromConfig(cfg); got != "critic" {
		t.Fatalf("auditTargetFromConfig(command approver) = %q, want critic", got)
	}
}

func auditDrawTestContent(from, to, messageID, replyPolicy, inputRequestID, fillsInputRequestID, verdict, completionRule string) string {
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
		body += "  verdict: " + verdict + "\n  verdictOf: ireq_audit\n"
	}
	if completionRule != "" {
		body += "  completion_rule: " + completionRule + "\n"
	}
	return body + "---\n\nbody\n"
}
