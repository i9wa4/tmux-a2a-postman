package message

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/envelope"
	"github.com/i9wa4/tmux-a2a-postman/internal/evidence"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

func TestHasEvidenceReplayContractRequiresCompleteShape(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "reports"), 0o755); err != nil {
		t.Fatalf("Mkdir reports: %v", err)
	}
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
		EvidenceCWD:             root,
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

func TestHasEvidenceReplayContractBoundsTimeoutSeconds(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "reports"), 0o755); err != nil {
		t.Fatalf("Mkdir reports: %v", err)
	}
	base := envelope.Metadata{
		EvidenceCommand:         "go test ./...",
		EvidenceCWD:             root,
		EvidenceSideEffectClass: string(evidence.SideEffectReadOnly),
		EvidenceArtifact:        "reports/test.json",
		EvidenceHash:            "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	tests := []struct {
		name    string
		timeout string
		want    bool
	}{
		{name: "maximum", timeout: "3600", want: true},
		{name: "just over maximum", timeout: "3601"},
		{name: "extreme", timeout: "9223372036854775807"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := base
			metadata.EvidenceTimeoutSeconds = tt.timeout
			if got := hasEvidenceReplayContract(metadata); got != tt.want {
				t.Fatalf("hasEvidenceReplayContract() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasEvidenceReplayContractRejectsUncontainedArtifactPath(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "reports"), 0o755); err != nil {
		t.Fatalf("Mkdir reports: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatalf("Symlink escape: %v", err)
	}

	base := envelope.Metadata{
		EvidenceCommand:         "go test ./...",
		EvidenceCWD:             root,
		EvidenceTimeoutSeconds:  "120",
		EvidenceSideEffectClass: string(evidence.SideEffectReadOnly),
		EvidenceHash:            "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	tests := []struct {
		name     string
		artifact string
	}{
		{name: "absolute", artifact: filepath.Join(root, "reports", "test.json")},
		{name: "traversal", artifact: "../outside/test.json"},
		{name: "symlink escape", artifact: filepath.Join("escape", "test.json")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metadata := base
			metadata.EvidenceArtifact = tt.artifact
			if hasEvidenceReplayContract(metadata) {
				t.Fatal("hasEvidenceReplayContract() = true, want false for uncontained artifact path")
			}
		})
	}
}

func TestEvidenceGateObservedAtUsesDaemonObservedJournalEvent(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}
	manager := journal.NewManager("test-context", os.Getpid())
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	leaseTime := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	if err := manager.Bootstrap(sessionDir, "test", leaseTime); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	filename := "message.md"
	path := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(path, []byte("message"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	fileTime := time.Date(2026, 7, 13, 9, 59, 59, 0, time.UTC)
	if err := os.Chtimes(path, fileTime, fileTime); err != nil {
		t.Fatalf("Chtimes() error = %v", err)
	}
	want := time.Date(2026, 7, 13, 10, 1, 0, 0, time.UTC)

	got := evidenceGateObservedAt(sessionDir, "test", filename, path, want)
	if !got.Equal(want) {
		t.Fatalf("evidenceGateObservedAt() = %s, want %s", got, want)
	}

	later := evidenceGateObservedAt(sessionDir, "test", filename, path, want.Add(time.Hour))
	if !later.Equal(want) {
		t.Fatalf("second evidenceGateObservedAt() = %s, want stable first observation %s", later, want)
	}
}

func TestEvidenceGateObservedBeforeActivationIsNotRetroactivelyActiveWhenEnabledLater(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}
	manager := journal.NewManager("test-context", os.Getpid())
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(sessionDir, "test", time.Date(2026, 7, 13, 9, 59, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	filename := "message.md"
	path := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(path, []byte("message"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	beforeActivation := time.Date(2026, 7, 13, 9, 59, 30, 0, time.UTC)
	activation := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	afterActivation := time.Date(2026, 7, 13, 10, 1, 0, 0, time.UTC)

	observedWhileDisabled := evidenceGateObservedAt(sessionDir, "test", filename, path, beforeActivation)
	if !observedWhileDisabled.Equal(beforeActivation) {
		t.Fatalf("disabled-period observation = %s, want %s", observedWhileDisabled, beforeActivation)
	}

	cfg := &config.Config{
		EvidencePresenceGateEnabled: true,
		EvidencePresenceGateAfter:   activation.Format(time.RFC3339Nano),
	}
	observedAfterEnabled := evidenceGateObservedAt(sessionDir, "test", filename, path, afterActivation)
	if !observedAfterEnabled.Equal(beforeActivation) {
		t.Fatalf("enabled-period observation = %s, want original disabled observation %s", observedAfterEnabled, beforeActivation)
	}
	if cfg.EvidencePresenceGateActiveAt(observedAfterEnabled) {
		t.Fatal("EvidencePresenceGateActiveAt() = true, want false for pre-activation observation")
	}
}

func TestEvidenceGateObservedBeforeActivationSurvivesSessionGenerationRollover(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs() error = %v", err)
	}
	manager := journal.NewManager("test-context", os.Getpid())
	journal.InstallProcessManager(manager)
	t.Cleanup(journal.ClearProcessManager)
	if err := manager.Bootstrap(sessionDir, "test", time.Date(2026, 7, 13, 9, 59, 0, 0, time.UTC)); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	filename := "message.md"
	path := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(path, []byte("message"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	beforeActivation := time.Date(2026, 7, 13, 9, 59, 30, 0, time.UTC)
	activation := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	afterActivation := time.Date(2026, 7, 13, 10, 1, 0, 0, time.UTC)

	observedBeforeRollover := evidenceGateObservedAt(sessionDir, "test", filename, path, beforeActivation)
	if !observedBeforeRollover.Equal(beforeActivation) {
		t.Fatalf("pre-rollover observation = %s, want %s", observedBeforeRollover, beforeActivation)
	}

	restartedManager := journal.NewManager("test-context", os.Getpid()+1)
	journal.InstallProcessManager(restartedManager)
	if err := restartedManager.Bootstrap(sessionDir, "test", afterActivation); err != nil {
		t.Fatalf("Bootstrap() after restart error = %v", err)
	}

	cfg := &config.Config{
		EvidencePresenceGateEnabled: true,
		EvidencePresenceGateAfter:   activation.Format(time.RFC3339Nano),
	}
	observedAfterRollover := evidenceGateObservedAt(sessionDir, "test", filename, path, afterActivation)
	if !observedAfterRollover.Equal(beforeActivation) {
		t.Fatalf("post-rollover observation = %s, want original pre-activation observation %s", observedAfterRollover, beforeActivation)
	}
	if cfg.EvidencePresenceGateActiveAt(observedAfterRollover) {
		t.Fatal("EvidencePresenceGateActiveAt() = true, want false for pre-activation observation after rollover")
	}
}
