package config

import "time"

func (cfg *Config) EvidencePresenceGateActiveFor(messageTimestamp string) bool {
	if cfg == nil || !cfg.EvidencePresenceGateEnabled || cfg.EvidencePresenceGateAfter == "" {
		return false
	}
	activatedAt, err := time.Parse(time.RFC3339, cfg.EvidencePresenceGateAfter)
	if err != nil {
		return false
	}
	messageAt, err := time.Parse(time.RFC3339, messageTimestamp)
	if err != nil {
		return false
	}
	return !messageAt.Before(activatedAt)
}
