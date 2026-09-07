package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/message"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/runtimeprofile"
)

func verdictGateSendContent(from, to, messageID, replyPolicy, inputRequestID string) string {
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		"  input_request_id: " + inputRequestID + "\n" +
		"---\n\nplease work\n"
}

func verdictGateSendContentWithVerdict(from, to, messageID, replyPolicy, inputRequestID, verdict, verdictOf string) string {
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		"  input_request_id: " + inputRequestID + "\n" +
		"  verdict: " + verdict + "\n" +
		"  verdictOf: " + verdictOf + "\n" +
		"---\n\nplease work\n"
}

func verdictGateFillContent(from, to, messageID, replyPolicy, inputRequestID, fillsInputRequestID string) string {
	inputLine := ""
	if inputRequestID != "" {
		inputLine = "  input_request_id: " + inputRequestID + "\n"
	}
	fillLine := ""
	if fillsInputRequestID != "" {
		fillLine = "  fills_input_request_id: " + fillsInputRequestID + "\n"
	}
	return "---\nparams:\n" +
		"  from: " + from + "\n" +
		"  to: " + to + "\n" +
		"  messageId: " + messageID + "\n" +
		"  replyPolicy: " + replyPolicy + "\n" +
		inputLine +
		fillLine +
		"---\n\nbody\n"
}

func appendVerdictGateFill(t *testing.T, sessionDir, sessionName, requester, filler, inputRequestID string, now time.Time) {
	t.Helper()
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", sessionName, 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	appendVerdictGateFillEvent(t, writer, requester, filler, inputRequestID, now)
}

func appendVerdictGateFillEvent(t *testing.T, writer *journal.Writer, requester, filler, inputRequestID string, now time.Time) {
	t.Helper()
	requestMessageID := inputRequestID + "-request.md"
	requestContent := verdictGateFillContent(requester, filler, requestMessageID, "required", inputRequestID, "")
	if _, err := writer.AppendEvent(projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: requestMessageID,
		From:      requester,
		To:        filler,
		Content:   requestContent,
	}, now); err != nil {
		t.Fatalf("AppendEvent request: %v", err)
	}
	fillMessageID := inputRequestID + "-fill.md"
	fillContent := verdictGateFillContent(filler, requester, fillMessageID, "none", "", inputRequestID)
	if _, err := writer.AppendEvent(projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: fillMessageID,
		From:      filler,
		To:        requester,
		Content:   fillContent,
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("AppendEvent fill: %v", err)
	}
}

func decodeMailboxEventPayloadForTest(t *testing.T, raw json.RawMessage) (journal.MailboxEventPayload, bool) {
	t.Helper()
	var payload journal.MailboxEventPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return journal.MailboxEventPayload{}, false
	}
	return payload, true
}

func TestProcessDaemonSubmitRequest_SendWritesPostFile(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-send",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  "20260414-033100-from-orchestrator-to-worker.md",
		Content:   "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:31:00Z\n---\n\nsubmit payload\n",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	postPath := filepath.Join(sessionDir, "post", "20260414-033100-from-orchestrator-to-worker.md")
	got, err := os.ReadFile(postPath)
	if err != nil {
		t.Fatalf("ReadFile postPath: %v", err)
	}
	if string(got) != "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:31:00Z\n---\n\nsubmit payload\n" {
		t.Fatalf("post payload changed:\n got %q", string(got))
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-send"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != "20260414-033100-from-orchestrator-to-worker.md" {
		t.Fatalf("response.Filename = %q", response.Filename)
	}
}

