package idle

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

const loadedPaneCaptureStates = 500

func BenchmarkGetPaneActivityStatus_LoadedPaneCapture500(b *testing.B) {
	tracker := newLoadedPaneCaptureTracker(loadedPaneCaptureStates)
	cfg := config.DefaultConfig()
	cfg.NodeActiveSeconds = 60
	b.ReportAllocs()

	b.ResetTimer()
	b.ReportMetric(loadedPaneCaptureStates, "pane_capture_states")
	for i := 0; i < b.N; i++ {
		statuses := tracker.GetPaneActivityStatus(cfg)
		if len(statuses) != loadedPaneCaptureStates {
			b.Fatalf("pane statuses = %d, want %d", len(statuses), loadedPaneCaptureStates)
		}
	}
}

func BenchmarkExportPaneActivityToFile_LoadedPaneCapture500(b *testing.B) {
	tracker := newLoadedPaneCaptureTracker(loadedPaneCaptureStates)
	cfg := config.DefaultConfig()
	cfg.NodeActiveSeconds = 60
	path := filepath.Join(b.TempDir(), "pane-activity.json")
	b.ReportAllocs()

	b.ResetTimer()
	b.ReportMetric(loadedPaneCaptureStates, "pane_capture_states")
	for i := 0; i < b.N; i++ {
		if err := tracker.ExportPaneActivityToFile(cfg, path); err != nil {
			b.Fatalf("ExportPaneActivityToFile: %v", err)
		}
	}
}

func BenchmarkCompactionTriggerScan_LongHistory(b *testing.B) {
	lines := make([]string, 0, 130)
	for i := 0; i < 96; i++ {
		lines = append(lines, fmt.Sprintf("ordinary retained output %03d %s", i, strings.Repeat("x", 240)))
	}
	lines = append(lines, "• Context compacted")
	for i := 0; i < 32; i++ {
		lines = append(lines, fmt.Sprintf("post-compaction output %03d %s", i, strings.Repeat("y", 180)))
	}
	content := strings.Join(lines, "\n")

	b.ReportAllocs()
	b.SetBytes(int64(len(content)))
	for i := 0; i < b.N; i++ {
		scan := compactionTriggerScan("codex", content)
		if scan.Trigger != "codex:context-compaction" || scan.MarkerCount != 1 {
			b.Fatalf("unexpected scan result: %#v", scan)
		}
	}
}

func newLoadedPaneCaptureTracker(panes int) *IdleTracker {
	tracker := NewIdleTracker()
	now := time.Date(2026, time.May, 21, 9, 30, 0, 0, time.UTC)
	for i := 0; i < panes; i++ {
		paneID := fmt.Sprintf("%%%d", 1000+i)
		tracker.paneCaptureState[paneID] = PaneCaptureState{
			LastHash:              uint32(i + 1),
			LastChangeAt:          now.Add(-time.Duration(i%120) * time.Second),
			ChangeCount:           i % 3,
			LastCaptureAt:         now.Add(-time.Duration(i%30) * time.Second),
			LastCompactionPingAt:  now.Add(-time.Duration(i%300) * time.Second),
			LastCompactionTrigger: "codex:context-compaction",
			LastCompactionHash:    uint32(10000 + i),
			LastCompactionMarkers: i % 5,
			LastCompactionPrefix:  "Context compacted",
		}
	}
	return tracker
}
