package config

import (
	"strings"
	"testing"
)

func TestResolveJournalCutoverMode(t *testing.T) {
	tests := []struct {
		name          string
		cfg           *Config
		wantMode      JournalCutoverMode
		wantErrSubstr string
	}{
		{
			name:     "legacy default",
			cfg:      DefaultConfig(),
			wantMode: JournalCutoverLegacy,
		},
		{
			name: "health first",
			cfg: &Config{
				JournalHealthCutoverEnabled: boolPtr(true),
			},
			wantMode: JournalCutoverHealthFirst,
		},
		{
			name: "compatibility first",
			cfg: &Config{
				JournalHealthCutoverEnabled:        boolPtr(true),
				JournalCompatibilityCutoverEnabled: boolPtr(true),
			},
			wantMode: JournalCutoverCompatibilityFirst,
		},
		{
			name: "reject compatibility without health",
			cfg: &Config{
				JournalCompatibilityCutoverEnabled: boolPtr(true),
			},
			wantErrSubstr: "journal_compatibility_cutover_enabled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveJournalCutoverMode(tc.cfg)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("ResolveJournalCutoverMode() error = nil, want substring %q", tc.wantErrSubstr)
				}
				if got != "" {
					t.Fatalf("ResolveJournalCutoverMode() mode = %q, want empty on error", got)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Fatalf("ResolveJournalCutoverMode() error = %q, want substring %q", err.Error(), tc.wantErrSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveJournalCutoverMode() error = %v", err)
			}
			if got != tc.wantMode {
				t.Fatalf("ResolveJournalCutoverMode() = %q, want %q", got, tc.wantMode)
			}
		})
	}
}
