package message

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/evidence"
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

func evidenceGateObservedAt(path string) time.Time {
	info, err := os.Stat(path)
	if err == nil {
		return info.ModTime().UTC()
	}
	return time.Now().UTC()
}
