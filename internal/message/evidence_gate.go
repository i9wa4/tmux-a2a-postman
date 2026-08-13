package message

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/evidence"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func isCompletionClaim(body string) bool {
	firstLine := body
	if idx := strings.IndexByte(firstLine, '\n'); idx >= 0 {
		firstLine = firstLine[:idx]
	}
	firstLine = strings.TrimSpace(firstLine)
	return firstLine == "DONE" ||
		strings.HasPrefix(firstLine, "DONE:") ||
		firstLine == "PASS" ||
		strings.HasPrefix(firstLine, "PASS:") ||
		strings.HasPrefix(firstLine, "APPROVED:")
}

func hasEvidenceReplayContract(metadata envelope.Metadata) bool {
	contract, ok := evidenceReplayContractFromMetadata(metadata)
	if !ok {
		return false
	}
	return contract.ValidateShape() == nil
}

func evidenceReplayContractFromMetadata(metadata envelope.Metadata) (evidence.ReplayContract, bool) {
	fields := []string{
		metadata.EvidenceCommand,
		metadata.EvidenceCWD,
		metadata.EvidenceTimeoutSeconds,
		metadata.EvidenceSideEffectClass,
		metadata.EvidenceArtifact,
		metadata.EvidenceHash,
	}
	any := false
	for _, field := range fields {
		if strings.TrimSpace(field) != "" {
			any = true
			break
		}
	}
	if !any && strings.TrimSpace(metadata.EvidenceEnvAllowlist) == "" {
		return evidence.ReplayContract{}, false
	}

	timeoutSeconds, err := strconv.Atoi(strings.TrimSpace(metadata.EvidenceTimeoutSeconds))
	if err != nil {
		return evidence.ReplayContract{}, true
	}
	contract := evidence.ReplayContract{
		Command:              strings.TrimSpace(metadata.EvidenceCommand),
		CWD:                  strings.TrimSpace(metadata.EvidenceCWD),
		EnvAllowlist:         parseEvidenceEnvAllowlist(metadata.EvidenceEnvAllowlist),
		Timeout:              time.Duration(timeoutSeconds) * time.Second,
		SideEffect:           evidence.SideEffectClass(strings.TrimSpace(metadata.EvidenceSideEffectClass)),
		ArtifactPath:         strings.TrimSpace(metadata.EvidenceArtifact),
		ExpectedArtifactHash: strings.TrimSpace(metadata.EvidenceHash),
	}
	return contract, true
}

func parseEvidenceEnvAllowlist(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}
	return out
}

func evidenceGateObservedAt(sessionDir, sessionName, filename, path string, now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	observedPayload := journal.MailboxEventPayload{
		MessageID:  filename,
		Path:       filepath.Join("post", filename),
		SourcePath: path,
	}
	if observedAt, ok := evidenceGateRecordedObservedAt(sessionDir, sessionName, observedPayload); ok {
		return observedAt
	}

	equivalent := func(event journal.Event) (bool, error) {
		if event.Type != projection.MailboxProjectionPostObservedEventType {
			return false, nil
		}
		payload, ok := decodeEvidenceGateObservedPayload(event)
		if !ok {
			return false, nil
		}
		return payload.MessageID == observedPayload.MessageID &&
			payload.Path == observedPayload.Path &&
			payload.SourcePath == observedPayload.SourcePath, nil
	}
	if _, err := journal.RecordProcessMailboxPayloadIfAbsent(
		sessionDir,
		sessionName,
		projection.MailboxProjectionPostObservedEventType,
		journal.VisibilityMailboxProjection,
		observedPayload,
		equivalent,
		now.UTC(),
	); err != nil {
		return now.UTC()
	}
	if observedAt, ok := evidenceGateRecordedObservedAt(sessionDir, sessionName, observedPayload); ok {
		return observedAt
	}
	return now.UTC()
}

func evidenceGateRecordedObservedAt(sessionDir, sessionName string, want journal.MailboxEventPayload) (time.Time, bool) {
	state, ok := currentEvidenceGateSessionState(sessionDir)
	if !ok {
		return time.Time{}, false
	}
	var observedAt time.Time
	err := journal.ReplayEach(sessionDir, func(event journal.Event) error {
		if event.Type != projection.MailboxProjectionPostObservedEventType ||
			event.SessionKey != state.SessionKey ||
			event.Generation != state.Generation ||
			event.TmuxSessionName != sessionName {
			return nil
		}
		payload, ok := decodeEvidenceGateObservedPayload(event)
		if !ok ||
			payload.MessageID != want.MessageID ||
			payload.Path != want.Path ||
			payload.SourcePath != want.SourcePath {
			return nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(event.OccurredAt))
		if err != nil {
			return nil
		}
		if observedAt.IsZero() || parsed.Before(observedAt) {
			observedAt = parsed.UTC()
		}
		return nil
	})
	if err != nil || observedAt.IsZero() {
		return time.Time{}, false
	}
	return observedAt, true
}

func currentEvidenceGateSessionState(sessionDir string) (journal.SessionState, bool) {
	data, err := os.ReadFile(journal.SessionStatePath(sessionDir))
	if err != nil {
		return journal.SessionState{}, false
	}
	var state journal.SessionState
	if err := json.Unmarshal(data, &state); err != nil {
		return journal.SessionState{}, false
	}
	if state.SessionKey == "" || state.Generation <= 0 {
		return journal.SessionState{}, false
	}
	return state, true
}

func decodeEvidenceGateObservedPayload(event journal.Event) (journal.MailboxEventPayload, bool) {
	var payload journal.MailboxEventPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return journal.MailboxEventPayload{}, false
	}
	return payload, true
}
