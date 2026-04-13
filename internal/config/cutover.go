package config

import "fmt"

type JournalCutoverMode string

const (
	JournalCutoverLegacy             JournalCutoverMode = "legacy"
	JournalCutoverHealthFirst        JournalCutoverMode = "health-first"
	JournalCutoverCompatibilityFirst JournalCutoverMode = "compatibility-first"
)

func JournalHealthCutoverEnabled(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return BoolVal(cfg.JournalHealthCutoverEnabled, false)
}

func JournalCompatibilityCutoverEnabled(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	return BoolVal(cfg.JournalCompatibilityCutoverEnabled, false)
}

func ResolveJournalCutoverMode(cfg *Config) (JournalCutoverMode, error) {
	healthEnabled := JournalHealthCutoverEnabled(cfg)
	compatibilityEnabled := JournalCompatibilityCutoverEnabled(cfg)

	if compatibilityEnabled && !healthEnabled {
		return "", fmt.Errorf("journal_compatibility_cutover_enabled requires journal_health_cutover_enabled")
	}
	switch {
	case compatibilityEnabled:
		return JournalCutoverCompatibilityFirst, nil
	case healthEnabled:
		return JournalCutoverHealthFirst, nil
	default:
		return JournalCutoverLegacy, nil
	}
}
