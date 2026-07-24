package config

import "time"

func (cfg *Config) EvidencePresenceGateActiveAt(observedAt time.Time) bool {
	if cfg == nil || !cfg.EvidencePresenceGateEnabled || cfg.EvidencePresenceGateAfter == "" {
		return false
	}
	activatedAt, err := time.Parse(time.RFC3339, cfg.EvidencePresenceGateAfter)
	if err != nil {
		return false
	}
	return !observedAt.Before(activatedAt)
}
