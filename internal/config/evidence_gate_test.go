package config

import "testing"

func TestEvidencePresenceGateDisabledByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.EvidencePresenceGateActiveFor("2026-07-13T10:00:00Z") {
		t.Fatal("EvidencePresenceGateActiveFor() = true, want false by default")
	}
}

func TestEvidencePresenceGateDoesNotAffectMessagesBeforeActivation(t *testing.T) {
	cfg := &Config{
		EvidencePresenceGateEnabled: true,
		EvidencePresenceGateAfter:   "2026-07-13T10:00:00Z",
	}
	if cfg.EvidencePresenceGateActiveFor("2026-07-13T09:59:59Z") {
		t.Fatal("EvidencePresenceGateActiveFor(before activation) = true, want false")
	}
	if !cfg.EvidencePresenceGateActiveFor("2026-07-13T10:00:00Z") {
		t.Fatal("EvidencePresenceGateActiveFor(at activation) = false, want true")
	}
}
