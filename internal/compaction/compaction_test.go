package compaction

import (
	"testing"
)

func TestCheckForCompaction(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		pattern  string
		expected bool
	}{
		{
			name:     "pattern found",
			output:   "some output\nauto-compact\nmore output",
			pattern:  "auto-compact",
			expected: true,
		},
		{
			name:     "pattern not found",
			output:   "some output\nno match here\nmore output",
			pattern:  "auto-compact",
			expected: false,
		},
		{
			name:     "empty pattern",
			output:   "some output\nauto-compact\nmore output",
			pattern:  "",
			expected: false,
		},
		{
			name:     "empty output",
			output:   "",
			pattern:  "auto-compact",
			expected: false,
		},
		{
			name:     "partial match",
			output:   "compaction started: auto-compact mode enabled",
			pattern:  "auto-compact",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkForCompaction(tt.output, tt.pattern)
			if result != tt.expected {
				t.Errorf("checkForCompaction() = %v, want %v", result, tt.expected)
			}
		})
	}
}
