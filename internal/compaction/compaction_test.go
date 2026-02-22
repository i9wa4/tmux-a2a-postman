package compaction

import (
	"testing"
)

func TestResolveTailLines(t *testing.T) {
	tests := []struct {
		name      string
		tailLines int
		expected  int
	}{
		{
			name:      "zero falls back to 10",
			tailLines: 0,
			expected:  10,
		},
		{
			name:      "negative falls back to 10",
			tailLines: -5,
			expected:  10,
		},
		{
			name:      "positive value used as-is",
			tailLines: 50,
			expected:  50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := resolveTailLines(tt.tailLines)
			if result != tt.expected {
				t.Errorf("resolveTailLines(%d) = %d, want %d", tt.tailLines, result, tt.expected)
			}
		})
	}
}

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
