package config

import (
	"testing"
	"time"
)

func TestEvidencePresenceGateDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.EvidencePresenceGateActiveAt(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("EvidencePresenceGateActiveAt() = true, want false by default")
	}
}

func TestEvidencePresenceGateDoesNotAffectMessagesBeforeActivation(t *testing.T) {
	cfg := &Config{
		EvidencePresenceGateEnabled: true,
		EvidencePresenceGateAfter:   "2026-07-13T10:00:00Z",
	}
	if cfg.EvidencePresenceGateActiveAt(time.Date(2026, 7, 13, 9, 59, 59, 0, time.UTC)) {
		t.Fatal("EvidencePresenceGateActiveAt(before activation) = true, want false")
	}
	if !cfg.EvidencePresenceGateActiveAt(time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("EvidencePresenceGateActiveAt(at activation) = false, want true")
	}
}
