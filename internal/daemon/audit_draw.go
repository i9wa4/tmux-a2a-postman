package daemon

import (
	crypto_rand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/controlplane"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

var (
	auditReviewProbabilityFloor = projection.DefaultAuditReviewProbability
	auditTarget                 string
)

func recordVerdictEventForMailbox(sessionDir, sessionName, eventType string, payload journal.MailboxEventPayload) {
	if eventType != projection.MailboxProjectionPostConsumedEventType || payload.Content == "" {
		return
	}
	meta, err := envelope.ParseMetadata(payload.Content)
	if err != nil || meta.Verdict == "" || meta.VerdictOf == "" {
		return
	}
	if meta.MessageID == "" {
		meta.MessageID = payload.MessageID
	}
	now := time.Now()
	verdict := projection.VerdictEventPayload{
		SchemaVersion:    1,
		VerdictMessageID: meta.MessageID,
		Verdict:          meta.Verdict,
		VerdictOf:        meta.VerdictOf,
		Requester:        meta.From,
		Recipient:        meta.To,
		RecordedAt:       now.UTC().Format(time.RFC3339),
	}
	if err := journal.RecordProcessEvent(sessionDir, sessionName, projection.VerdictEventType, journal.VisibilityOperatorVisible, verdict, now); err != nil {
		log.Printf("postman: WARNING: component=audit_draw event=verdict_append_failed message_id=%s err=%v\n", meta.MessageID, err)
		return
	}
	recordAuditDrawForVerdictEvent(sessionDir, sessionName, verdict)
}

func recordAuditDrawForVerdictEvent(sessionDir, sessionName string, verdict projection.VerdictEventPayload) {
	if verdict.Verdict != "pass" {
		return
	}
	// Cooperative visibility boundary: the draw is not exposed through normal
	// mailbox APIs, but the local journal remains readable on a shared
	// filesystem until identity attestation and ACL work exists.
	now := time.Now()
	draw, ok, err := projection.BuildAuditDrawPayload(sessionDir, sessionName, verdict, now, auditReviewProbabilityFloor)
	if err != nil {
		log.Printf("postman: WARNING: component=audit_draw event=project_failed message_id=%s err=%v\n", verdict.VerdictMessageID, err)
		return
	}
	if !ok {
		return
	}
	draw.Sampled = sampleAuditDraw(draw.PReview)
	if draw.Sampled {
		messageID, inputRequestID, err := enqueueAuditReviewRequest(sessionDir, sessionName, draw, now)
		if err != nil {
			log.Printf("postman: WARNING: component=audit_draw event=audit_request_enqueue_failed message_id=%s err=%v\n", verdict.VerdictMessageID, err)
		} else {
			draw.AuditTarget = auditTarget
			draw.AuditMessageID = messageID
			draw.AuditRequestID = inputRequestID
		}
	}
	if err := journal.RecordProcessEvent(sessionDir, sessionName, projection.AuditDrawEventType, journal.VisibilityOperatorVisible, draw, now); err != nil {
		log.Printf("postman: WARNING: component=audit_draw event=append_failed message_id=%s err=%v\n", verdict.VerdictMessageID, err)
	}
}

func enqueueAuditReviewRequest(sessionDir, sessionName string, draw projection.AuditDrawPayload, now time.Time) (string, string, error) {
	target := strings.TrimSpace(auditTarget)
	if target == "" {
		return "", "", fmt.Errorf("audit_target is not configured")
	}
	inputRequestID, err := newAuditInputRequestID()
	if err != nil {
		return "", "", err
	}
	filename, err := message.GenerateFilename(now.UTC().Format("20060102-150405"), "postman", target, sessionName)
	if err != nil {
		return "", "", err
	}
	content := auditReviewRequestContent(filename, inputRequestID, target, draw, now)
	deliveryTarget := controlplane.TargetForNode(target, discovery.NodeInfo{
		SessionName: sessionName,
		SessionDir:  sessionDir,
	})
	result, err := message.DeliverSystemMessageDirectResultToTarget(filename, deliveryTarget, "postman", "", content, config.DefaultConfig(), nil, nil, nil)
	if err != nil {
		return "", "", fmt.Errorf("delivering audit request: %w", err)
	}
	if !result.Delivered {
		return "", "", fmt.Errorf("audit target inbox is full")
	}
	return filename, inputRequestID, nil
}

func newAuditInputRequestID() (string, error) {
	var raw [12]byte
	if _, err := crypto_rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("generating audit input request id: %w", err)
	}
	return "ireq_" + hex.EncodeToString(raw[:]), nil
}

func auditReviewRequestContent(messageID, inputRequestID, target string, draw projection.AuditDrawPayload, now time.Time) string {
	return fmt.Sprintf(`---
params:
  from: postman
  to: %s
  messageId: %s
  replyPolicy: required
  input_request_id: %s
  messageType: audit_review_request
  verdictOf: %s
  timestamp: %s
---

Audit the accepted fill below. Reply with verdict: pass or verdict: fail for this audit request.

Original verdict message: %s
Accepted request: %s
Audited identity: %s
Work class: %s

Acceptance criteria:
%s

Accepted fill:
%s
`, target, messageID, inputRequestID, draw.VerdictOf, now.UTC().Format(time.RFC3339), draw.VerdictMessageID, draw.RequestMessageID, draw.Identity, draw.WorkClass, draw.AcceptanceCriteria, draw.AcceptedFillContent)
}

func configureAuditDrawFromConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	auditReviewProbabilityFloor = projection.NormalizeAuditReviewProbabilityFloor(cfg.AuditReviewProbabilityFloor)
	auditTarget = auditTargetFromConfig(cfg)
}

func auditTargetFromConfig(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if target := strings.TrimSpace(cfg.AuditTarget); target != "" {
		return target
	}
	if target, ok := cfg.ResolveCommandApproverNode(); ok {
		return target
	}
	return ""
}

func sampleAuditDraw(probability float64) bool {
	if probability <= 0 {
		return false
	}
	if probability >= 1 {
		return true
	}
	var buf [8]byte
	if _, err := crypto_rand.Read(buf[:]); err != nil {
		log.Printf("postman: WARNING: component=audit_draw event=random_failed err=%v\n", err)
		return true
	}
	value := float64(binary.BigEndian.Uint64(buf[:])>>11) / (1 << 53)
	return value < math.Min(probability, 1)
}