func TestProcessDaemonSubmitRequest_SendRefusesReplyRequiredWhenVerdictGraceExpired(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_expired", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-expired",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  "20260713-120000-from-orchestrator-to-worker.md",
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("orchestrator", "worker", "20260713-120000-from-orchestrator-to-worker.md", "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-expired"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "past verdict_grace_seconds=60") {
		t.Fatalf("response.Error = %q, want grace-window verdict gate rejection", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", "20260713-120000-from-orchestrator-to-worker.md")); !os.IsNotExist(err) {
		t.Fatalf("post file written despite verdict gate rejection: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_ValidateSendRefusesBeforePostWrite(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_validate_expired", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120002-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-validate-verdict-expired",
		Command:   projection.DaemonSubmitValidateSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-validate-verdict-expired"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "past verdict_grace_seconds=60") {
		t.Fatalf("response.Error = %q, want grace-window verdict gate rejection", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file written despite validate-send rejection: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_SendRefusesReplyRequiredWhenVerdictDebtExceedsCap(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-10 * time.Second).UTC()
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review-session", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter: %v", err)
	}
	for i := 0; i < 4; i++ {
		appendVerdictGateFillEvent(t, writer, "orchestrator", "worker", "ireq_debt_"+strconv.Itoa(i), now.Add(time.Duration(i)*time.Second))
	}

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 3600
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-debt",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  "20260713-120001-from-orchestrator-to-worker.md",
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("orchestrator", "worker", "20260713-120001-from-orchestrator-to-worker.md", "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-debt"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "verdict debt 4 above verdict_debt_cap=3") {
		t.Fatalf("response.Error = %q, want debt-cap verdict gate rejection", response.Error)
	}
}

func TestProcessDaemonSubmitRequest_AllowsReplyRequiredWithPiggybackVerdict(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_piggyback", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 0
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120010-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-piggyback",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContentWithVerdict("orchestrator", "worker", filename, "required", "ireq_new", "pass", "ireq_piggyback"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-piggyback"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q, want piggyback verdict to satisfy gate", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); err != nil {
		t.Fatalf("post file missing for piggyback verdict send: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_RejectsWrongRecipientPiggybackVerdict(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_wrong_to", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 0
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120014-from-orchestrator-to-critic.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-piggyback-wrong-to",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContentWithVerdict("orchestrator", "critic", filename, "required", "ireq_new", "pass", "ireq_wrong_to"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-piggyback-wrong-to"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "past verdict_grace_seconds=60") {
		t.Fatalf("response.Error = %q, want wrong-recipient piggyback verdict to leave expired debt", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file written despite wrong-recipient piggyback rejection: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_SendExemptsMessengerFromVerdictGate(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "messenger", "worker", "ireq_messenger", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 0
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120002-from-messenger-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-messenger",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "messenger",
		Content:   verdictGateSendContent("messenger", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-messenger"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q, want messenger exemption", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); err != nil {
		t.Fatalf("post file missing for exempt messenger send: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_SendExemptsConfiguredUINodeFromVerdictGate(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "human", "worker", "ireq_ui_node", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	originalUINode := verdictExemptUINode
	verdictGraceSeconds = 60
	verdictDebtCap = 0
	verdictExemptUINode = "human"
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
		verdictExemptUINode = originalUINode
	})

	filename := "20260713-120011-from-human-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-ui-node",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "human",
		Content:   verdictGateSendContent("human", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-ui-node"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q, want configured UI node exemption", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); err != nil {
		t.Fatalf("post file missing for exempt UI node send: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_VerdictGateRejectsEnvelopeSenderSpoof(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_expired", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120003-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-spoof",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("messenger", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-spoof"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "daemon-submit sender \"orchestrator\" does not match envelope sender \"messenger\"") {
		t.Fatalf("response.Error = %q, want sender mismatch rejection", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file written despite sender mismatch gate rejection: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_VerdictGateFailsClosedWithoutAuthoritativeSender(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	filename := "20260713-120007-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-no-sender",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Content:   verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-no-sender"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "without authoritative daemon-submit sender") {
		t.Fatalf("response.Error = %q, want fail-closed sender rejection", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file written despite missing sender gate rejection: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_SendRejectsMalformedFilenameWithSender(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	filename := "not-a-message-name.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-malformed-filename",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("orchestrator", "worker", filename, "none", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-malformed-filename"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "daemon submit send invalid filename") {
		t.Fatalf("response.Error = %q, want malformed filename rejection", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file written despite malformed filename rejection: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_VerdictGateNormalizesSameSessionSender(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_same_session", now)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120003-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-same-session",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "review-session:orchestrator",
		Content:   verdictGateSendContent("review-session:orchestrator", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-same-session"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "requester \"orchestrator\"") {
		t.Fatalf("response.Error = %q, want normalized requester debt rejection", response.Error)
	}
}

func TestProcessDaemonSubmitRequest_RecordsVerdictNoneTimeout(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_timeout", now)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120004-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-none",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	var found bool
	for _, event := range events {
		if event.Type != projection.VerdictNoneTimeoutEventType {
			continue
		}
		payload, ok := decodeMailboxEventPayloadForTest(t, event.Payload)
		if !ok {
			t.Fatal("verdict none timeout payload did not decode")
		}
		if !strings.Contains(payload.Content, "verdict: none") || !strings.Contains(payload.Content, "verdictOf: ireq_timeout") {
			t.Fatalf("timeout content = %q, want verdict:none for ireq_timeout", payload.Content)
		}
		found = true
	}
	if !found {
		t.Fatal("missing verdict none timeout journal event")
	}

	state, ok, err := projection.ProjectVerdictDebtState(sessionDir, "review-session", time.Now(), 60)
	if err != nil {
		t.Fatalf("ProjectVerdictDebtState: %v", err)
	}
	if !ok {
		t.Fatal("ProjectVerdictDebtState ok = false, want true")
	}
	if got := state.Requesters["orchestrator"].UnstampedCount; got != 0 {
		t.Fatalf("unstamped count after durable verdict:none = %d, want 0", got)
	}
}

func TestEnforceVerdictGate_DedupesConcurrentSameRequesterTimeout(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_timeout_concurrent", now)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120006-from-orchestrator-to-worker.md"
	content := verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new")
	errCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- enforceVerdictGate(sessionDir, "orchestrator", filename, content)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil && !strings.Contains(err.Error(), "past verdict_grace_seconds=60") {
			t.Fatalf("enforceVerdictGate error = %v, want only verdict gate rejection or already-materialized timeout", err)
		}
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	count := 0
	for _, event := range events {
		if event.Type != projection.VerdictNoneTimeoutEventType {
			continue
		}
		payload, ok := decodeMailboxEventPayloadForTest(t, event.Payload)
		if !ok {
			t.Fatal("verdict none timeout payload did not decode")
		}
		if strings.Contains(payload.Content, "verdictOf: ireq_timeout_concurrent") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("timeout event count = %d, want 1", count)
	}
}

func TestEnforceVerdictGate_TimeoutDedupeIgnoresPriorGeneration(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	writer, err := journal.OpenShadowWriter(sessionDir, "ctx-main", "review-session", 101, now)
	if err != nil {
		t.Fatalf("OpenShadowWriter generation 1: %v", err)
	}
	oldTimeoutContent := "---\nparams:\n" +
		"  from: orchestrator\n" +
		"  to: worker\n" +
		"  messageId: old-timeout.md\n" +
		"  verdict: none\n" +
		"  verdictOf: ireq_generation\n" +
		"---\n\nold generation timeout\n"
	if _, err := writer.AppendEvent(projection.VerdictNoneTimeoutEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: "old-timeout.md",
		From:      "orchestrator",
		To:        "worker",
		Content:   oldTimeoutContent,
	}, now); err != nil {
		t.Fatalf("AppendEvent old timeout: %v", err)
	}
	if _, _, err := journal.ResolveSession(sessionDir, "review-session", journal.ResolutionExplicitNewSession, now.Add(time.Minute)); err != nil {
		t.Fatalf("ResolveSession explicit new session: %v", err)
	}
	writer, err = journal.OpenShadowWriter(sessionDir, "ctx-main", "review-session", 101, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("OpenShadowWriter generation 2: %v", err)
	}
	appendVerdictGateFillEvent(t, writer, "orchestrator", "worker", "ireq_generation", now.Add(2*time.Minute))
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120012-from-orchestrator-to-worker.md"
	err = enforceVerdictGate(sessionDir, "orchestrator", filename, verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new"))
	if err == nil || !strings.Contains(err.Error(), "past verdict_grace_seconds=60") {
		t.Fatalf("enforceVerdictGate error = %v, want verdict gate rejection", err)
	}

	sessionKey, generation, ok := projection.CurrentSessionIdentity(sessionDir)
	if !ok {
		t.Fatal("CurrentSessionIdentity ok = false, want true")
	}
	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	currentCount := 0
	totalCount := 0
	for _, event := range events {
		if event.Type != projection.VerdictNoneTimeoutEventType {
			continue
		}
		payload, ok := decodeMailboxEventPayloadForTest(t, event.Payload)
		if !ok {
			t.Fatal("verdict none timeout payload did not decode")
		}
		if !strings.Contains(payload.Content, "verdictOf: ireq_generation") {
			continue
		}
		totalCount++
		if event.SessionKey == sessionKey && event.Generation == generation {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Fatalf("current generation timeout count = %d, want 1", currentCount)
	}
	if totalCount != 2 {
		t.Fatalf("total timeout count = %d, want old plus current timeout", totalCount)
	}
}

func TestProcessDaemonSubmitRequest_ReturnsErrorWhenVerdictNoneTimeoutAppendFails(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-2 * time.Hour).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_timeout_append", now)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	if _, err := journal.OpenShadowWriter(sessionDir, "ctx-other", "review-session", os.Getpid(), time.Now()); err != nil {
		t.Fatalf("OpenShadowWriter stealing lease: %v", err)
	}

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = 60
	verdictDebtCap = 3
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})

	filename := "20260713-120005-from-orchestrator-to-worker.md"
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-verdict-none-append-fails",
		Command:   projection.DaemonSubmitSend,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Filename:  filename,
		Sender:    "orchestrator",
		Content:   verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new"),
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-verdict-none-append-fails"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if !strings.Contains(response.Error, "recording verdict:none timeout") || !strings.Contains(response.Error, "lease mismatch") {
		t.Fatalf("response.Error = %q, want propagated verdict:none append failure", response.Error)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "post", filename)); !os.IsNotExist(err) {
		t.Fatalf("post file written despite verdict:none append failure: %v", err)
	}
}

func TestConfigureVerdictGateFromConfig_AllowsZeroVerdictDebtCap(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "postman.toml")
	configContent := `[postman]
edges = ["orchestrator --- worker"]
verdict_debt_cap = 0

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	originalCap := verdictDebtCap
	verdictDebtCap = defaultVerdictDebtCap
	t.Cleanup(func() {
		verdictDebtCap = originalCap
	})

	configureVerdictGateFromConfig(cfg)

	if verdictDebtCap != 0 {
		t.Fatalf("verdictDebtCap = %d, want config value 0", verdictDebtCap)
	}
}

func TestConfigureVerdictGateFromConfig_NegativeVerdictDebtCapDisablesCap(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "postman.toml")
	configContent := `[postman]
edges = ["orchestrator --- worker"]
verdict_debt_cap = -1

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	originalCap := verdictDebtCap
	verdictDebtCap = defaultVerdictDebtCap
	t.Cleanup(func() {
		verdictDebtCap = originalCap
	})

	configureVerdictGateFromConfig(cfg)

	if verdictDebtCap != -1 {
		t.Fatalf("verdictDebtCap = %d, want explicit negative config to disable cap", verdictDebtCap)
	}
}

func TestConfigureVerdictGateFromConfig_ExplicitZeroGraceExpiresImmediately(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Now().Add(-time.Second).UTC()
	appendVerdictGateFill(t, sessionDir, "review-session", "orchestrator", "worker", "ireq_zero_grace", now)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	configPath := filepath.Join(t.TempDir(), "postman.toml")
	configContent := `[postman]
edges = ["orchestrator --- worker"]
verdict_grace_seconds = 0
verdict_debt_cap = 3

[orchestrator]
role = "orchestrator"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	originalGrace := verdictGraceSeconds
	originalCap := verdictDebtCap
	verdictGraceSeconds = defaultVerdictGraceSeconds
	verdictDebtCap = defaultVerdictDebtCap
	t.Cleanup(func() {
		verdictGraceSeconds = originalGrace
		verdictDebtCap = originalCap
	})
	configureVerdictGateFromConfig(cfg)
	if verdictGraceSeconds != 0 {
		t.Fatalf("verdictGraceSeconds = %d, want explicit config value 0", verdictGraceSeconds)
	}

	filename := "20260713-120013-from-orchestrator-to-worker.md"
	err = enforceVerdictGate(sessionDir, "orchestrator", filename, verdictGateSendContent("orchestrator", "worker", filename, "required", "ireq_new"))
	if err == nil || !strings.Contains(err.Error(), "past verdict_grace_seconds=0") {
		t.Fatalf("enforceVerdictGate error = %v, want immediate zero-grace rejection", err)
	}
}

func TestProcessDaemonSubmitRequest_PopArchivesUnreadMessage(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033200-from-orchestrator-to-worker.md"
	newest := "20260414-033201-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\noldest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:01Z\n---\n\nnewest\n"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("archived read file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, oldest)); !os.IsNotExist(err) {
		t.Fatalf("oldest inbox file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file missing: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != oldest {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, oldest)
	}
	if response.UnreadBefore != 2 {
		t.Fatalf("response.UnreadBefore = %d, want 2", response.UnreadBefore)
	}
}

// TestHandleDaemonSubmitPop_RollsBackToInboxWhenArchiveReadinessCheckFails is
// the regression test for #755's producer-side ordering requirement,
// including guardian's rework-1 correction: validate the just-archived file
// before ever constructing a response that contains MarkdownPath, AND roll
// the archive back to inbox on failure so a retry genuinely re-discovers and
// re-verifies the message. Without the rollback, ArchiveInboxMessage has
// already moved the file out of inbox by the time verification runs, and
// nothing scans read/ for undelivered mail -- so a verification failure
// would otherwise leave the message permanently unreachable, silently
// removing it from the unread set. The underlying rename is a same-host,
// same-filesystem atomic operation not practical to race from a test, so
// this injects the failure via daemonPopArchiveVerify's test seam instead of
// trying to reproduce real filesystem timing.
func TestHandleDaemonSubmitPop_RollsBackToInboxWhenArchiveReadinessCheckFails(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	inboxPath := filepath.Join(inboxDir, filename)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	injectedErr := errors.New("simulated readiness failure")
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return injectedErr
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	response, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-block",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err == nil {
		t.Fatalf("handleDaemonSubmitPop() error = nil, want a readiness failure")
	}
	if !errors.Is(err, injectedErr) {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want it to wrap %v", err, injectedErr)
	}
	if response.MarkdownPath != "" || response.Filename != "" || response.Content != "" {
		t.Fatalf("handleDaemonSubmitPop() response = %+v, want a zero-value response: publication must be blocked, not merely flagged", response)
	}

	// The rollback is the point of this test: the message must be back in
	// inbox (not stranded in read/) so a retry pop can find it at all. The
	// rollback writes the known-good in-memory content back to inbox rather
	// than renaming read/<filename>, so read/<filename> is left as-is (see
	// TestHandleDaemonSubmitPop_DedupBranchRollbackPreservesBothCopies for
	// why that matters); asserting its continued presence here, not its
	// absence.
	if got, readErr := os.ReadFile(inboxPath); readErr != nil || string(got) != content {
		t.Fatalf("message not rolled back to inbox: content = %q, err = %v, want %q, nil", got, readErr, content)
	}
	if got, readErr := os.ReadFile(filepath.Join(sessionDir, "read", filename)); readErr != nil || string(got) != content {
		t.Fatalf("archived read file missing or changed after rollback: content = %q, err = %v, want %q, nil", got, readErr, content)
	}
}

// TestHandleDaemonSubmitPop_RollbackCallSiteConsultsWriteFileAtomicOpsSeam is
// the regression test for guardian's B1 reopen: critic reran the exact F-014
// mutation this file's own atomicity tests were meant to catch
// (writeFileAtomic's call site in handleDaemonSubmitPop's rollback path
// silently replaced with a direct os.WriteFile) and both packages still
// passed. Root cause: TestWriteFileAtomicWriteFailure/RenameFailure... call
// writeFileAtomic directly, proving the HELPER is atomic, but nothing pinned
// that the ROLLBACK CALL SITE actually reaches that helper rather than
// bypassing it entirely. This test drives handleDaemonSubmitPop itself into
// the rollback path (via the existing daemonPopArchiveVerify failure seam)
// AND injects a writeFileAtomicOpsVar.writeFile failure, then asserts the
// returned error wraps that injected failure. If the call site were mutated
// to a direct os.WriteFile bypassing the seam, this injected failure would
// never fire (the real filesystem write would simply succeed) and this
// assertion would fail -- unlike the pre-existing unit tests, which call
// writeFileAtomic directly and therefore can't observe a call-site bypass at
// all.
func TestHandleDaemonSubmitPop_RollbackCallSiteConsultsWriteFileAtomicOpsSeam(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	originalOps := writeFileAtomicOpsVar
	injectedOpsErr := errors.New("simulated rollback write-ops failure")
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll: os.MkdirAll,
		writeFile: func(name string, data []byte, perm os.FileMode) error {
			return injectedOpsErr
		},
		rename: os.Rename,
	}
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })

	_, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-rollback-seam",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err == nil {
		t.Fatal("handleDaemonSubmitPop() error = nil, want a rollback-write failure")
	}
	if !errors.Is(err, injectedOpsErr) {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want it to wrap the injected writeFileAtomicOpsVar failure %v -- this proves the rollback call site actually consults the seam rather than bypassing it with a direct os.WriteFile", err, injectedOpsErr)
	}
}

// TestWriteFileAtomicWriteFailureLeavesDestinationUntouched is the
// regression test for F-014's mutation-testing gap: guardian found that
// reverting writeFileAtomic to a plain os.WriteFile(path, data, 0o600) left
// every existing test still passing, because none of them exercised the
// atomicity property specifically -- only that the function writes correct
// bytes on success or reports an error on rename failure, not that a
// failure partway through never leaves `path` holding partial content. This
// calls the real, unmodified writeFileAtomic function and injects a
// write-step failure via the package-level writeFileAtomicOpsVar seam
// (simulating a write interrupted before completion, which the real
// implementation directs at tmpPath, never at path itself), then asserts
// path is left exactly as it was beforehand -- never modified at all, let
// alone partially. Swapping the package variable rather than taking a
// parallel ops-parameterized function is deliberate: it means a mutation
// can't defeat this test simply by reverting writeFileAtomic's one-line
// call sites while leaving a separate "WithOps" test-only variant intact.
//
// MAJ-4 fix: the injected writeFile mock ACTUALLY WRITES partial bytes to
// whatever path it is given, then returns the error, rather than a pure
// no-op that returns only an error. A pure no-op mock cannot distinguish
// atomic from non-atomic behavior at all -- it never touches the
// filesystem, so `path` would come back unchanged whether writeFileAtomic
// correctly directs the write at a temp file (atomic) or was mutated to
// write directly at `path` (not atomic), because in the mutated case the
// mock's no-op still never reaches the real filesystem before "failing".
// With a mock that genuinely writes to whatever name it's called with, an
// atomic implementation's partial write lands on tmpPath (path is
// untouched, as asserted below); a mutated non-atomic implementation that
// passes `path` directly to this seam would have its partial write land
// on `path` itself, and this assertion would correctly fail.
func TestWriteFileAtomicWriteFailureLeavesDestinationUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "message.md")
	original := "original-content"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile(original): %v", err)
	}
	writeErr := errors.New("simulated interrupted write")

	originalOps := writeFileAtomicOpsVar
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll: os.MkdirAll,
		writeFile: func(name string, data []byte, perm os.FileMode) error {
			// C13: pin that the temp file lives in the SAME directory as
			// its destination -- a temp path under a different directory
			// (e.g. os.TempDir()) could be on a different filesystem,
			// which would make the subsequent os.Rename fail with EXDEV
			// and turn every rollback into the data-loss path this whole
			// fix exists to prevent.
			if filepath.Dir(name) != filepath.Dir(path) {
				t.Fatalf("writeFile seam received name %q in a different directory than destination %q", name, path)
			}
			// Simulate a real interrupted write: some bytes genuinely land
			// on disk at whatever path this seam was given, before the
			// call reports failure.
			_ = os.WriteFile(name, []byte("PARTIAL"), perm)
			return writeErr
		},
		rename: os.Rename,
	}

	err := writeFileAtomic(path, []byte("new-content"))
	if !errors.Is(err, writeErr) {
		t.Fatalf("writeFileAtomic() error = %v, want %v", err, writeErr)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != original {
		t.Fatalf("destination changed after a failed write step: content = %q, err = %v, want %q, nil", got, readErr, original)
	}
}

// TestWriteFileAtomicRenameFailureLeavesDestinationUntouched is the
// companion to the write-failure test above: it calls the real
// writeFileAtomic and injects a rename-step failure (the temp file is
// genuinely written with the new content first, via the real
// os.WriteFile, then the rename into place fails), then asserts the
// destination is left exactly as it was beforehand -- proving a failed
// rename cannot leave `path` half-written either, only either fully
// replaced (on success) or fully untouched (on any failure).
func TestWriteFileAtomicRenameFailureLeavesDestinationUntouched(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "message.md")
	original := "original-content"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatalf("WriteFile(original): %v", err)
	}
	renameErr := errors.New("simulated rename failure")

	originalOps := writeFileAtomicOpsVar
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll:  os.MkdirAll,
		writeFile: os.WriteFile,
		rename: func(oldPath, newPath string) error {
			return renameErr
		},
	}

	err := writeFileAtomic(path, []byte("new-content"))
	if !errors.Is(err, renameErr) {
		t.Fatalf("writeFileAtomic() error = %v, want %v", err, renameErr)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != original {
		t.Fatalf("destination changed after a failed rename step: content = %q, err = %v, want %q, nil", got, readErr, original)
	}
}

// TestWriteFileAtomicCreatesDestinationDirectoryWhenMissing is the
// regression test for Blocker 3: on handleDaemonSubmitPop's dedup rollback
// path, ArchiveInboxMessage's dedup branch removes the sole file in
// inbox/<node>/, and internal/projection's removeEmptyDirs (called during
// projection sync) can prune that now-empty directory before the rollback
// write runs -- so the destination directory can genuinely be gone, not
// just the destination file. Without MkdirAll, that rollback write fails
// ENOENT and the only complete copy of the message is silently discarded --
// the exact "message permanently lost" consequence class this whole fix
// exists to prevent. This asserts writeFileAtomic succeeds and recreates
// the directory when the destination's parent doesn't exist at all.
func TestWriteFileAtomicCreatesDestinationDirectoryWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	// inboxDir is never created -- simulates it having been pruned by
	// removeEmptyDirs after the dedup branch removed its last file.
	inboxDir := filepath.Join(tmpDir, "inbox", "worker")
	path := filepath.Join(inboxDir, "message.md")
	content := "restored-content"

	if err := writeFileAtomic(path, []byte(content)); err != nil {
		t.Fatalf("writeFileAtomic() error = %v, want success with directory auto-created", err)
	}
	if got, readErr := os.ReadFile(path); readErr != nil || string(got) != content {
		t.Fatalf("ReadFile() = %q, %v, want %q, nil", got, readErr, content)
	}
}

// TestHandleDaemonSubmitPop_DedupBranchRollbackPreservesBothCopies is the
// regression test for F-012: ArchiveInboxMessage's dedup branch (taken when
// read/<filename> already exists) does not rename anything -- it only
// removes the inbox copy. A rollback that assumes a rename happened (i.e.
// renames read/<filename> back to inbox) is wrong on this branch: it would
// both erase whatever was legitimately archived at read/<filename> and, if
// the two copies genuinely differ (exactly what triggers this readiness
// failure), replace the fresh inbox content with the stale/differing
// archived bytes instead of preserving it. This test seeds a pre-existing
// read/<filename> with DIFFERENT content than the fresh inbox copy, uses
// the real (non-mocked) daemonPopArchiveVerify so the mismatch is
// genuinely detected, and asserts both copies survive unchanged: the fresh
// content back in inbox (available for a future genuine archive attempt),
// and the pre-existing archive record untouched in read/.
func TestHandleDaemonSubmitPop_DedupBranchRollbackPreservesBothCopies(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	readDir := filepath.Join(sessionDir, "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	freshContent := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\nfresh-inbox-content\n"
	archivedContent := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\nalready-archived-content\n"
	inboxPath := filepath.Join(inboxDir, filename)
	readPath := filepath.Join(readDir, filename)
	if err := os.WriteFile(inboxPath, []byte(freshContent), 0o600); err != nil {
		t.Fatalf("WriteFile(inbox): %v", err)
	}
	if err := os.WriteFile(readPath, []byte(archivedContent), 0o600); err != nil {
		t.Fatalf("WriteFile(read): %v", err)
	}

	// Uses the real daemonPopArchiveVerify (no test-seam override): the
	// dedup branch leaves readPath's pre-existing content untouched, so
	// comparing it against the freshly read inbox content genuinely
	// mismatches without any injected failure.
	response, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-dedup",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err == nil {
		t.Fatalf("handleDaemonSubmitPop() error = nil, want a content-mismatch readiness failure")
	}
	if !strings.Contains(err.Error(), "content mismatch") {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want it to mention content mismatch", err)
	}
	if response.MarkdownPath != "" || response.Filename != "" || response.Content != "" {
		t.Fatalf("handleDaemonSubmitPop() response = %+v, want a zero-value response", response)
	}

	if got, readErr := os.ReadFile(inboxPath); readErr != nil || string(got) != freshContent {
		t.Fatalf("fresh inbox content not preserved: content = %q, err = %v, want %q, nil", got, readErr, freshContent)
	}
	if got, readErr := os.ReadFile(readPath); readErr != nil || string(got) != archivedContent {
		t.Fatalf("pre-existing archive record not preserved: content = %q, err = %v, want %q, nil", got, readErr, archivedContent)
	}
}

// TestHandleDaemonSubmitPop_SecondPopReturnsSameMessageAfterReadinessFailure
// is the end-to-end regression test critic specified (adopted by guardian as
// stronger than checking an internal file location): after a readiness
// failure and its rollback, a SECOND pop must return the SAME message, not
// an empty result. This is what actually matters to whoever is waiting on
// that message -- it proves the message's unread state genuinely survives a
// readiness failure, rather than merely proving a file ended up in a
// particular directory.
func TestHandleDaemonSubmitPop_SecondPopReturnsSameMessageAfterReadinessFailure(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	if _, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-first",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	}); err == nil {
		t.Fatalf("first handleDaemonSubmitPop() error = nil, want a readiness failure")
	}

	daemonPopArchiveVerify = originalVerify
	response, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-second",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("second handleDaemonSubmitPop() error = %v, want the message to be re-discoverable", err)
	}
	if response.Empty {
		t.Fatalf("second handleDaemonSubmitPop() reported Empty=true, want the same message returned, not an empty result")
	}
	if response.Filename != filename {
		t.Fatalf("second handleDaemonSubmitPop() Filename = %q, want %q", response.Filename, filename)
	}
	if response.Content != content {
		t.Fatalf("second handleDaemonSubmitPop() Content = %q, want %q", response.Content, content)
	}
}

// TestPopVerificationFailureDeadLetterThresholdIsThree pins F-013's N=3
// product decision with a literal, hardcoded comparison (MED-1): the
// loop-based tests below all iterate
// popVerificationFailureDeadLetterThreshold times, so they pass identically
// whether the constant is 3, 5, or any other value -- they prove the
// mechanism is internally consistent, not that the user's actual decision
// (3) is what shipped. Changing the constant without deliberately updating
// this test's literal must fail.
func TestPopVerificationFailureDeadLetterThresholdIsThree(t *testing.T) {
	if popVerificationFailureDeadLetterThreshold != 3 {
		t.Fatalf("popVerificationFailureDeadLetterThreshold = %d, want 3 (the user's F-013 decision; if this is a deliberate change, update this test's literal too)", popVerificationFailureDeadLetterThreshold)
	}
}

// TestHandleDaemonSubmitPop_RollsBackBelowDeadLetterThreshold is the F-013
// companion to TestHandleDaemonSubmitPop_RollsBackToInboxWhenArchiveReadinessCheckFails:
// it drives two consecutive verification failures for the SAME message
// (popVerificationFailureDeadLetterThreshold is 3) and asserts both still
// roll back to inbox rather than dead-lettering -- the threshold must not
// fire early.
func TestHandleDaemonSubmitPop_RollsBackBelowDeadLetterThreshold(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	inboxPath := filepath.Join(inboxDir, filename)

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	for attempt := 1; attempt <= popVerificationFailureDeadLetterThreshold-1; attempt++ {
		if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(attempt %d): %v", attempt, err)
		}
		_, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
			RequestID: "req-pop-below-threshold",
			Command:   projection.DaemonSubmitPop,
			Node:      "worker",
		})
		if err == nil {
			t.Fatalf("attempt %d: handleDaemonSubmitPop() error = nil, want a readiness failure", attempt)
		}
		if !strings.Contains(err.Error(), "rolled back to inbox") {
			t.Fatalf("attempt %d: handleDaemonSubmitPop() error = %v, want rollback below threshold", attempt, err)
		}
		if got, readErr := os.ReadFile(inboxPath); readErr != nil || string(got) != content {
			t.Fatalf("attempt %d: message not rolled back to inbox: content = %q, err = %v", attempt, got, readErr)
		}
		if _, statErr := os.Stat(filepath.Join(sessionDir, "dead-letter", strings.TrimSuffix(filename, ".md")+message.DlSuffixPopVerificationExhausted+".md")); !os.IsNotExist(statErr) {
			t.Fatalf("attempt %d: message dead-lettered before reaching threshold", attempt)
		}
	}
}

// TestHandleDaemonSubmitPop_DeadLettersAfterExhaustedRetries is F-013's core
// regression test: after popVerificationFailureDeadLetterThreshold
// consecutive verification failures for the SAME message, the Nth failure
// must dead-letter the message instead of rolling it back to inbox again --
// otherwise a persistently unreadable archive retries forever. It asserts
// the dead-letter file exists with the original content, the message is
// NOT restored to inbox, a MailboxProjectionDeadLetteredEventType event was
// recorded, and an accurate operator notification (not the generic "not
// delivered" text, with contextId populated) landed in the sender's inbox.
func TestHandleDaemonSubmitPop_DeadLettersAfterExhaustedRetries(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  contextId: ctx-f013-test\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	inboxPath := filepath.Join(inboxDir, filename)

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	var lastErr error
	for attempt := 1; attempt <= popVerificationFailureDeadLetterThreshold; attempt++ {
		// inboxDir itself persists across iterations (nothing in this test
		// prunes it), so re-seeding inboxPath here always succeeds, even on
		// the final attempt whose rollback never runs (it dead-letters
		// instead).
		if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(attempt %d): %v", attempt, err)
		}
		_, lastErr = handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
			RequestID: "req-pop-exhausted",
			Command:   projection.DaemonSubmitPop,
			Node:      "worker",
		})
		if lastErr == nil {
			t.Fatalf("attempt %d: handleDaemonSubmitPop() error = nil, want a readiness failure", attempt)
		}
	}
	if !strings.Contains(lastErr.Error(), "moved to dead-letter") {
		t.Fatalf("final attempt error = %v, want dead-letter outcome after exhausting retries", lastErr)
	}

	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("message restored to inbox after exhausting retries, want it gone: err = %v", err)
	}
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", strings.TrimSuffix(filename, ".md")+message.DlSuffixPopVerificationExhausted+".md")
	got, err := os.ReadFile(deadLetterPath)
	if err != nil {
		t.Fatalf("ReadFile(dead-letter): %v", err)
	}
	if string(got) != content {
		t.Fatalf("dead-letter content = %q, want %q", got, content)
	}

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay: %v", err)
	}
	var sawDeadLettered bool
	for _, event := range events {
		if event.Type != projection.MailboxProjectionDeadLetteredEventType {
			continue
		}
		payload, ok := decodeMailboxEventPayloadForTest(t, event.Payload)
		if !ok {
			t.Fatal("dead-lettered payload did not decode")
		}
		if payload.MessageID == filename {
			sawDeadLettered = true
			if payload.FailureReason != message.DeadLetterReasonPopVerificationExhausted {
				t.Fatalf("dead-lettered FailureReason = %q, want %q", payload.FailureReason, message.DeadLetterReasonPopVerificationExhausted)
			}
		}
	}
	if !sawDeadLettered {
		t.Fatal("missing mailbox_projection_dead_lettered journal event for exhausted message")
	}

	notifyDir := filepath.Join(sessionDir, "inbox", "orchestrator")
	entries, err := os.ReadDir(notifyDir)
	if err != nil {
		t.Fatalf("ReadDir(notify inbox): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no dead-letter notification written to sender's inbox")
	}
	notifContent, err := os.ReadFile(filepath.Join(notifyDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(notification): %v", err)
	}
	// MED-2: the notification must not claim the message "was not
	// delivered" (it was -- the failure is reading it back afterward), and
	// contextId must be populated from the archived message's own
	// frontmatter, not left empty.
	if strings.Contains(string(notifContent), "was not delivered") {
		t.Fatalf("notification content = %q, want accurate wording, not the generic \"was not delivered\" claim", notifContent)
	}
	if !strings.Contains(string(notifContent), "contextId: ctx-f013-test") {
		t.Fatalf("notification content = %q, want contextId populated from the archived message's frontmatter", notifContent)
	}
}

// TestHandleDaemonSubmitPop_UnknownFailureCountFailsOpenToRollback is the
// regression test for MAJ-1: if projection.CountPopVerificationFailures
// itself fails (a damaged journal record, a read error), the count must be
// treated as unknown -- never fabricated as 0 -- and the outcome must
// always be a rollback (fail-open), never a dead-letter, regardless of how
// many real failures already happened. Failing closed here would destroy a
// healthy message purely because the daemon couldn't read its own history.
// This also asserts the returned error says "unknown" rather than a
// fabricated "0/3".
func TestHandleDaemonSubmitPop_UnknownFailureCountFailsOpenToRollback(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	// Deliberately do NOT install a shadow journal manager: recordMailboxProjectionPayload
	// becomes a silent no-op and projection.CountPopVerificationFailures's
	// underlying journal.ReplayEach call still succeeds trivially against an
	// empty/missing journal directory in the general case, so to force a
	// genuine count error this test instead points CountPopVerificationFailures
	// at a session directory whose journal records path (<sessionDir>/journal/records)
	// exists as a file, not a directory, so the underlying os.ReadDir call
	// fails outright.
	recordsPath := filepath.Join(sessionDir, "journal", "records")
	if err := os.RemoveAll(filepath.Dir(recordsPath)); err != nil {
		t.Fatalf("RemoveAll(journal dir seeded by CreateSessionDirs): %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(recordsPath), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(recordsPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("WriteFile(records-as-file): %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	inboxPath := filepath.Join(inboxDir, filename)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	_, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-unknown-count",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err == nil {
		t.Fatal("handleDaemonSubmitPop() error = nil, want a readiness failure")
	}
	if strings.Contains(err.Error(), "moved to dead-letter") {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want rollback (fail-open) when the failure count is unknown, not dead-letter", err)
	}
	if !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want it to report the failure count as unknown, not a fabricated count", err)
	}
	if strings.Contains(err.Error(), "0/3") {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want no fabricated 0/3 count", err)
	}
	if got, readErr := os.ReadFile(inboxPath); readErr != nil || string(got) != content {
		t.Fatalf("message not rolled back to inbox despite unknown count: content = %q, err = %v", got, readErr)
	}
}

// TestHandleDaemonSubmitPop_RollbackFailureFallsBackToDeadLetter is the
// regression test for MAJ-2's mutual-fallback requirement: if the primary
// recovery (rollback, below threshold) fails to write, the message must not
// simply be lost -- it must fall back to dead-lettering the known-good
// in-memory copy rather than discarding it.
func TestHandleDaemonSubmitPop_RollbackFailureFallsBackToDeadLetter(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	originalOps := writeFileAtomicOpsVar
	rollbackErr := errors.New("simulated rollback write failure")
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll: os.MkdirAll,
		writeFile: func(name string, data []byte, perm os.FileMode) error {
			// Both the rollback and dead-letter writes go through this same
			// package-level seam, so failing it unconditionally would fail
			// both arms and defeat this test's purpose (proving a
			// rollback-specific failure still falls back to a genuinely
			// successful dead-letter write). Fail only the write aimed at
			// the inbox (the rollback's destination); let a write aimed
			// anywhere else (the dead-letter path) genuinely succeed.
			if strings.Contains(name, string(filepath.Separator)+"inbox"+string(filepath.Separator)) {
				return rollbackErr
			}
			return os.WriteFile(name, data, perm)
		},
		rename: os.Rename,
	}
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })

	_, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-rollback-fallback",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err == nil {
		t.Fatal("handleDaemonSubmitPop() error = nil, want a readiness failure")
	}
	if !strings.Contains(err.Error(), "fell back to dead-letter") {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want it to report falling back to dead-letter", err)
	}
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", strings.TrimSuffix(filename, ".md")+message.DlSuffixPopVerificationExhausted+".md")
	got, readErr := os.ReadFile(deadLetterPath)
	if readErr != nil {
		t.Fatalf("ReadFile(dead-letter fallback): %v", readErr)
	}
	if string(got) != content {
		t.Fatalf("dead-letter fallback content = %q, want %q -- the known-good copy must survive the rollback failure", got, content)
	}
}

// TestHandleDaemonSubmitPop_BothRecoveryArmsFailingReportsMessageMayBeLost
// is MAJ-2's negative case: when BOTH the primary and fallback recovery
// writes fail, handleDaemonSubmitPop must not silently swallow that --
// it must return an error that says the message may be lost, so this is
// visible in logs/response rather than a generic-looking failure.
func TestHandleDaemonSubmitPop_BothRecoveryArmsFailingReportsMessageMayBeLost(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	originalOps := writeFileAtomicOpsVar
	writeOpsErr := errors.New("simulated write failure")
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll:  os.MkdirAll,
		writeFile: func(name string, data []byte, perm os.FileMode) error { return writeOpsErr },
		rename:    os.Rename,
	}
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })

	_, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-both-fail",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	})
	if err == nil {
		t.Fatal("handleDaemonSubmitPop() error = nil, want a readiness failure")
	}
	if !strings.Contains(err.Error(), "message may be lost") {
		t.Fatalf("handleDaemonSubmitPop() error = %v, want it to explicitly say the message may be lost when both recovery arms fail", err)
	}
}

// TestHandleDaemonSubmitPopDeadLetter_RejectsUnvalidatedSenderInNotification
// is the regression test for MAJ-3, a security regression: payload.From can
// fall back to an unvalidated value from the message body's envelope when
// filename parsing fails, and the notification sink
// (notifyPopVerificationDeadLetter) joins it into a path and MkdirAlls it. A
// crafted From like "../../../../tmp/x-pop-poc" fails nodeaddr.Validate's
// pattern (it contains "/"), so handleDaemonSubmitPopDeadLetter must never
// call the notification sink with it at all. This asserts that directly via
// the notifyPopVerificationDeadLetterFn seam, rather than inferring it from
// filesystem side effects: filepath.Join cleans ".." segments internally,
// so the crafted sender does not land at a single predictable,
// test-assertable path -- it can escape arbitrarily far up the real
// filesystem depending on t.TempDir()'s depth in a given environment, which
// a "did some file appear near here" check cannot reliably catch. It also
// asserts the dead-letter write itself still succeeds (the invalid sender
// should only suppress the notification, not the dead-letter write).
func TestHandleDaemonSubmitPopDeadLetter_RejectsUnvalidatedSenderInNotification(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	originalNotify := notifyPopVerificationDeadLetterFn
	notifyCalled := false
	notifyPopVerificationDeadLetterFn = func(sessionDir, contextID, senderNode, originalFilename, deadLetterBasename, attemptsDescription string) {
		notifyCalled = true
	}
	t.Cleanup(func() { notifyPopVerificationDeadLetterFn = originalNotify })

	// A filename that fails message.ParseMessageFilename (missing the
	// "-from-...-to-..." markers) forces mailboxProjectionPayloadForFile to
	// fall back to whatever "from" the envelope metadata supplies, which is
	// exactly the unvalidated path this test targets.
	filename := "not-a-parseable-message-name.md"
	content := "---\nparams:\n  from: \"../../../../tmp/x-pop-poc\"\n  to: worker\n---\n\npayload\n"

	if err := handleDaemonSubmitPopDeadLetter(sessionDir, "review-session", filename, []byte(content), "after 3 verification attempts (threshold 3)"); err != nil {
		t.Fatalf("handleDaemonSubmitPopDeadLetter: %v", err)
	}

	if notifyCalled {
		t.Fatal("notification sink was called with an unvalidated sender that fails nodeaddr.Validate")
	}
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", strings.TrimSuffix(filename, ".md")+message.DlSuffixPopVerificationExhausted+".md")
	if _, err := os.Stat(deadLetterPath); err != nil {
		t.Fatalf("dead-letter write should still succeed despite invalid sender: %v", err)
	}
}

// TestWriteDeadLetterFileAtomicCreatesDirectoryAndIsAtomic is the
// regression test for MAJ-2's atomicity requirement on the dead-letter
// write specifically: it must MkdirAll (the dead-letter directory can be
// missing, matching the defensive posture already established for the
// rollback path) and must never leave a partial file at dst on a failed
// write.
func TestWriteDeadLetterFileAtomicCreatesDirectoryAndIsAtomic(t *testing.T) {
	tmpDir := t.TempDir()
	dst := filepath.Join(tmpDir, "dead-letter", "message-dl-pop-verification-exhausted.md")

	if err := writeDeadLetterFileAtomic(dst, []byte("payload")); err != nil {
		t.Fatalf("writeDeadLetterFileAtomic() error = %v, want success with directory auto-created", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "payload" {
		t.Fatalf("ReadFile() = %q, %v, want %q, nil", got, err, "payload")
	}

	original := "payload"
	writeErr := errors.New("simulated interrupted dead-letter write")
	originalOps := writeFileAtomicOpsVar
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll: os.MkdirAll,
		writeFile: func(name string, data []byte, perm os.FileMode) error {
			_ = os.WriteFile(name, []byte("PARTIAL"), perm)
			return writeErr
		},
		rename: os.Rename,
	}
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })

	err = writeDeadLetterFileAtomic(dst, []byte("new-payload"))
	if !errors.Is(err, writeErr) {
		t.Fatalf("writeDeadLetterFileAtomic() error = %v, want %v", err, writeErr)
	}
	if got, readErr := os.ReadFile(dst); readErr != nil || string(got) != original {
		t.Fatalf("dst changed after a failed write step: content = %q, err = %v, want %q, nil", got, readErr, original)
	}
}

// TestWriteDeadLetterFileAtomicRejectsSymlinkedDeadLetterDir is critic's
// coverage-gap fix: mailbox_store_test.go already proves
// store.ValidateDeadLetterTarget itself rejects a symlinked dead-letter
// directory, but nothing in THIS package proved that writeDeadLetterFileAtomic
// -- the daemon's own wrapper, the one every real dead-letter write actually
// goes through -- still calls it. Removing that call from
// writeDeadLetterFileAtomic's body left the whole suite green before this
// test existed. This mirrors the exact call-site-vs-helper gap B1 closed on
// #734, one round later, in a different function.
func TestWriteDeadLetterFileAtomicRejectsSymlinkedDeadLetterDir(t *testing.T) {
	sessionDir := t.TempDir()
	deadLetterDir := filepath.Join(sessionDir, "dead-letter")
	elsewhere := filepath.Join(t.TempDir(), "elsewhere-dead-letter")
	if err := os.MkdirAll(elsewhere, 0o700); err != nil {
		t.Fatalf("MkdirAll(elsewhere): %v", err)
	}
	if err := os.Symlink(elsewhere, deadLetterDir); err != nil {
		t.Fatalf("Symlink: %v", err)
	}

	dst := filepath.Join(deadLetterDir, "message-dl-pop-verification-exhausted.md")
	err := writeDeadLetterFileAtomic(dst, []byte("payload"))
	if err == nil {
		t.Fatal("writeDeadLetterFileAtomic() error = nil, want rejection of a symlinked dead-letter directory")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("writeDeadLetterFileAtomic() error = %v, want it to mention the symlink rejection", err)
	}
	if _, statErr := os.Lstat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("file written despite symlinked dead-letter dir: statErr = %v, want IsNotExist", statErr)
	}
}

// TestCountPopVerificationFailures_ScopesToFilename proves
// projection.CountPopVerificationFailures counts events by MessageID, not
// merely by event type -- two different messages accumulating failures in
// the same session journal must not share a count (#755 F-013).
func TestCountPopVerificationFailures_ScopesToFilename(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)

	filenameA := "20260414-033200-from-orchestrator-to-worker.md"
	filenameB := "20260414-033201-from-orchestrator-to-worker.md"
	recordMailboxProjectionPayload(sessionDir, "review-session", projection.MailboxProjectionPopVerificationFailedEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{MessageID: filenameA})
	recordMailboxProjectionPayload(sessionDir, "review-session", projection.MailboxProjectionPopVerificationFailedEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{MessageID: filenameA})
	recordMailboxProjectionPayload(sessionDir, "review-session", projection.MailboxProjectionPopVerificationFailedEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{MessageID: filenameB})

	countA, err := projection.CountPopVerificationFailures(sessionDir, filenameA)
	if err != nil {
		t.Fatalf("CountPopVerificationFailures(A): %v", err)
	}
	if countA != 2 {
		t.Fatalf("countA = %d, want 2", countA)
	}
	countB, err := projection.CountPopVerificationFailures(sessionDir, filenameB)
	if err != nil {
		t.Fatalf("CountPopVerificationFailures(B): %v", err)
	}
	if countB != 1 {
		t.Fatalf("countB = %d, want 1", countB)
	}
}

// TestHandleDaemonSubmitPop_DeadLetterSyncDoesNotResurrectInbox is the
// regression test for guardian's B-1 finding: internal/projection's
// MailboxProjectionDeadLetteredEventType handler never deleted the message
// from projected.Inbox, so a mailbox-projection sync run after
// dead-lettering would write it right back into inbox/ -- resurrecting an
// already-dead-lettered message as live unread mail while it simultaneously
// sat in dead-letter/, contradicting the entire point of dead-lettering.
// TestHandleDaemonSubmitPop_DeadLettersAfterExhaustedRetries never calls
// SyncMailboxProjection, so it structurally could not catch this; this test
// drives the same threshold-exhaustion sequence but syncs the projection
// BETWEEN pops, which is what actually exposed the bug in review.
func TestHandleDaemonSubmitPop_DeadLetterSyncDoesNotResurrectInbox(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	inboxPath := filepath.Join(inboxDir, filename)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	// Seed the Delivered event the real post-to-inbox pipeline would have
	// recorded when this message first landed. Without it, projected.Inbox
	// never holds this message in the first place, and the resurrection bug
	// this test targets has nothing to resurrect.
	recordMailboxProjectionPayload(sessionDir, "review-session", projection.MailboxProjectionDeliveredEventType, journal.VisibilityOperatorVisible, journal.MailboxEventPayload{
		MessageID: filename,
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", filename),
		Content:   content,
	})

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	for attempt := 1; attempt <= popVerificationFailureDeadLetterThreshold; attempt++ {
		if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(attempt %d): %v", attempt, err)
		}
		if _, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
			RequestID: "req-pop-dl-sync",
			Command:   projection.DaemonSubmitPop,
			Node:      "worker",
		}); err == nil {
			t.Fatalf("attempt %d: handleDaemonSubmitPop() error = nil, want a readiness failure", attempt)
		}
		// B-1: sync BETWEEN pops -- this is the step
		// TestHandleDaemonSubmitPop_DeadLettersAfterExhaustedRetries never
		// performs, and is exactly what exposes the resurrection bug.
		if err := projection.SyncMailboxProjection(sessionDir); err != nil {
			t.Fatalf("attempt %d: SyncMailboxProjection: %v", attempt, err)
		}
	}

	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("message resurrected in inbox/ after a post-dead-letter projection sync: err = %v, want IsNotExist", err)
	}
	deadLetterPath := filepath.Join(sessionDir, "dead-letter", strings.TrimSuffix(filename, ".md")+message.DlSuffixPopVerificationExhausted+".md")
	if _, err := os.Stat(deadLetterPath); err != nil {
		t.Fatalf("dead-letter file missing after sync: %v", err)
	}
}

// TestHandleDaemonSubmitPop_ThirdConsecutiveFailureDeadLettersSecondDoesNot
// is B-2's fix. The existing threshold tests
// (TestHandleDaemonSubmitPop_RollsBackBelowDeadLetterThreshold and
// TestHandleDaemonSubmitPop_DeadLettersAfterExhaustedRetries) loop
// "popVerificationFailureDeadLetterThreshold-1" or
// "popVerificationFailureDeadLetterThreshold" times -- they scale with
// whatever the constant currently says, so they still pass unchanged even
// if its value drifts; they assert that SOME Nth attempt dead-letters, not
// that N is specifically 3. This test hardcodes the literal attempt
// numbers the user's F-013 decision actually specifies -- the 2nd
// consecutive verification failure for a message must still roll back, and
// the 3rd must dead-letter -- so mutating the threshold constant to any
// value other than 3 makes THIS test fail, independent of whether
// TestPopVerificationFailureDeadLetterThresholdIsThree's own literal
// comparison is also touched.
func TestHandleDaemonSubmitPop_ThirdConsecutiveFailureDeadLettersSecondDoesNot(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	inboxPath := filepath.Join(inboxDir, filename)

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	popOnce := func(attempt int, requestID string) error {
		if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(attempt %d): %v", attempt, err)
		}
		_, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
			RequestID: requestID,
			Command:   projection.DaemonSubmitPop,
			Node:      "worker",
		})
		if err == nil {
			t.Fatalf("attempt %d: handleDaemonSubmitPop() error = nil, want a readiness failure", attempt)
		}
		return err
	}

	if err := popOnce(1, "req-pop-literal-1"); !strings.Contains(err.Error(), "rolled back to inbox") {
		t.Fatalf("attempt 1 error = %v, want rollback", err)
	}
	if err := popOnce(2, "req-pop-literal-2"); !strings.Contains(err.Error(), "rolled back to inbox") {
		t.Fatalf("attempt 2 (literal, not threshold-relative) error = %v, want rollback -- the 2nd failure must not dead-letter", err)
	}
	if err := popOnce(3, "req-pop-literal-3"); !strings.Contains(err.Error(), "moved to dead-letter") {
		t.Fatalf("attempt 3 (literal, not threshold-relative) error = %v, want dead-letter -- the 3rd failure must dead-letter", err)
	}
}

// TestHandleDaemonSubmitPop_DeadLetterNotificationAccurateOnFallbackPath is
// B-3's regression test: the dead-letter notification text must not falsely
// claim a fixed attempt count on the rollback-write-failure fallback path,
// where dead-lettering can be reached after just the FIRST verification
// failure (well below the threshold), not after
// popVerificationFailureDeadLetterThreshold attempts.
func TestHandleDaemonSubmitPop_DeadLetterNotificationAccurateOnFallbackPath(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", time.Now())
	t.Cleanup(journal.ClearProcessManager)
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	filename := "20260414-033200-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  contextId: ctx-b3-test\n  from: orchestrator\n  to: worker\n  timestamp: 2026-04-14T03:32:00Z\n---\n\npayload\n"
	if err := os.WriteFile(filepath.Join(inboxDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	originalVerify := daemonPopArchiveVerify
	daemonPopArchiveVerify = func(readPath, expectedContent string) error {
		return errors.New("simulated readiness failure")
	}
	t.Cleanup(func() { daemonPopArchiveVerify = originalVerify })

	originalOps := writeFileAtomicOpsVar
	rollbackErr := errors.New("simulated rollback write failure")
	writeFileAtomicOpsVar = writeFileAtomicOps{
		mkdirAll: os.MkdirAll,
		writeFile: func(name string, data []byte, perm os.FileMode) error {
			if strings.Contains(name, string(filepath.Separator)+"inbox"+string(filepath.Separator)) {
				return rollbackErr
			}
			return os.WriteFile(name, data, perm)
		},
		rename: os.Rename,
	}
	t.Cleanup(func() { writeFileAtomicOpsVar = originalOps })

	// A single pop attempt: the FIRST verification failure for this
	// message, well below the threshold -- but the rollback write itself
	// fails, so this falls back to dead-lettering after only 1 attempt.
	if _, err := handleDaemonSubmitPop(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-notify-fallback",
		Command:   projection.DaemonSubmitPop,
		Node:      "worker",
	}); err == nil {
		t.Fatal("handleDaemonSubmitPop() error = nil, want a readiness failure")
	}

	notifyDir := filepath.Join(sessionDir, "inbox", "orchestrator")
	entries, err := os.ReadDir(notifyDir)
	if err != nil {
		t.Fatalf("ReadDir(notify inbox): %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no dead-letter notification written to sender's inbox")
	}
	notifContent, err := os.ReadFile(filepath.Join(notifyDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile(notification): %v", err)
	}
	// B-3: this dead-letter happened after 1 failed verification attempt,
	// via the rollback-failure fallback -- the threshold was never reached.
	// The notification must not claim otherwise.
	if strings.Contains(string(notifContent), "after 3 verification attempts") {
		t.Fatalf("notification content = %q, want it to not falsely claim the threshold was reached on the rollback-failure fallback path", notifContent)
	}
	if !strings.Contains(string(notifContent), "rollback recovery attempt itself failed") {
		t.Fatalf("notification content = %q, want it to describe the actual reason (rollback write failure), not a fabricated attempt count", notifContent)
	}
}

func TestProcessDaemonSubmitRequest_PopRecordsReadBeforeProjectionSync(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	now := time.Date(2026, time.May, 4, 6, 5, 0, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", now)
	t.Cleanup(journal.ClearProcessManager)

	filename := "20260504-150109-sfb93-r001f-from-orchestrator-to-worker.md"
	content := "---\nparams:\n  from: orchestrator\n  to: worker\n  messageId: " + filename + "\n  replyPolicy: required\n  timestamp: 2026-05-04T15:01:09+09:00\n---\n\nplease work\n"
	recordMailboxProjectionPayload(sessionDir, "review-session", projection.MailboxProjectionDeliveredEventType, journal.VisibilityMailboxProjection, journal.MailboxEventPayload{
		MessageID: filename,
		From:      "orchestrator",
		To:        "worker",
		Path:      filepath.Join("inbox", "worker", filename),
		Content:   content,
	})
	syncMailboxProjection(sessionDir)

	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Fatalf("projected inbox file missing before pop: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-project",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: now.Add(time.Second).UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	readPath := filepath.Join(sessionDir, "read", filename)
	if _, err := os.Stat(readPath); err != nil {
		t.Fatalf("read file missing after pop: %v", err)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file still present after pop or wrong error: %v", err)
	}

	if err := projection.SyncMailboxProjection(sessionDir); err != nil {
		t.Fatalf("SyncMailboxProjection(after pop): %v", err)
	}
	if _, err := os.Stat(readPath); err != nil {
		t.Fatalf("read file missing after projection sync: %v", err)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("projection sync resurrected popped inbox file or wrong error: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop-project"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != filename {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, filename)
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileStdoutReturnsBoundedPayload(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "stdout",
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q", response.Error)
	}
	if response.RuntimeProfile == nil {
		t.Fatal("RuntimeProfile = nil")
	}
	if response.RuntimeProfile.Kind != runtimeprofile.KindGoroutine ||
		response.RuntimeProfile.Destination != "stdout" ||
		response.RuntimeProfile.Encoding != "base64" ||
		response.RuntimeProfile.OutputPath != "" {
		t.Fatalf("RuntimeProfile metadata = %#v", response.RuntimeProfile)
	}
	data, err := base64.StdEncoding.DecodeString(response.RuntimeProfile.ContentBase64)
	if err != nil {
		t.Fatalf("DecodeString(ContentBase64): %v", err)
	}
	if len(data) == 0 || len(data) != response.RuntimeProfile.Bytes {
		t.Fatalf("profile payload bytes = %d, response bytes = %d", len(data), response.RuntimeProfile.Bytes)
	}
	if response.Content != "" || response.MarkdownPath != "" {
		t.Fatalf("profile response leaked message fields: content=%q markdown_path=%q", response.Content, response.MarkdownPath)
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileFileWritesExplicitPathOnly(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "goroutine.pprof")

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile-file",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "file",
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile profile output: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("written profile is empty")
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-file"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q", response.Error)
	}
	if response.RuntimeProfile == nil {
		t.Fatal("RuntimeProfile = nil")
	}
	if response.RuntimeProfile.ContentBase64 != "" {
		t.Fatal("file response should not include profile content")
	}
	if response.RuntimeProfile.OutputPath != outputPath {
		t.Fatalf("OutputPath = %q, want explicit output path %q", response.RuntimeProfile.OutputPath, outputPath)
	}
	if response.RuntimeProfile.Bytes != len(written) {
		t.Fatalf("response bytes = %d, written bytes = %d", response.RuntimeProfile.Bytes, len(written))
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileRequiresExplicitDestination(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:   "req-profile-no-destination",
		Command:     projection.DaemonSubmitRuntimeProfile,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		ProfileKind: runtimeprofile.KindGoroutine,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-no-destination"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error == "" {
		t.Fatal("response.Error = empty, want destination error")
	}
	if response.RuntimeProfile != nil {
		t.Fatalf("RuntimeProfile = %#v, want nil on error", response.RuntimeProfile)
	}
}

func TestProcessDaemonSubmitRequest_QueueMsThresholdExceededEmitsWarning(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// CreatedAt far in the past to guarantee queue_ms > 30,000 ms.
	staleCreatedAt := time.Now().Add(-2 * time.Minute).UTC().Format(time.RFC3339Nano)
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-queue-warn",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: staleCreatedAt,
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "event=queue_ms_threshold_exceeded") {
		t.Fatalf("expected queue_ms_threshold_exceeded WARNING in log; got:\n%s", logged)
	}
	if !strings.Contains(logged, "threshold_ms=30000") {
		t.Fatalf("expected threshold_ms=30000 in WARNING log; got:\n%s", logged)
	}
}

func TestProcessDaemonSubmitRequest_QueueMsBelowThresholdNoWarning(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// CreatedAt is current — queue_ms will be well below the 30,000 ms threshold.
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-queue-no-warn",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	logged := buf.String()
	if strings.Contains(logged, "event=queue_ms_threshold_exceeded") {
		t.Fatalf("unexpected queue_ms_threshold_exceeded WARNING for fast request; got:\n%s", logged)
	}
}

func TestProcessDaemonSubmitRequest_ConfiguredThresholdIsHonored(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "cfg-threshold-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	// Override the package-level threshold to 1 000 ms (1 s) to prove the
	// configured value is used instead of the default 30 000 ms.
	original := daemonSubmitQueueWarnThresholdMs
	daemonSubmitQueueWarnThresholdMs = 1_000
	t.Cleanup(func() { daemonSubmitQueueWarnThresholdMs = original })

	var buf bytes.Buffer
	originalOutput := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(originalOutput)
		log.SetFlags(originalFlags)
	})

	// CreatedAt 5 s ago: queue_ms ~5 000, which is above 1 000 but well below
	// the default 30 000, proving the custom threshold fires.
	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-cfg-threshold",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339Nano),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}

	logged := buf.String()
	if !strings.Contains(logged, "event=queue_ms_threshold_exceeded") {
		t.Fatalf("expected queue_ms_threshold_exceeded WARNING for custom 1 000 ms threshold; got:\n%s", logged)
	}
	if !strings.Contains(logged, "threshold_ms=1000") {
		t.Fatalf("expected threshold_ms=1000 in WARNING log; got:\n%s", logged)
	}
}

func TestProcessDaemonSubmitRequest_AlreadyClaimedRequestIsNoop(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033300-from-orchestrator-to-worker.md"
	newest := "20260414-033301-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("oldest"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("newest"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-once",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest(first): %v", err)
	}
	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest(second): %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file should not be popped by duplicate processing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("oldest read file missing: %v", err)
	}
}

func TestProcessDaemonSubmitRequest_ConcurrentClaimsPopOnlyOnce(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	oldest := "20260414-033400-from-orchestrator-to-worker.md"
	newest := "20260414-033401-from-orchestrator-to-worker.md"
	if err := os.WriteFile(filepath.Join(inboxDir, oldest), []byte("oldest"), 0o600); err != nil {
		t.Fatalf("WriteFile oldest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, newest), []byte("newest"), 0o600); err != nil {
		t.Fatalf("WriteFile newest: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID: "req-pop-concurrent",
		Command:   projection.DaemonSubmitPop,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Node:      "worker",
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := processDaemonSubmitRequest(requestPath)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("processDaemonSubmitRequest concurrent error: %v", err)
		}
	}

	if _, err := os.Stat(filepath.Join(sessionDir, "read", oldest)); err != nil {
		t.Fatalf("oldest read file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(inboxDir, newest)); err != nil {
		t.Fatalf("newest inbox file should not be popped by duplicate concurrent processing: %v", err)
	}
	if _, err := os.Stat(requestPath); !os.IsNotExist(err) {
		t.Fatalf("request file still present or wrong error: %v", err)
	}
	if _, err := os.Stat(requestPath + ".processing"); !os.IsNotExist(err) {
		t.Fatalf("claimed request file still present or wrong error: %v", err)
	}

	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-pop-concurrent"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Filename != oldest {
		t.Fatalf("response.Filename = %q, want %q", response.Filename, oldest)
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileFileRefusesOverwriteByDefault(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "goroutine.pprof")
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile existing: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile-no-overwrite",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "file",
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-no-overwrite"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error == "" {
		t.Fatal("response.Error = empty, want overwrite refusal error")
	}
	if !strings.Contains(response.Error, "already exists") {
		t.Fatalf("response.Error = %q, want 'already exists'", response.Error)
	}
	got, _ := os.ReadFile(outputPath)
	if string(got) != "existing" {
		t.Fatalf("existing file was modified: %q", string(got))
	}
}

func TestProcessDaemonSubmitRequest_RuntimeProfileFileForceOverwrites(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "goroutine.pprof")
	if err := os.WriteFile(outputPath, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile existing: %v", err)
	}

	requestPath, err := projection.WriteDaemonSubmitRequest(sessionDir, projection.DaemonSubmitRequest{
		RequestID:          "req-profile-force",
		Command:            projection.DaemonSubmitRuntimeProfile,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		ProfileKind:        runtimeprofile.KindGoroutine,
		ProfileDestination: "file",
		ProfileOutputPath:  outputPath,
		ProfileMaxBytes:    runtimeprofile.DefaultMaxBytes,
		ProfileForce:       true,
	})
	if err != nil {
		t.Fatalf("WriteDaemonSubmitRequest: %v", err)
	}

	if _, err := processDaemonSubmitRequest(requestPath); err != nil {
		t.Fatalf("processDaemonSubmitRequest: %v", err)
	}
	response, err := projection.ReadDaemonSubmitResponse(projection.DaemonSubmitResponsePath(sessionDir, "req-profile-force"))
	if err != nil {
		t.Fatalf("ReadDaemonSubmitResponse: %v", err)
	}
	if response.Error != "" {
		t.Fatalf("response.Error = %q, want empty (force overwrite succeeded)", response.Error)
	}
	written, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile profile output: %v", err)
	}
	if string(written) == "existing" {
		t.Fatal("file was not overwritten by --force")
	}
	if len(written) == 0 {
		t.Fatal("overwritten profile is empty")
	}
}

// TestRecordDaemonSubmitPopRead_PrefersFallbackOverTruncatedReadPath
// reproduces the #633 race: readPath is caught mid-truncation (0 bytes) by
// a concurrent projection sync at the exact moment the pop path records its
// read event. The known-good content read from the inbox message before
// archiving (fallbackContent) must win instead of the torn on-disk read.
func TestRecordDaemonSubmitPopRead_PrefersFallbackOverTruncatedReadPath(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "review-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	now := time.Date(2026, time.July, 10, 15, 2, 6, 0, time.UTC)
	installShadowJournalManager(sessionDir, "ctx-main", "review-session", now)
	t.Cleanup(journal.ClearProcessManager)

	filename := "20260710-000149-s7c1c-ra364-from-orchestrator-to-guardian.md"
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.MkdirAll(filepath.Dir(readPath), 0o700); err != nil {
		t.Fatalf("MkdirAll(read): %v", err)
	}
	// Simulate the torn/truncated file a racing projection sync can leave
	// visible mid-rewrite: present on disk, but 0 bytes.
	if err := os.WriteFile(readPath, nil, 0o600); err != nil {
		t.Fatalf("WriteFile(truncated readPath): %v", err)
	}

	recordDaemonSubmitPopRead(sessionDir, readPath, filename, "full correct body")

	events, err := journal.Replay(sessionDir)
	if err != nil {
		t.Fatalf("journal.Replay() error = %v", err)
	}
	last := events[len(events)-1]
	if last.Type != projection.MailboxProjectionReadEventType {
		t.Fatalf("last event type = %q, want %s", last.Type, projection.MailboxProjectionReadEventType)
	}
	var payload map[string]string
	if err := json.Unmarshal(last.Payload, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload): %v", err)
	}
	if payload["content"] != "full correct body" {
		t.Fatalf("recorded read content = %q, want full correct body (torn read must not win)", payload["content"])
	}
}
