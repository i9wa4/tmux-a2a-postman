package message

import (
	"strings"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
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

func hasEvidenceFields(metadata envelope.Metadata) bool {
	return metadata.EvidenceCommand != "" ||
		metadata.EvidenceArtifact != "" ||
		metadata.EvidenceHash != ""
}
