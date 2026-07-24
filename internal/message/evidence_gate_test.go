package message

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/evidence"
)

func TestHasEvidenceReplayContractRequiresCompleteShape(t *testing.T) {
	partial := envelope.Metadata{
		EvidenceCommand:  "go test ./...",
		EvidenceArtifact: "reports/test.json",
		EvidenceHash:     "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if hasEvidenceReplayContract(partial) {
		t.Fatal("hasEvidenceReplayContract(partial) = true, want false")
	}

	complete := envelope.Metadata{
		EvidenceCommand:         "go test ./...",
		EvidenceCWD:             "/repo",
		EvidenceEnvAllowlist:    "PATH, HOME",
		EvidenceTimeoutSeconds:  "120",
		EvidenceSideEffectClass: string(evidence.SideEffectIdempotent),
		EvidenceArtifact:        "reports/test.json",
		EvidenceHash:            "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if !hasEvidenceReplayContract(complete) {
		t.Fatal("hasEvidenceReplayContract(complete) = false, want true")
	}
}

func TestEvidenceGateObservedAtUsesFileModTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "message.md")
	if err := os.WriteFile(path, []byte("message"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	want := time.Date(2026, 7, 13, 9, 59, 59, 0, time.UTC)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}

	got := evidenceGateObservedAt(path)
	if !got.Equal(want) {
		t.Fatalf("evidenceGateObservedAt() = %s, want %s", got, want)
	}
}
