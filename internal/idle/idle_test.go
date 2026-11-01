package idle

import (
	"strings"
	"testing"
)

func TestContainsCompactionTrigger(t *testing.T) {
	tests := []struct {
		name    string
		runtime string
		content string
		want    bool
	}{
		{
			name:    "ignores claude compacting line",
			runtime: "claude",
			content: "Compacting conversation history",
			want:    false,
		},
		{
			name:    "ignores claude compacting status line with bullet",
			runtime: "claude",
			content: "• Compacting conversation history",
			want:    false,
		},
		{
			name:    "ignores claude spinner compacting line",
			runtime: "claude",
			content: "✽ Compacting conversation… (28s)",
			want:    false,
		},
		{
			name:    "matches claude compacted completion line",
			runtime: "claude",
			content: "✻ Conversation compacted (ctrl+o for history)",
			want:    true,
		},
		{
			name:    "matches claude compact command result",
			runtime: "claude",
			content: "⎿  Compacted (ctrl+o to see full summary)",
			want:    true,
		},
		{
			name:    "ignores unrelated claude compacting status",
			runtime: "claude",
			content: "✽ Compacting files…",
			want:    false,
		},
		{
			name:    "ignores claude compaction prose",
			runtime: "claude",
			content: "The compaction plan is ready.",
			want:    false,
		},
		{
			name:    "matches codex compacted notice",
			runtime: "codex",
			content: "• Context compacted",
			want:    true,
		},
		{
			name:    "ignores codex compacted prose",
			runtime: "codex",
			content: "I compacted this explanation.",
			want:    false,
		},
		{
			name:    "ignores codex compaction prose",
			runtime: "codex",
			content: "The compaction plan is ready.",
			want:    false,
		},
		{
			name:    "ignores unknown runtime compaction text",
			runtime: "bash",
			content: "Compacting conversation history",
			want:    false,
		},
		{
			name:    "ignores unrelated text",
			runtime: "claude",
			content: "writing response",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsCompactionTrigger(tt.runtime, tt.content); got != tt.want {
				t.Fatalf("containsCompactionTrigger(%q, %q) = %v, want %v", tt.runtime, tt.content, got, tt.want)
			}
		})
	}
}

func TestCompactionTriggerScanBoundsLatestMarkerPrefix(t *testing.T) {
	historyLine := "ordinary retained output " + strings.Repeat("x", 48)
	content := strings.Repeat(historyLine+"\n", 20) + "• Context compacted\npost-compaction output"

	scan := compactionTriggerScan("codex", content)
	if scan.Trigger != "codex:context-compaction" {
		t.Fatalf("compactionTriggerScan() trigger = %q, want codex context compaction", scan.Trigger)
	}
	if scan.MarkerCount != 1 {
		t.Fatalf("compactionTriggerScan() marker count = %d, want 1", scan.MarkerCount)
	}
	if len(scan.LatestMarkerPrefix) > maxCompactionPrefixTailBytes {
		t.Fatalf("compactionTriggerScan() retained %d prefix bytes, want at most %d", len(scan.LatestMarkerPrefix), maxCompactionPrefixTailBytes)
	}
	if !strings.HasSuffix(scan.LatestMarkerPrefix, "• Context compacted") {
		t.Fatalf("compactionTriggerScan() prefix tail = %q, want marker suffix", scan.LatestMarkerPrefix)
	}
}
