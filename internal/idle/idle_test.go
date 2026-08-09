package idle

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestUpdateActivity(t *testing.T) {
	now := time.Date(2026, time.May, 21, 1, 2, 3, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	nodeKey := "test-session:test-node"

	// Test UpdateSendActivity
	tracker.UpdateSendActivity(nodeKey)

	tracker.mu.Lock()
	activity := tracker.nodeActivity[nodeKey]
	tracker.mu.Unlock()

	if activity.LastSent.IsZero() {
		t.Fatalf("send activity not recorded for %s", nodeKey)
	}

	if !activity.LastSent.Equal(now) {
		t.Errorf("send activity time %v, want %v", activity.LastSent, now)
	}

	// Test UpdateReceiveActivity
	now = now.Add(5 * time.Second)
	tracker.UpdateReceiveActivity(nodeKey)

	tracker.mu.Lock()
	activity2 := tracker.nodeActivity[nodeKey]
	tracker.mu.Unlock()

	if activity2.LastReceived.IsZero() {
		t.Fatalf("receive activity not recorded for %s", nodeKey)
	}

	if !activity2.LastReceived.Equal(now) {
		t.Errorf("receive activity time %v, want %v", activity2.LastReceived, now)
	}
}

// Issue #123: Test for ExportPaneActivityToFile — verifies new JSON schema (struct format)
func TestExportPaneActivityToFile(t *testing.T) {
	now := time.Date(2026, time.May, 21, 2, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
	}

	// Set up pane states
	tracker.mu.Lock()
	tracker.paneCaptureState["%20"] = PaneCaptureState{
		LastHash:      111,
		LastChangeAt:  now.Add(-10 * time.Second), // active
		ChangeCount:   0,
		LastCaptureAt: now,
	}
	tracker.paneCaptureState["%21"] = PaneCaptureState{
		LastHash:      222,
		LastChangeAt:  now.Add(-500 * time.Second), // idle: beyond active threshold
		ChangeCount:   0,
		LastCaptureAt: now,
	}
	tracker.mu.Unlock()

	tmpFile := filepath.Join(t.TempDir(), "pane-activity.json")
	if err := tracker.ExportPaneActivityToFile(cfg, tmpFile); err != nil {
		t.Fatalf("ExportPaneActivityToFile failed: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("reading exported file: %v", err)
	}

	// Verify new struct format (not plain string)
	var exported map[string]PaneActivityExport
	if err := json.Unmarshal(data, &exported); err != nil {
		t.Fatalf("unmarshaling as map[string]PaneActivityExport: %v", err)
	}

	pane20, ok := exported["%20"]
	if !ok {
		t.Fatal("expected %%20 in exported data")
	}
	if pane20.Status != "active" {
		t.Errorf("expected %%20 status 'active', got %q", pane20.Status)
	}
	if pane20.LastChangeAt.IsZero() {
		t.Errorf("expected %%20 LastChangeAt to be set")
	}
	if pane20.LastCaptureAt.IsZero() {
		t.Errorf("expected %%20 LastCaptureAt to be set")
	}
	if pane20.ScreenFingerprint != "0000006f" {
		t.Errorf("expected %%20 ScreenFingerprint '0000006f', got %q", pane20.ScreenFingerprint)
	}

	pane21, ok := exported["%21"]
	if !ok {
		t.Fatal("expected %%21 in exported data")
	}
	if pane21.Status != "idle" {
		t.Errorf("expected %%21 status 'idle', got %q", pane21.Status)
	}
	if pane21.ScreenFingerprint != "000000de" {
		t.Errorf("expected %%21 ScreenFingerprint '000000de', got %q", pane21.ScreenFingerprint)
	}
}

// Issue #122: Tests for GetPaneActivityStatus
func TestGetPaneActivityStatus_ChangeCountZeroAfterActive(t *testing.T) {
	// Bug case: ChangeCount==0 (reset after 2 consecutive changes) but LastChangeAt is recent.
	// Before fix: returned "stale". After fix: returns "active".
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
	}
	now := time.Now()
	tracker.mu.Lock()
	tracker.paneCaptureState["%10"] = PaneCaptureState{
		LastHash:      12345,
		LastChangeAt:  now.Add(-10 * time.Second), // Recent change
		ChangeCount:   0,                          // Reset after marking active
		LastCaptureAt: now,
	}
	tracker.mu.Unlock()

	result := tracker.GetPaneActivityStatus(cfg)
	if result["%10"] != "active" {
		t.Errorf("expected 'active' for recent LastChangeAt with ChangeCount==0, got %q", result["%10"])
	}
}

func TestGetPaneActivityStatus_StaleWhenLastChangeAtZero(t *testing.T) {
	// Pane just initialized: LastChangeAt is zero -> stale.
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
	}
	tracker.mu.Lock()
	tracker.paneCaptureState["%11"] = PaneCaptureState{
		LastHash:      0,
		LastChangeAt:  time.Time{}, // Zero
		ChangeCount:   1,
		LastCaptureAt: time.Now(),
	}
	tracker.mu.Unlock()

	result := tracker.GetPaneActivityStatus(cfg)
	if result["%11"] != "stale" {
		t.Errorf("expected 'stale' for zero LastChangeAt, got %q", result["%11"])
	}
}

func TestGetPaneActivityStatus_IdlePane(t *testing.T) {
	// LastChangeAt older than active threshold -> "idle".
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 60.0,
	}
	now := time.Now()
	tracker.mu.Lock()
	tracker.paneCaptureState["%12"] = PaneCaptureState{
		LastHash:      999,
		LastChangeAt:  now.Add(-120 * time.Second), // 2 min ago: beyond active (60s)
		ChangeCount:   0,
		LastCaptureAt: now,
	}
	tracker.mu.Unlock()

	result := tracker.GetPaneActivityStatus(cfg)
	if result["%12"] != "idle" {
		t.Errorf("expected 'idle', got %q", result["%12"])
	}
}

func TestGetPaneActivityStatus_LongUnchangedLivePaneStaysIdle(t *testing.T) {
	// A live pane with no recent screen change should stay idle, not stale.
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 60.0,
	}
	now := time.Now()
	tracker.mu.Lock()
	tracker.paneCaptureState["%13"] = PaneCaptureState{
		LastHash:      111,
		LastChangeAt:  now.Add(-700 * time.Second), // long after active threshold
		ChangeCount:   0,
		LastCaptureAt: now,
	}
	tracker.mu.Unlock()

	result := tracker.GetPaneActivityStatus(cfg)
	if result["%13"] != "idle" {
		t.Errorf("expected 'idle' for old LastChangeAt on live pane, got %q", result["%13"])
	}
}

func TestGetPaneActivityStatus_EmptyState(t *testing.T) {
	// No pane capture state -> empty result.
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
	}
	result := tracker.GetPaneActivityStatus(cfg)
	if len(result) != 0 {
		t.Errorf("expected empty result for no pane state, got %v", result)
	}
}

func TestGetLivenessMap(t *testing.T) {
	tracker := NewIdleTracker()

	// Initially empty
	result := tracker.GetLivenessMap()
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}

	// Mark nodes alive
	tracker.MarkNodeAlive("session1:nodeA")
	tracker.MarkNodeAlive("session1:nodeB")
	tracker.UpdateSendActivity("session1:nodeC") // No liveness confirmed

	result = tracker.GetLivenessMap()
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
	if !result["session1:nodeA"] || !result["session1:nodeB"] {
		t.Errorf("expected nodeA and nodeB, got %v", result)
	}
	if result["session1:nodeC"] {
		t.Errorf("nodeC should not be in liveness map (no liveness confirmed)")
	}
}

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

func TestFilterPaneCaptureNodes_PreservesSessionPrefixedKeys(t *testing.T) {
	filtered := filterPaneCaptureNodes(map[string]discovery.NodeInfo{
		"dotfiles:messenger":    {},
		"dotfiles:orchestrator": {},
		"review:critic":         {},
	}, map[string]bool{
		"dotfiles:messenger":    true,
		"dotfiles:orchestrator": true,
	})

	if _, ok := filtered["dotfiles:messenger"]; !ok {
		t.Fatal("expected session-prefixed sender node to remain after edge filtering")
	}
	if _, ok := filtered["dotfiles:orchestrator"]; !ok {
		t.Fatal("expected session-prefixed recipient node to remain after edge filtering")
	}
	if _, ok := filtered["review:critic"]; ok {
		t.Fatal("unexpected unrelated node remained after edge filtering")
	}
}

func TestFilterPaneCaptureNodes_PreservesBareKeys(t *testing.T) {
	filtered := filterPaneCaptureNodes(map[string]discovery.NodeInfo{
		"dotfiles:messenger":    {},
		"dotfiles:orchestrator": {},
		"review:critic":         {},
	}, map[string]bool{
		"messenger":    true,
		"orchestrator": true,
	})

	if _, ok := filtered["dotfiles:messenger"]; !ok {
		t.Fatal("expected bare-edge sender node to remain after edge filtering")
	}
	if _, ok := filtered["dotfiles:orchestrator"]; !ok {
		t.Fatal("expected bare-edge recipient node to remain after edge filtering")
	}
	if _, ok := filtered["review:critic"]; ok {
		t.Fatal("unexpected unrelated node remained after edge filtering")
	}
}

func TestCheckPaneCaptureUsesInjectedClockForPaneTimestamps(t *testing.T) {
	scriptDir := t.TempDir()
	capturePath := filepath.Join(scriptDir, "capture.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_CAPTURE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_CAPTURE", capturePath)

	now := time.Date(2026, time.May, 21, 3, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  filepath.Join(t.TempDir(), "review"),
		},
	}

	if err := os.WriteFile(capturePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture ready): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(targets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if !state.LastChangeAt.Equal(now) {
		t.Fatalf("initial LastChangeAt = %v, want %v", state.LastChangeAt, now)
	}
	if !state.LastCaptureAt.Equal(now) {
		t.Fatalf("initial LastCaptureAt = %v, want %v", state.LastCaptureAt, now)
	}

	now = now.Add(10 * time.Second)
	if err := os.WriteFile(capturePath, []byte("working"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture working): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0", len(targets))
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if !state.LastChangeAt.Equal(now) {
		t.Fatalf("changed LastChangeAt = %v, want %v", state.LastChangeAt, now)
	}
	if !state.LastCaptureAt.Equal(now) {
		t.Fatalf("changed LastCaptureAt = %v, want %v", state.LastCaptureAt, now)
	}

	now = now.Add(10 * time.Second)
	if err := os.WriteFile(capturePath, []byte("working again"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture working again): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("third checkPaneCapture() returned %d targets, want 0", len(targets))
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	activity := tracker.nodeActivity["review:worker"]
	tracker.mu.Unlock()
	if !state.LastChangeAt.Equal(now) {
		t.Fatalf("active LastChangeAt = %v, want %v", state.LastChangeAt, now)
	}
	if !state.LastCaptureAt.Equal(now) {
		t.Fatalf("active LastCaptureAt = %v, want %v", state.LastCaptureAt, now)
	}
	if !activity.LastScreenChange.Equal(now) {
		t.Fatalf("LastScreenChange = %v, want %v", activity.LastScreenChange, now)
	}
}

func TestCheckPaneCapture_CompactionTriggerReturnsDetectedNodeForInitialMarker(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '✻ Conversation compacted (ctrl+o for history)'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 1 {
		t.Fatalf("checkPaneCapture() returned %d targets, want 1 for an already-visible initial marker", len(targets))
	}
	if targets[0].NodeKey != "review:worker" {
		t.Fatalf("checkPaneCapture() target = %q, want %q", targets[0].NodeKey, "review:worker")
	}
	if targets[0].Runtime != "claude" {
		t.Fatalf("checkPaneCapture() runtime = %q, want %q", targets[0].Runtime, "claude")
	}
	if targets[0].Trigger != "claude:conversation-compaction" {
		t.Fatalf("checkPaneCapture() trigger = %q, want %q", targets[0].Trigger, "claude:conversation-compaction")
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionTrigger == "" {
		t.Fatal("checkPaneCapture() did not record the initial compaction trigger")
	}
	if state.LastCompactionPingAt.IsZero() {
		t.Fatal("checkPaneCapture() did not record a ping timestamp for an initial marker")
	}
	if state.LastCompactionHash != state.LastHash {
		t.Fatal("checkPaneCapture() did not record the initial compaction pane hash")
	}
}

func TestCheckPaneCapture_CompactionTriggerReturnsDetectedNodeAfterInitialCapture(t *testing.T) {
	scriptDir := t.TempDir()
	capturePath := filepath.Join(scriptDir, "capture.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_CAPTURE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_CAPTURE", capturePath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(capturePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	if err := os.WriteFile(capturePath, []byte("✻ Conversation compacted (ctrl+o for history)"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture marker): %v", err)
	}
	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 1 {
		t.Fatalf("checkPaneCapture() returned %d targets, want 1", len(targets))
	}
	if targets[0].NodeKey != "review:worker" {
		t.Fatalf("checkPaneCapture() target = %q, want %q", targets[0].NodeKey, "review:worker")
	}
	if targets[0].Runtime != "claude" {
		t.Fatalf("checkPaneCapture() runtime = %q, want %q", targets[0].Runtime, "claude")
	}
	if targets[0].Trigger != "claude:conversation-compaction" {
		t.Fatalf("checkPaneCapture() trigger = %q, want %q", targets[0].Trigger, "claude:conversation-compaction")
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionPingAt.IsZero() {
		t.Fatal("checkPaneCapture() did not record the compaction-triggered ping timestamp")
	}
	if state.LastCompactionHash != state.LastHash {
		t.Fatal("checkPaneCapture() did not record the compaction-triggered pane hash")
	}
}

func TestCheckPaneCapture_CompactionTriggerUsesRecentHistory(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-100' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  100,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible ready): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(history ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	visibleContent := "latest prompt"
	if err := os.WriteFile(visiblePath, []byte(visibleContent), 0o644); err != nil {
		t.Fatalf("WriteFile(visible latest): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("older\n• Context compacted\n"+visibleContent), 0o644); err != nil {
		t.Fatalf("WriteFile(history compacted): %v", err)
	}

	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 1 {
		t.Fatalf("checkPaneCapture() returned %d targets, want 1", len(targets))
	}
	if targets[0].NodeKey != "review:worker" {
		t.Fatalf("checkPaneCapture() target = %q, want %q", targets[0].NodeKey, "review:worker")
	}
	if targets[0].Runtime != "codex" {
		t.Fatalf("checkPaneCapture() runtime = %q, want %q", targets[0].Runtime, "codex")
	}
	if targets[0].Trigger != "codex:context-compaction" {
		t.Fatalf("checkPaneCapture() trigger = %q, want %q", targets[0].Trigger, "codex:context-compaction")
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(visibleContent) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionPingAt.IsZero() {
		t.Fatal("checkPaneCapture() did not record the compaction-triggered ping timestamp")
	}
}

func TestCheckPaneCapture_CompactionTriggerFallsBackToFullHistoryWhenMarkerOutrunsTail(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible ready): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(recent ready): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(history ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	visibleContent := "latest prompt"
	recentContent := "post output 4\npost output 5\n" + visibleContent
	fullHistory := "older\n✻ Conversation compacted (ctrl+o for history)\npost output 1\npost output 2\npost output 3\n" + recentContent
	if err := os.WriteFile(visiblePath, []byte(visibleContent), 0o644); err != nil {
		t.Fatalf("WriteFile(visible latest): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(recentContent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent without marker): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(fullHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history with marker): %v", err)
	}

	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 1 {
		t.Fatalf("checkPaneCapture() returned %d targets, want 1 for marker outside recent tail", len(targets))
	}
	if targets[0].NodeKey != "review:worker" {
		t.Fatalf("checkPaneCapture() target = %q, want %q", targets[0].NodeKey, "review:worker")
	}
	if targets[0].Runtime != "claude" {
		t.Fatalf("checkPaneCapture() runtime = %q, want %q", targets[0].Runtime, "claude")
	}
	if targets[0].Trigger != "claude:conversation-compaction" {
		t.Fatalf("checkPaneCapture() trigger = %q, want %q", targets[0].Trigger, "claude:conversation-compaction")
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(visibleContent) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionHash != hashContentCRC32(fullHistory) {
		t.Fatal("checkPaneCapture() did not record the full-history compaction hash")
	}
}

func TestCheckPaneCapture_CompactionTriggerSkipsFullHistoryForUnsupportedRuntime(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyCalledPath := filepath.Join(scriptDir, "history-called")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tzsh'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  : > \"$TMUX_A2A_TEST_HISTORY_CALLED\"\n" +
		"  printf '%s\\n' '✻ Conversation compacted (ctrl+o for history)'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY_CALLED", historyCalledPath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("latest prompt"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte("recent output without marker"), 0o644); err != nil {
		t.Fatalf("WriteFile(recent): %v", err)
	}

	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 0 {
		t.Fatalf("checkPaneCapture() returned %d targets for unsupported runtime, want 0", len(targets))
	}
	if _, err := os.Stat(historyCalledPath); !os.IsNotExist(err) {
		t.Fatalf("full-history capture was invoked for unsupported runtime; stat err=%v", err)
	}
}

func TestCheckPaneCapture_CompactionTriggerSkipsFullHistoryForUnchangedSupportedRuntime(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyCalledPath := filepath.Join(scriptDir, "history-called")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  : >> \"$TMUX_A2A_TEST_HISTORY_CALLED\"\n" +
		"  printf '%s\\n' 'history without compaction marker'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY_CALLED", historyCalledPath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("latest prompt"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte("recent output without marker"), 0o644); err != nil {
		t.Fatalf("WriteFile(recent): %v", err)
	}

	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 0 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 0", len(firstTargets))
	}
	if _, err := os.Stat(historyCalledPath); err != nil {
		t.Fatalf("first checkPaneCapture() did not invoke initial full-history scan; stat err=%v", err)
	}
	if err := os.Remove(historyCalledPath); err != nil {
		t.Fatalf("Remove(history-called): %v", err)
	}

	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0", len(secondTargets))
	}
	if _, err := os.Stat(historyCalledPath); !os.IsNotExist(err) {
		t.Fatalf("full-history capture was invoked for unchanged supported runtime; stat err=%v", err)
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotRepeatWhenTailMarkerLaterSeenInFullHistory(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	firstVisible := "post output 1"
	firstRecent := "✻ Conversation compacted (ctrl+o for history)\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(firstRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("unused history"), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for recent-tail marker", len(firstTargets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	secondVisible := "post output 2"
	secondRecent := "post output 1\n" + secondVisible
	secondHistory := "older prefix outside tail\n✻ Conversation compacted (ctrl+o for history)\n" + secondRecent
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(secondRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}

	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0 when the same marker moves from recent tail to full-history fallback", len(secondTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(secondVisible) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenRecentMarkerReplacedBySingleFullHistoryFallbackMarker(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	firstVisible := "after recent marker A"
	firstRecent := "• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(firstRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("unused history"), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for recent-tail marker A", len(firstTargets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	secondVisible := "after history marker B"
	secondRecent := "ordinary tail\n" + secondVisible
	secondHistory := "retained replacement context\n• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(secondRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 for full-history marker B replacing recent marker A", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:worker" {
		t.Fatalf("second checkPaneCapture() target = %q, want %q", secondTargets[0].NodeKey, "review:worker")
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenRecentMarkerReplacedBySingleFullHistoryFallbackMarkerWithRepeatedSuffixPrefix(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	sharedPostMarkerPrefix := strings.Repeat("x", 300)
	firstVisible := sharedPostMarkerPrefix + " marker A tail"
	firstRecent := "• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(firstRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("unused history"), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for recent-tail marker A", len(firstTargets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	secondVisible := sharedPostMarkerPrefix + " marker B tail"
	secondRecent := "ordinary tail\n" + secondVisible
	secondHistory := "retained replacement context\n• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(secondRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 when marker B repeats marker A's first post-marker bytes", len(secondTargets))
	}
}

func TestCheckPaneCapture_CompactionTriggerRetainsBoundedSuffixIdentityForLargeHistory(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	largeSuffix := strings.Repeat("large-history-line\n", 4096)
	if err := os.WriteFile(visiblePath, []byte("visible tail"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte("ordinary recent tail\nvisible tail"), 0o644); err != nil {
		t.Fatalf("WriteFile(recent): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("retained prefix\n• Context compacted\n"+largeSuffix), 0o644); err != nil {
		t.Fatalf("WriteFile(history): %v", err)
	}

	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 1 {
		t.Fatalf("checkPaneCapture() returned %d targets, want 1 for full-history marker", len(targets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	memory := tracker.nodeCompactionMemory["review:worker"]
	tracker.mu.Unlock()

	wantSuffixLength := len("\n" + largeSuffix)
	for name, suffix := range map[string]compactionSuffixIdentity{
		"pane state":  state.LastCompactionSuffix,
		"node memory": memory.LastCompactionSuffix,
	} {
		if suffix.Length != wantSuffixLength {
			t.Fatalf("%s suffix length = %d, want %d", name, suffix.Length, wantSuffixLength)
		}
		if len(suffix.Head) > maxCompactionSuffixWindowBytes {
			t.Fatalf("%s suffix head retained %d bytes, want <= %d", name, len(suffix.Head), maxCompactionSuffixWindowBytes)
		}
		if len(suffix.Tail) > maxCompactionSuffixWindowBytes {
			t.Fatalf("%s suffix tail retained %d bytes, want <= %d", name, len(suffix.Tail), maxCompactionSuffixWindowBytes)
		}
		if retained := len(suffix.Head) + len(suffix.Tail); retained > 2*maxCompactionSuffixWindowBytes {
			t.Fatalf("%s retained %d suffix bytes, want <= %d", name, retained, 2*maxCompactionSuffixWindowBytes)
		}
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenSingleMarkerFullHistoryFallbackReplacesOldMarker(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	firstVisible := "after first fallback compaction"
	firstRecent := "ordinary tail\n" + firstVisible
	firstHistory := "older retained A\n• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(firstRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(firstHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for full-history fallback marker A", len(firstTargets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	secondVisible := "after second fallback compaction"
	secondRecent := "ordinary tail\n" + secondVisible
	secondHistory := "new retained B\n• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(secondRecent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 for full-history fallback marker B replacing A", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:worker" {
		t.Fatalf("second checkPaneCapture() target = %q, want %q", secondTargets[0].NodeKey, "review:worker")
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionHash != hashContentCRC32(secondHistory) {
		t.Fatal("checkPaneCapture() did not record marker B's full-history compaction hash")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
}

func TestCheckPaneCapture_CompactionTriggerKeepsFullHistoryMemoryAcrossTailOnlyAbsence(t *testing.T) {
	scriptDir := t.TempDir()
	listPath := filepath.Join(scriptDir, "list.txt")
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	historyCalledPath := filepath.Join(scriptDir, "history-called")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_LIST\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  : >> \"$TMUX_A2A_TEST_HISTORY_CALLED\"\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_LIST", listPath)
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY_CALLED", historyCalledPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      1,
		PaneCaptureTailLines:  3,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	visible := "after fallback compaction"
	recent := "ordinary tail\n" + visible
	history := "older retained context\n• Context compacted\n" + visible
	if err := os.WriteFile(listPath, []byte("%11\tcodex\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list first): %v", err)
	}
	if err := os.WriteFile(visiblePath, []byte(visible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte(recent), 0o644); err != nil {
		t.Fatalf("WriteFile(recent): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(history), 0o644); err != nil {
		t.Fatalf("WriteFile(history): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for full-history fallback marker", len(firstTargets))
	}

	if err := os.Remove(historyCalledPath); err != nil {
		t.Fatalf("Remove(history-called): %v", err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0 for unchanged tail-only absence", len(secondTargets))
	}
	if _, err := os.Stat(historyCalledPath); !os.IsNotExist(err) {
		t.Fatalf("unchanged poll invoked full-history capture; stat err=%v", err)
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	_, memoryExists := tracker.nodeCompactionMemory["review:worker"]
	tracker.mu.Unlock()
	if state.LastCompactionTrigger == "" {
		t.Fatal("tail-only absence cleared full-history compaction state")
	}
	if !memoryExists {
		t.Fatal("tail-only absence cleared node compaction memory learned from full-history fallback")
	}

	now = now.Add(2 * time.Second)
	if err := os.WriteFile(listPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(list empty): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("stale-prune checkPaneCapture() returned %d targets, want 0", len(targets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(listPath, []byte("%11\tcodex\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list reappear): %v", err)
	}
	reappearedTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(reappearedTargets) != 0 {
		t.Fatalf("reappeared checkPaneCapture() returned %d targets, want 0 because retained node memory dedupes the same fallback marker", len(reappearedTargets))
	}
}

func TestCheckPaneCapture_RetriesFullHistoryAfterTransientCaptureFailure(t *testing.T) {
	scriptDir := t.TempDir()
	listPath := filepath.Join(scriptDir, "list.txt")
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	attemptsPath := filepath.Join(scriptDir, "history-attempts")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_LIST\"\n  exit 0\nfi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-3' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_RECENT\"\n  exit 0\nfi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then\n" +
		"  attempts=0\n  if [ -f \"$TMUX_A2A_TEST_HISTORY_ATTEMPTS\" ]; then attempts=$(cat \"$TMUX_A2A_TEST_HISTORY_ATTEMPTS\"); fi\n  attempts=$((attempts + 1))\n  printf '%s\\n' \"$attempts\" > \"$TMUX_A2A_TEST_HISTORY_ATTEMPTS\"\n  if [ \"$attempts\" -eq 1 ]; then exit 1; fi\n  cat \"$TMUX_A2A_TEST_HISTORY\"\n  exit 0\nfi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n  exit 0\nfi\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_LIST", listPath)
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY_ATTEMPTS", attemptsPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600, PaneCaptureTailLines: 3}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{"review:worker": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir}}
	if err := os.WriteFile(listPath, []byte("%11\tcodex\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list): %v", err)
	}
	visible := "unchanged visible content"
	if err := os.WriteFile(visiblePath, []byte(visible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible): %v", err)
	}
	if err := os.WriteFile(recentPath, []byte("ordinary tail\n"+visible), 0o644); err != nil {
		t.Fatalf("WriteFile(recent): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("older retained context\n• Context compacted\n"+visible), 0o644); err != nil {
		t.Fatalf("WriteFile(history): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 0 after transient history failure", len(targets))
	}
	now = now.Add(compactionPingCooldown + time.Second)
	targets := tracker.checkPaneCapture(cfg, nodes)
	if len(targets) != 1 || targets[0].NodeKey != "review:worker" {
		tracker.mu.Lock()
		state := tracker.paneCaptureState["%11"]
		tracker.mu.Unlock()
		t.Fatalf("second checkPaneCapture() targets = %#v, state=%+v, want one review:worker target after retry", targets, state)
	}
	if got, err := os.ReadFile(attemptsPath); err != nil || strings.TrimSpace(string(got)) != "2" {
		t.Fatalf("history attempts = %q, err=%v, want 2", got, err)
	}
}

func TestShouldPingCompaction_SuppressesDeliveredSuffixRefreshAfterCooldown(t *testing.T) {
	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	first := codexCompactionTriggerScan("older context\n• Context compacted\nfirst output")
	second := codexCompactionTriggerScan("older context\n• Context compacted\nfirst output\nsecond output")
	state := PaneCaptureState{}
	if !shouldPingCompaction(state, first, hashContentCRC32("first"), compactionScopeHistory, now) {
		t.Fatal("initial marker was not emitted")
	}
	recordCompactionPing(&state, first, hashContentCRC32("first"), compactionScopeHistory, now)
	state.LastCompactionDeliveredIdentity = state.LastCompactionMarkerIdentity
	state.LastCompactionDeliveredKey = compactionStateDeliveryKey(state)
	now = now.Add(compactionPingCooldown + time.Second)
	state.LastCompactionMarkerIdentity = compactionMarkerIdentity(second)
	state.LastCompactionSuffix = second.LatestMarkerSuffixID
	if shouldPingCompaction(state, second, hashContentCRC32("second"), compactionScopeHistory, now) {
		t.Fatal("suffix-only refresh of a delivered marker emitted a duplicate after cooldown")
	}
}

func TestShouldPingCompaction_DeliveredKeyDoesNotCollapseDistinctHistoryWindows(t *testing.T) {
	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	first := codexCompactionTriggerScan("prefix one\n• Context compacted\noutput")
	distinct := codexCompactionTriggerScan("prefix two\n• Context compacted\noutput")
	state := PaneCaptureState{LastCompactionTrigger: first.Trigger, LastCompactionMarkerHash: first.MarkerLineHash, LastCompactionMarkers: first.MarkerCount, LastCompactionPrefixHash: first.MarkerPrefixHash, LastCompactionPrefixLines: first.MarkerPrefixLines, LastCompactionScope: compactionScopeHistory, LastCompactionHash: hashContentCRC32("window one"), LastCompactionPingAt: now.Add(-compactionPingCooldown - time.Second)}
	state.LastCompactionDeliveredKey = compactionStateDeliveryKey(state)
	if !shouldPingCompaction(state, distinct, hashContentCRC32("window two"), compactionScopeHistory, now) {
		t.Fatal("distinct history window was suppressed by a collapsed delivered key")
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotRepeatSameMarkerAfterStalePrune(t *testing.T) {
	scriptDir := t.TempDir()
	listPath := filepath.Join(scriptDir, "list.txt")
	capturePath := filepath.Join(scriptDir, "capture.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_LIST\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_CAPTURE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_LIST", listPath)
	t.Setenv("TMUX_A2A_TEST_CAPTURE", capturePath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      1,
		PaneCaptureTailLines:  0,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	marker := "✻ Conversation compacted (ctrl+o for history)"
	if err := os.WriteFile(listPath, []byte("%11\tclaude\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list visible): %v", err)
	}
	if err := os.WriteFile(capturePath, []byte(marker), 0o644); err != nil {
		t.Fatalf("WriteFile(capture marker): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}

	now = now.Add(2 * time.Second)
	if err := os.WriteFile(listPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(list empty): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("stale-prune checkPaneCapture() returned %d targets, want 0", len(targets))
	}
	tracker.mu.Lock()
	_, paneStateExists := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if paneStateExists {
		t.Fatal("checkPaneCapture() did not stale-prune pane state")
	}

	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(listPath, []byte("%11\tclaude\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list visible again): %v", err)
	}
	reobservedTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(reobservedTargets) != 0 {
		t.Fatalf("reobserved checkPaneCapture() returned %d targets, want 0 for already-handled marker", len(reobservedTargets))
	}
	// Once per-node memory expires, the same marker is a fresh event and emits once.
	now = now.Add(compactionMemoryRetention + time.Second)
	tracker.mu.Lock()
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 1 {
		t.Fatalf("post-retention reobserved targets = %d, want 1", len(targets))
	}
}

func TestCheckPaneCapture_NonAuthoritativeCompactionAbsencePreservesMemoryAndSuppressesReplay(t *testing.T) {
	scriptDir := t.TempDir()
	listPath := filepath.Join(scriptDir, "list.txt")
	capturePath := filepath.Join(scriptDir, "capture.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_LIST\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_CAPTURE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_LIST", listPath)
	t.Setenv("TMUX_A2A_TEST_CAPTURE", capturePath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      1,
		PaneCaptureTailLines:  0,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	marker := "✻ Conversation compacted (ctrl+o for history)"
	if err := os.WriteFile(listPath, []byte("%11\tclaude\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list visible): %v", err)
	}
	if err := os.WriteFile(capturePath, []byte(marker), 0o644); err != nil {
		t.Fatalf("WriteFile(capture marker): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}

	if err := os.WriteFile(capturePath, []byte("ready after compaction"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture cleared): %v", err)
	}
	clearedTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(clearedTargets) != 0 {
		t.Fatalf("cleared checkPaneCapture() returned %d targets, want 0", len(clearedTargets))
	}
	tracker.mu.Lock()
	_, memoryExists := tracker.nodeCompactionMemory["review:worker"]
	tracker.mu.Unlock()
	if !memoryExists {
		t.Fatal("checkPaneCapture() cleared node compaction memory after non-authoritative marker absence")
	}

	now = now.Add(2 * time.Second)
	if err := os.WriteFile(listPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile(list empty): %v", err)
	}
	if targets := tracker.checkPaneCapture(cfg, nodes); len(targets) != 0 {
		t.Fatalf("stale-prune checkPaneCapture() returned %d targets, want 0", len(targets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(listPath, []byte("%11\tclaude\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(list visible again): %v", err)
	}
	if err := os.WriteFile(capturePath, []byte(marker), 0o644); err != nil {
		t.Fatalf("WriteFile(capture fresh marker): %v", err)
	}
	freshTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(freshTargets) != 0 {
		t.Fatalf("fresh checkPaneCapture() returned %d targets, want 0 after non-authoritative absence", len(freshTargets))
	}
}

func TestPruneNodeCompactionMemoryDropsExpiredEntries(t *testing.T) {
	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	tracker.nodeCompactionMemory["review:old"] = PaneCaptureState{
		LastCompactionPingAt:  now.Add(-compactionMemoryRetention - time.Second),
		LastCompactionTrigger: "claude:conversation-compaction",
	}
	tracker.nodeCompactionMemory["review:recent"] = PaneCaptureState{
		LastCompactionPingAt:  now.Add(-compactionMemoryRetention + time.Second),
		LastCompactionTrigger: "claude:conversation-compaction",
	}

	tracker.pruneNodeCompactionMemory(now)

	if _, ok := tracker.nodeCompactionMemory["review:old"]; ok {
		t.Fatal("pruneNodeCompactionMemory kept expired node compaction memory")
	}
	if _, ok := tracker.nodeCompactionMemory["review:recent"]; !ok {
		t.Fatal("pruneNodeCompactionMemory deleted recent node compaction memory")
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenNewerHistoryMarkerAppearsAfterCooldown(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-100' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	now := time.Date(2026, time.May, 21, 4, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  100,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible ready): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(history ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	firstVisible := "after first compaction"
	firstHistory := "older\n• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(firstHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	if state.LastCompactionTrigger == "" {
		t.Fatal("first checkPaneCapture() did not leave compaction trigger set")
	}
	if !state.LastCompactionPingAt.Equal(now) {
		t.Fatalf("first LastCompactionPingAt = %v, want %v", state.LastCompactionPingAt, now)
	}
	tracker.mu.Unlock()

	secondVisible := "after second compaction"
	secondHistory := "older\n• Context compacted\nwork after first marker\n• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}

	withinCooldownTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(withinCooldownTargets) != 0 {
		t.Fatalf("within-cooldown checkPaneCapture() returned %d targets, want 0", len(withinCooldownTargets))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 for newer compaction marker in retained history", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:worker" {
		t.Fatalf("second checkPaneCapture() target = %q, want %q", secondTargets[0].NodeKey, "review:worker")
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(secondVisible) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionHash != hashContentCRC32(secondHistory) {
		t.Fatal("checkPaneCapture() did not record the newer compaction history hash")
	}
	if state.LastCompactionMarkers != 2 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers, want 2", state.LastCompactionMarkers)
	}
	if !state.LastCompactionPingAt.Equal(now) {
		t.Fatalf("second LastCompactionPingAt = %v, want %v", state.LastCompactionPingAt, now)
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenNewerSingleHistoryMarkerReplacesOldMarker(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-100' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  100,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible ready): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(history ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	firstVisible := "after first compaction"
	firstHistory := "older retained context\n• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(firstHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	if state.LastCompactionTrigger == "" {
		t.Fatal("first checkPaneCapture() did not leave compaction trigger set")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("first checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
	state.LastCompactionPingAt = time.Now().Add(-compactionPingCooldown - time.Second)
	tracker.paneCaptureState["%11"] = state
	tracker.mu.Unlock()

	secondVisible := "after second compaction"
	secondHistory := "work after first marker retained in finite tail\n• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 for a newer single compaction marker replacing the old marker", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:worker" {
		t.Fatalf("second checkPaneCapture() target = %q, want %q", secondTargets[0].NodeKey, "review:worker")
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(secondVisible) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionHash != hashContentCRC32(secondHistory) {
		t.Fatal("checkPaneCapture() did not record the replacement marker history hash")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenMarkerOnlyHistoryReplacesOldMarker(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-100' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  100,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible ready): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(history ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	firstVisible := "after first compaction"
	firstHistory := "older retained context\n• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(firstHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	if state.LastCompactionTrigger == "" {
		t.Fatal("first checkPaneCapture() did not leave compaction trigger set")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("first checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
	state.LastCompactionPingAt = time.Now().Add(-compactionPingCooldown - time.Second)
	tracker.paneCaptureState["%11"] = state
	tracker.mu.Unlock()

	secondVisible := "after second compaction"
	secondHistory := "• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 for a marker-only newer history window replacing the old marker", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:worker" {
		t.Fatalf("second checkPaneCapture() target = %q, want %q", secondTargets[0].NodeKey, "review:worker")
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(secondVisible) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionHash != hashContentCRC32(secondHistory) {
		t.Fatal("checkPaneCapture() did not record the marker-only replacement history hash")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
	if state.LastCompactionPrefixLines != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction prefix lines, want marker-only prefix", state.LastCompactionPrefixLines)
	}
	if state.LastCompactionPrefixHash != hashContentCRC32("• Context compacted") {
		t.Fatal("checkPaneCapture() did not record the marker-only prefix hash")
	}

	state.LastCompactionPingAt = time.Now().Add(-compactionPingCooldown - time.Second)
	tracker.mu.Lock()
	tracker.paneCaptureState["%11"] = state
	tracker.mu.Unlock()

	thirdVisible := "after third compaction"
	thirdHistory := "• Context compacted\n" + thirdVisible
	if err := os.WriteFile(visiblePath, []byte(thirdVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible third): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(thirdHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history third): %v", err)
	}
	thirdTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(thirdTargets) != 1 {
		t.Fatalf("third checkPaneCapture() returned %d targets, want 1 for a newer marker-only history window replacing a stored marker-only prefix", len(thirdTargets))
	}
	if thirdTargets[0].NodeKey != "review:worker" {
		t.Fatalf("third checkPaneCapture() target = %q, want %q", thirdTargets[0].NodeKey, "review:worker")
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(thirdVisible) {
		t.Fatal("checkPaneCapture() changed idle hash away from third visible pane content")
	}
	if state.LastCompactionHash != hashContentCRC32(thirdHistory) {
		t.Fatal("checkPaneCapture() did not record the third marker-only replacement history hash")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers after third poll, want 1", state.LastCompactionMarkers)
	}
	if state.LastCompactionPrefixLines != 1 {
		t.Fatalf("checkPaneCapture() recorded %d third compaction prefix lines, want marker-only prefix", state.LastCompactionPrefixLines)
	}
	if state.LastCompactionPrefixHash != hashContentCRC32("• Context compacted") {
		t.Fatal("checkPaneCapture() did not record the third marker-only prefix hash")
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotRepeatWhenOnlyOutputAfterOldHistoryMarkerChanges(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-100' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_HISTORY\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_VISIBLE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  100,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(visiblePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(visible ready): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(history ready): %v", err)
	}
	initialTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(initialTargets) != 0 {
		t.Fatalf("initial checkPaneCapture() returned %d targets, want 0", len(initialTargets))
	}

	firstVisible := "after first compaction"
	firstHistory := "older\n• Context compacted\n" + firstVisible
	if err := os.WriteFile(visiblePath, []byte(firstVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible first): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(firstHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	if state.LastCompactionTrigger == "" {
		t.Fatal("first checkPaneCapture() did not leave compaction trigger set")
	}
	state.LastCompactionPingAt = time.Now().Add(-compactionPingCooldown - time.Second)
	tracker.paneCaptureState["%11"] = state
	tracker.mu.Unlock()

	secondVisible := "ordinary output after first marker"
	secondHistory := "older\n• Context compacted\n" + secondVisible
	if err := os.WriteFile(visiblePath, []byte(secondVisible), 0o644); err != nil {
		t.Fatalf("WriteFile(visible second): %v", err)
	}
	if err := os.WriteFile(historyPath, []byte(secondHistory), 0o644); err != nil {
		t.Fatalf("WriteFile(history second): %v", err)
	}
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0 when only output after the old marker changed", len(secondTargets))
	}

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastHash != hashContentCRC32(secondVisible) {
		t.Fatal("checkPaneCapture() changed idle hash away from visible pane content")
	}
	if state.LastCompactionHash != hashContentCRC32(firstHistory) {
		t.Fatal("checkPaneCapture() changed the compaction hash without a new marker occurrence")
	}
	if state.LastCompactionMarkers != 1 {
		t.Fatalf("checkPaneCapture() recorded %d compaction markers, want 1", state.LastCompactionMarkers)
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotRepeatWhileMarkerRemainsVisible(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '✻ Conversation compacted (ctrl+o for history)'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for an already-visible initial marker", len(firstTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	state.LastCompactionPingAt = time.Now().Add(-compactionPingCooldown - time.Second)
	tracker.paneCaptureState["%11"] = state
	tracker.mu.Unlock()

	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0 while marker remains visible", len(secondTargets))
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsAfterSamePaneProcessGenerationChanges(t *testing.T) {
	scriptDir := t.TempDir()
	pidPath := filepath.Join(scriptDir, "pane-pid")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '#{pane_pid}' ]; then\n" +
		"  pid=$(cat \"$TMUX_A2A_TEST_PANE_PID\")\n" +
		"  if [ \"$pid\" = 'fail' ]; then\n" +
		"    exit 1\n" +
		"  fi\n" +
		"  printf '%s\\n' \"$pid\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_PANE_PID", pidPath)

	now := time.Date(2026, time.August, 8, 22, 30, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(pidPath, []byte("100\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	if firstTargets[0].LifecycleIdentity != "review:worker|%11|pid:100" {
		t.Fatalf("first lifecycle identity = %q, want pid-qualified identity", firstTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	sameGenerationTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(sameGenerationTargets) != 0 {
		t.Fatalf("same-generation checkPaneCapture() returned %d targets, want 0", len(sameGenerationTargets))
	}

	if err := os.WriteFile(pidPath, []byte("fail\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid lookup failure): %v", err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	unknownGenerationTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(unknownGenerationTargets) != 0 {
		t.Fatalf("unknown-generation checkPaneCapture() returned %d targets, want 0", len(unknownGenerationTargets))
	}
	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionLifecycleIdentity != "review:worker|%11|pid:100" {
		t.Fatalf("unknown-generation lifecycle identity = %q, want retained pid-qualified identity", state.LastCompactionLifecycleIdentity)
	}

	if err := os.WriteFile(pidPath, []byte("100\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid restored): %v", err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	restoredGenerationTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(restoredGenerationTargets) != 0 {
		t.Fatalf("restored-generation checkPaneCapture() returned %d targets, want 0", len(restoredGenerationTargets))
	}

	if err := os.WriteFile(pidPath, []byte("200\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid second): %v", err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	newGenerationTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(newGenerationTargets) != 1 {
		t.Fatalf("new-generation checkPaneCapture() returned %d targets, want 1", len(newGenerationTargets))
	}
	if newGenerationTargets[0].LifecycleIdentity != "review:worker|%11|pid:200" {
		t.Fatalf("new lifecycle identity = %q, want updated pid-qualified identity", newGenerationTargets[0].LifecycleIdentity)
	}
}

func TestCheckPaneCapture_CompactionTriggerPreservesRetainedPIDLifecycleAcrossLookupFailureStateRecreation(t *testing.T) {
	scriptDir := t.TempDir()
	pidPath := filepath.Join(scriptDir, "pane-pid")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '#{pane_pid}' ]; then\n" +
		"  pid=$(cat \"$TMUX_A2A_TEST_PANE_PID\")\n" +
		"  if [ \"$pid\" = 'fail' ]; then\n" +
		"    exit 1\n" +
		"  fi\n" +
		"  printf '%s\\n' \"$pid\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_PANE_PID", pidPath)

	now := time.Date(2026, time.August, 8, 22, 45, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	if err := os.WriteFile(pidPath, []byte("100\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid first): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	if firstTargets[0].LifecycleIdentity != "review:worker|%11|pid:100" {
		t.Fatalf("first lifecycle identity = %q, want pid-qualified identity", firstTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	if err := os.WriteFile(pidPath, []byte("fail\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid lookup failure): %v", err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	recreatedTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(recreatedTargets) != 0 {
		t.Fatalf("recreated-state checkPaneCapture() returned %d targets, want 0", len(recreatedTargets))
	}
	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionLifecycleIdentity != "review:worker|%11|pid:100" {
		t.Fatalf("recreated lifecycle identity = %q, want retained pid-qualified identity", state.LastCompactionLifecycleIdentity)
	}

	if err := os.WriteFile(pidPath, []byte("100\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid restored): %v", err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	restoredTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(restoredTargets) != 0 {
		t.Fatalf("restored checkPaneCapture() returned %d targets, want 0", len(restoredTargets))
	}
}

func TestCheckPaneCapture_CompactionTriggerUsesLegacyLifecycleWhenPanePIDLookupFails(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	now := time.Date(2026, time.August, 8, 22, 40, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	if firstTargets[0].LifecycleIdentity != "review:worker|%11" {
		t.Fatalf("lifecycle identity = %q, want legacy pane identity", firstTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(secondTargets) != 0 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 0 when pid lookup fails and marker is already delivered", len(secondTargets))
	}
}

func TestCheckPaneCapture_CompactionTriggerRetriesAfterDeliveryFailure(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '#{pane_pid}' ]; then\n" +
		"  printf '%s\\n' '100'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '✻ Conversation compacted (ctrl+o for history)'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	now := time.Date(2026, time.August, 8, 22, 50, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	pendingTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(pendingTargets) != 0 {
		t.Fatalf("pending checkPaneCapture() returned %d targets, want 0 while delivery is pending", len(pendingTargets))
	}

	tracker.MarkCompactionPingDeliveryFailed(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)
	now = now.Add(compactionPingCooldown + time.Second)
	retryTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(retryTargets) != 1 {
		t.Fatalf("retry checkPaneCapture() returned %d targets, want 1 after failed/skipped/undelivered delivery", len(retryTargets))
	}
}

func TestCheckPaneCapture_CompactionMarkerIdentitySurvivesPaneStateRecreation(t *testing.T) {
	scriptDir := t.TempDir()
	capturePath := filepath.Join(scriptDir, "capture.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ]; then printf '%b\\n' '%11\\tcodex'; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ]; then cat \"$TMUX_A2A_TEST_CAPTURE\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_CAPTURE", capturePath)
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]discovery.NodeInfo{"review:worker": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir}}
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	now := time.Date(2026, time.August, 5, 1, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	if err := os.WriteFile(capturePath, []byte("ready"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("initial targets = %d, want 0", len(got))
	}
	if err := os.WriteFile(capturePath, []byte("• Context compacted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 {
		t.Fatalf("first targets = %d, want 1", len(got))
	}
	tracker.mu.Lock()
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()
	now = now.Add(time.Second)
	if err := os.WriteFile(capturePath, []byte("visible tail without marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("transient marker-free targets = %d, want 0", len(got))
	}
	tracker.mu.Lock()
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()
	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(capturePath, []byte("• Context compacted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("recreated-pane targets = %d, want 0 for same marker", len(got))
	}
	if err := os.WriteFile(capturePath, []byte("• Context compacted\n• Context compacted"), 0o644); err != nil {
		t.Fatal(err)
	}
	now = now.Add(compactionPingCooldown + time.Second)
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 {
		t.Fatalf("newer-marker targets = %d, want 1", len(got))
	}
}

func TestCheckPaneCapture_CompactionTriggerEmitsAfterAuthoritativeMarkerFreeHistoryClearsSameMarker(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ]; then printf '%b\\n' '%11\\tcodex'; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then cat \"$TMUX_A2A_TEST_HISTORY\"; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ]; then cat \"$TMUX_A2A_TEST_RECENT\"; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then cat \"$TMUX_A2A_TEST_VISIBLE\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]discovery.NodeInfo{"review:worker": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir}}
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  1,
	}
	now := time.Date(2026, time.August, 6, 3, 0, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })

	markerHistory := "older retained output\n• Context compacted\nsame post-marker output"
	if err := os.WriteFile(visiblePath, []byte("visible first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPath, []byte("recent first without marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(markerHistory), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 {
		t.Fatalf("first targets = %d, want 1 for marker in full history", len(got))
	}

	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(visiblePath, []byte("visible marker-free"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPath, []byte("recent marker-free"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte("full history marker-free"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("marker-free history targets = %d, want 0", len(got))
	}
	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionTrigger != "" {
		t.Fatalf("LastCompactionTrigger = %q, want cleared", state.LastCompactionTrigger)
	}
	if state.LastCompactionHash != 0 {
		t.Fatalf("LastCompactionHash = %d, want cleared", state.LastCompactionHash)
	}

	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(visiblePath, []byte("visible marker returns"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPath, []byte("recent marker-free again"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(markerHistory), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 {
		t.Fatalf("returned marker targets = %d, want 1 after authoritative marker-free history cleared prior marker", len(got))
	}
}

func TestCheckPaneCapture_RecreatedPaneAuthoritativeMarkerFreeHistoryClearsNodeMemory(t *testing.T) {
	scriptDir := t.TempDir()
	visiblePath := filepath.Join(scriptDir, "visible.txt")
	recentPath := filepath.Join(scriptDir, "recent.txt")
	historyPath := filepath.Join(scriptDir, "history.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ]; then printf '%b\\n' '%11\\tcodex'; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ] && [ \"$6\" = '-' ]; then cat \"$TMUX_A2A_TEST_HISTORY\"; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '-S' ]; then cat \"$TMUX_A2A_TEST_RECENT\"; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then cat \"$TMUX_A2A_TEST_VISIBLE\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_VISIBLE", visiblePath)
	t.Setenv("TMUX_A2A_TEST_RECENT", recentPath)
	t.Setenv("TMUX_A2A_TEST_HISTORY", historyPath)

	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatal(err)
	}
	const nodeKey = "review:worker"
	nodes := map[string]discovery.NodeInfo{nodeKey: {PaneID: "%11", SessionName: "review", SessionDir: sessionDir}}
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
		PaneCaptureTailLines:  1,
	}
	now := time.Date(2026, time.August, 6, 3, 30, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })

	markerHistory := "prelude\n• Context compacted\nsame post-marker output"
	if err := os.WriteFile(visiblePath, []byte("visible initial without marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPath, []byte("recent initial without marker"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(markerHistory), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 {
		t.Fatalf("first targets = %d, want 1 for marker in full history", len(got))
	}

	tracker.mu.Lock()
	memory, memoryExists := tracker.nodeCompactionMemory[nodeKey]
	tracker.mu.Unlock()
	if !memoryExists {
		t.Fatal("node compaction memory was not established")
	}
	if memory.LastCompactionTrigger == "" {
		t.Fatal("node compaction memory has empty trigger, want handled marker memory")
	}
	if memory.LastCompactionMarkerIdentity == "" {
		t.Fatal("node compaction memory has empty marker identity, want handled marker memory")
	}
	if memory.LastCompactionHash == 0 {
		t.Fatal("node compaction memory has zero hash, want handled marker memory")
	}
	if memory.LastCompactionMarkers == 0 {
		t.Fatal("node compaction memory has zero marker count, want handled marker memory")
	}
	if memory.LastCompactionScope != compactionScopeHistory {
		t.Fatalf("node compaction memory scope = %q, want full history", memory.LastCompactionScope)
	}
	firstPingAt := memory.LastCompactionPingAt
	if firstPingAt.IsZero() {
		t.Fatal("node compaction memory has zero ping time, want handled marker memory")
	}

	tracker.mu.Lock()
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()
	now = now.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(visiblePath, []byte("visible marker-free after pane recreation"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPath, []byte("recent marker-free after pane recreation"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte("full history marker-free after pane recreation"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("marker-free recreated-pane targets = %d, want 0", len(got))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	_, memoryExists = tracker.nodeCompactionMemory[nodeKey]
	tracker.mu.Unlock()
	if !state.LastCompactionPingAt.Equal(firstPingAt) {
		t.Fatalf("LastCompactionPingAt = %v, want retained %v", state.LastCompactionPingAt, firstPingAt)
	}
	if state.LastCompactionTrigger != "" {
		t.Fatalf("LastCompactionTrigger = %q, want cleared", state.LastCompactionTrigger)
	}
	if state.LastCompactionMarkerIdentity != "" {
		t.Fatalf("LastCompactionMarkerIdentity = %q, want cleared", state.LastCompactionMarkerIdentity)
	}
	if state.LastCompactionHash != 0 {
		t.Fatalf("LastCompactionHash = %d, want cleared", state.LastCompactionHash)
	}
	if state.LastCompactionMarkers != 0 {
		t.Fatalf("LastCompactionMarkers = %d, want cleared", state.LastCompactionMarkers)
	}
	if state.LastCompactionMarkerHash != 0 {
		t.Fatalf("LastCompactionMarkerHash = %d, want cleared", state.LastCompactionMarkerHash)
	}
	if state.LastCompactionScope != "" {
		t.Fatalf("LastCompactionScope = %q, want cleared", state.LastCompactionScope)
	}
	if state.LastCompactionSuffix != (compactionSuffixIdentity{}) {
		t.Fatalf("LastCompactionSuffix = %#v, want cleared", state.LastCompactionSuffix)
	}
	if state.LastCompactionPrefixHash != 0 {
		t.Fatalf("LastCompactionPrefixHash = %d, want cleared", state.LastCompactionPrefixHash)
	}
	if state.LastCompactionPrefixLines != 0 {
		t.Fatalf("LastCompactionPrefixLines = %d, want cleared", state.LastCompactionPrefixLines)
	}
	if memoryExists {
		t.Fatal("node compaction memory still exists, want cleared")
	}

	now = firstPingAt.Add(compactionPingCooldown - time.Second)
	if err := os.WriteFile(visiblePath, []byte("visible marker returns inside cooldown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recentPath, []byte("recent marker-free inside cooldown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(historyPath, []byte(markerHistory), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("same marker inside cooldown targets = %d, want 0", len(got))
	}

	now = firstPingAt.Add(compactionPingCooldown + time.Second)
	if err := os.WriteFile(visiblePath, []byte("visible marker returns after cooldown"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 {
		t.Fatalf("same marker after cooldown targets = %d, want 1", len(got))
	}
}

func TestCheckPaneCapture_CompactionMarkersRemainIndependentPerNode(t *testing.T) {
	scriptDir := t.TempDir()
	firstPath, secondPath := filepath.Join(scriptDir, "first"), filepath.Join(scriptDir, "second")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ]; then printf '%b\\n' '%11\\tcodex' '%12\\tcodex'; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$4\" = '%11' ]; then cat \"$TMUX_A2A_TEST_FIRST\"; exit 0; fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$4\" = '%12' ]; then cat \"$TMUX_A2A_TEST_SECOND\"; exit 0; fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_FIRST", firstPath)
	t.Setenv("TMUX_A2A_TEST_SECOND", secondPath)
	for _, path := range []string{firstPath, secondPath} {
		if err := os.WriteFile(path, []byte("ready"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatal(err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:first":  {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
		"review:second": {PaneID: "%12", SessionName: "review", SessionDir: sessionDir},
	}
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	tracker := NewIdleTracker()
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 0 {
		t.Fatalf("initial targets = %d, want 0", len(got))
	}
	if err := os.WriteFile(firstPath, []byte("• Context compacted"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := tracker.checkPaneCapture(cfg, nodes); len(got) != 1 || got[0].NodeKey != "review:first" {
		t.Fatalf("first-node targets = %#v, want review:first", got)
	}
	if err := os.WriteFile(secondPath, []byte("• Context compacted"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := tracker.checkPaneCapture(cfg, nodes)
	if len(got) != 1 || got[0].NodeKey != "review:second" {
		t.Fatalf("second-node targets = %#v, want review:second", got)
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenSamePaneRemapsToDifferentNodeWithKnownPID(t *testing.T) {
	scriptDir := t.TempDir()
	pidPath := filepath.Join(scriptDir, "pane-pid")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '#{pane_pid}' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_PANE_PID\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_PANE_PID", pidPath)
	if err := os.WriteFile(pidPath, []byte("100\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid): %v", err)
	}

	now := time.Date(2026, time.August, 8, 23, 25, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	firstTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:first": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	if firstTargets[0].LifecycleIdentity != "review:first|%11|pid:100" {
		t.Fatalf("first lifecycle identity = %q, want first node PID lifecycle", firstTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 after node remap", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:second" {
		t.Fatalf("second target node = %q, want review:second", secondTargets[0].NodeKey)
	}
	if secondTargets[0].LifecycleIdentity != "review:second|%11|pid:100" {
		t.Fatalf("second lifecycle identity = %q, want second node PID lifecycle", secondTargets[0].LifecycleIdentity)
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotReuseStaleNodeMemoryWhenSamePaneRemapsBackWithKnownPID(t *testing.T) {
	scriptDir := t.TempDir()
	pidPath := filepath.Join(scriptDir, "pane-pid")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ] && [ \"$5\" = '#{pane_pid}' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_PANE_PID\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_PANE_PID", pidPath)
	if err := os.WriteFile(pidPath, []byte("100\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(pid): %v", err)
	}

	now := time.Date(2026, time.August, 8, 23, 45, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	firstTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:first": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 after node remap", len(secondTargets))
	}
	tracker.MarkCompactionPingDelivered(secondTargets[0].NodeKey, secondTargets[0].LifecycleIdentity, secondTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	if stale := tracker.nodeCompactionMemory["review:first"].LastCompactionLifecycleIdentity; stale != "" {
		t.Fatalf("stale first-node memory lifecycle = %q, want cleared after remap", stale)
	}
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	now = now.Add(compactionPingCooldown + time.Second)
	thirdTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:first": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(thirdTargets) != 1 {
		t.Fatalf("third checkPaneCapture() returned %d targets, want 1 after remap back and state recreation", len(thirdTargets))
	}
	if thirdTargets[0].NodeKey != "review:first" {
		t.Fatalf("third target node = %q, want review:first", thirdTargets[0].NodeKey)
	}
	if thirdTargets[0].LifecycleIdentity != "review:first|%11|pid:100" {
		t.Fatalf("third lifecycle identity = %q, want first node PID lifecycle", thirdTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(thirdTargets[0].NodeKey, thirdTargets[0].LifecycleIdentity, thirdTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	if stale := tracker.nodeCompactionMemory["review:second"].LastCompactionLifecycleIdentity; stale != "" {
		t.Fatalf("stale second-node memory lifecycle = %q, want cleared after remap back", stale)
	}
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	now = now.Add(compactionPingCooldown + time.Second)
	fourthTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(fourthTargets) != 1 {
		t.Fatalf("fourth checkPaneCapture() returned %d targets, want 1 after second remap and state recreation", len(fourthTargets))
	}
	if fourthTargets[0].NodeKey != "review:second" {
		t.Fatalf("fourth target node = %q, want review:second", fourthTargets[0].NodeKey)
	}
	if fourthTargets[0].LifecycleIdentity != "review:second|%11|pid:100" {
		t.Fatalf("fourth lifecycle identity = %q, want second node PID lifecycle", fourthTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(fourthTargets[0].NodeKey, fourthTargets[0].LifecycleIdentity, fourthTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	memory := tracker.nodeCompactionMemory["review:second"]
	tracker.mu.Unlock()
	if state.LastCompactionLifecycleIdentity != "review:second|%11|pid:100" {
		t.Fatalf("state lifecycle identity = %q, want second node PID lifecycle", state.LastCompactionLifecycleIdentity)
	}
	if memory.LastCompactionLifecycleIdentity != "review:second|%11|pid:100" {
		t.Fatalf("memory lifecycle identity = %q, want second node PID lifecycle", memory.LastCompactionLifecycleIdentity)
	}
	if state.LastCompactionPaneID != "%11" {
		t.Fatalf("state pane identity = %q, want %%11", state.LastCompactionPaneID)
	}
	if memory.LastCompactionPaneID != "%11" {
		t.Fatalf("memory pane identity = %q, want %%11", memory.LastCompactionPaneID)
	}

	now = now.Add(compactionPingCooldown + time.Second)
	repeatTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(repeatTargets) != 0 {
		t.Fatalf("repeat checkPaneCapture() returned %d targets, want 0 after delivery", len(repeatTargets))
	}
}

func TestCheckPaneCapture_CompactionTriggerRepeatsWhenSamePaneRemapsToDifferentNodeWithUnknownPID(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	now := time.Date(2026, time.August, 8, 23, 30, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	firstTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:first": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	if firstTargets[0].LifecycleIdentity != "review:first|%11" {
		t.Fatalf("first lifecycle identity = %q, want first node legacy lifecycle", firstTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 after node remap", len(secondTargets))
	}
	if secondTargets[0].NodeKey != "review:second" {
		t.Fatalf("second target node = %q, want review:second", secondTargets[0].NodeKey)
	}
	if secondTargets[0].LifecycleIdentity != "review:second|%11" {
		t.Fatalf("second lifecycle identity = %q, want second node legacy lifecycle", secondTargets[0].LifecycleIdentity)
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	memory := tracker.nodeCompactionMemory["review:second"]
	tracker.mu.Unlock()
	if state.LastCompactionLifecycleIdentity != "review:second|%11" {
		t.Fatalf("state lifecycle identity = %q, want second node legacy lifecycle", state.LastCompactionLifecycleIdentity)
	}
	if memory.LastCompactionLifecycleIdentity != "review:second|%11" {
		t.Fatalf("memory lifecycle identity = %q, want second node legacy lifecycle", memory.LastCompactionLifecycleIdentity)
	}
	if state.LastCompactionPaneID != "%11" {
		t.Fatalf("state pane identity = %q, want %%11", state.LastCompactionPaneID)
	}
	if memory.LastCompactionPaneID != "%11" {
		t.Fatalf("memory pane identity = %q, want %%11", memory.LastCompactionPaneID)
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotReuseStaleNodeMemoryWhenSamePaneRemapsBackWithUnknownPID(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	now := time.Date(2026, time.August, 8, 23, 50, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	firstTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:first": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 after node remap", len(secondTargets))
	}
	tracker.MarkCompactionPingDelivered(secondTargets[0].NodeKey, secondTargets[0].LifecycleIdentity, secondTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	if stale := tracker.nodeCompactionMemory["review:first"].LastCompactionLifecycleIdentity; stale != "" {
		t.Fatalf("stale first-node memory lifecycle = %q, want cleared after remap", stale)
	}
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	now = now.Add(compactionPingCooldown + time.Second)
	thirdTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:first": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(thirdTargets) != 1 {
		t.Fatalf("third checkPaneCapture() returned %d targets, want 1 after remap back and state recreation", len(thirdTargets))
	}
	if thirdTargets[0].NodeKey != "review:first" {
		t.Fatalf("third target node = %q, want review:first", thirdTargets[0].NodeKey)
	}
	if thirdTargets[0].LifecycleIdentity != "review:first|%11" {
		t.Fatalf("third lifecycle identity = %q, want first node legacy lifecycle", thirdTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(thirdTargets[0].NodeKey, thirdTargets[0].LifecycleIdentity, thirdTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	memory := tracker.nodeCompactionMemory["review:first"]
	tracker.mu.Unlock()
	if state.LastCompactionLifecycleIdentity != "review:first|%11" {
		t.Fatalf("state lifecycle identity = %q, want first node legacy lifecycle", state.LastCompactionLifecycleIdentity)
	}
	if memory.LastCompactionLifecycleIdentity != "review:first|%11" {
		t.Fatalf("memory lifecycle identity = %q, want first node legacy lifecycle", memory.LastCompactionLifecycleIdentity)
	}
	if state.LastCompactionPaneID != "%11" {
		t.Fatalf("state pane identity = %q, want %%11", state.LastCompactionPaneID)
	}
	if memory.LastCompactionPaneID != "%11" {
		t.Fatalf("memory pane identity = %q, want %%11", memory.LastCompactionPaneID)
	}

	tracker.mu.Lock()
	if stale := tracker.nodeCompactionMemory["review:second"].LastCompactionLifecycleIdentity; stale != "" {
		t.Fatalf("stale second-node memory lifecycle = %q, want cleared after remap back", stale)
	}
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	now = now.Add(compactionPingCooldown + time.Second)
	fourthTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(fourthTargets) != 1 {
		t.Fatalf("fourth checkPaneCapture() returned %d targets, want 1 after second remap and state recreation", len(fourthTargets))
	}
	if fourthTargets[0].NodeKey != "review:second" {
		t.Fatalf("fourth target node = %q, want review:second", fourthTargets[0].NodeKey)
	}
	if fourthTargets[0].LifecycleIdentity != "review:second|%11" {
		t.Fatalf("fourth lifecycle identity = %q, want second node legacy lifecycle", fourthTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(fourthTargets[0].NodeKey, fourthTargets[0].LifecycleIdentity, fourthTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	state = tracker.paneCaptureState["%11"]
	memory = tracker.nodeCompactionMemory["review:second"]
	tracker.mu.Unlock()
	if state.LastCompactionLifecycleIdentity != "review:second|%11" {
		t.Fatalf("state lifecycle identity = %q, want second node legacy lifecycle", state.LastCompactionLifecycleIdentity)
	}
	if memory.LastCompactionLifecycleIdentity != "review:second|%11" {
		t.Fatalf("memory lifecycle identity = %q, want second node legacy lifecycle", memory.LastCompactionLifecycleIdentity)
	}
	if state.LastCompactionPaneID != "%11" {
		t.Fatalf("state pane identity = %q, want %%11", state.LastCompactionPaneID)
	}
	if memory.LastCompactionPaneID != "%11" {
		t.Fatalf("memory pane identity = %q, want %%11", memory.LastCompactionPaneID)
	}

	now = now.Add(compactionPingCooldown + time.Second)
	repeatTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		"review:second": {PaneID: "%11", SessionName: "review", SessionDir: sessionDir},
	})
	if len(repeatTargets) != 0 {
		t.Fatalf("repeat checkPaneCapture() returned %d targets, want 0 after delivery", len(repeatTargets))
	}
}

func TestIdleTracker_ClearOtherNodeCompactionMemoryForPaneKeepsCurrentAndOtherPanes(t *testing.T) {
	now := time.Date(2026, time.August, 9, 0, 5, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	tracker.nodeCompactionMemory["review:first"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "review:first|%11|pid:100",
		LastCompactionPaneID:            "%11",
	}
	tracker.nodeCompactionMemory["review:second"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "review:second|%11|pid:100",
		LastCompactionPaneID:            "%11",
	}
	tracker.nodeCompactionMemory["review:other"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "review:other|%12|pid:200",
		LastCompactionPaneID:            "%12",
	}

	tracker.clearOtherNodeCompactionMemoryForPane("review:second", "%11")

	if _, ok := tracker.nodeCompactionMemory["review:first"]; ok {
		t.Fatal("stale first-node memory still exists, want cleared")
	}
	if _, ok := tracker.nodeCompactionMemory["review:second"]; !ok {
		t.Fatal("current second-node memory was cleared, want retained")
	}
	if _, ok := tracker.nodeCompactionMemory["review:other"]; !ok {
		t.Fatal("other-pane memory was cleared, want retained")
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotReuseStaleNodeMemoryWhenPipeNodeKeyRemapsBackWithUnknownPID(t *testing.T) {
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tcodex'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'display-message' ]; then\n" +
		"  exit 1\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  printf '%s\\n' '• Context compacted'\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	now := time.Date(2026, time.August, 9, 0, 20, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	cfg := &config.Config{ActivityWindowSeconds: 120, NodeStaleSeconds: 600}
	sessionDir := filepath.Join(t.TempDir(), "team|blue")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}

	firstNodeKey := "team|blue:first"
	secondNodeKey := "team|blue:second"

	firstTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		firstNodeKey: {PaneID: "%11", SessionName: "team|blue", SessionDir: sessionDir},
	})
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1", len(firstTargets))
	}
	tracker.MarkCompactionPingDelivered(firstTargets[0].NodeKey, firstTargets[0].LifecycleIdentity, firstTargets[0].MarkerIdentity)

	now = now.Add(compactionPingCooldown + time.Second)
	secondTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		secondNodeKey: {PaneID: "%11", SessionName: "team|blue", SessionDir: sessionDir},
	})
	if len(secondTargets) != 1 {
		t.Fatalf("second checkPaneCapture() returned %d targets, want 1 after pipe-node remap", len(secondTargets))
	}
	tracker.MarkCompactionPingDelivered(secondTargets[0].NodeKey, secondTargets[0].LifecycleIdentity, secondTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	if stale := tracker.nodeCompactionMemory[firstNodeKey].LastCompactionLifecycleIdentity; stale != "" {
		t.Fatalf("stale first-node memory lifecycle = %q, want cleared after pipe-node remap", stale)
	}
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	now = now.Add(compactionPingCooldown + time.Second)
	thirdTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		firstNodeKey: {PaneID: "%11", SessionName: "team|blue", SessionDir: sessionDir},
	})
	if len(thirdTargets) != 1 {
		t.Fatalf("third checkPaneCapture() returned %d targets, want 1 after pipe-node remap back and state recreation", len(thirdTargets))
	}
	if thirdTargets[0].NodeKey != firstNodeKey {
		t.Fatalf("third target node = %q, want %q", thirdTargets[0].NodeKey, firstNodeKey)
	}
	if thirdTargets[0].LifecycleIdentity != "team|blue:first|%11" {
		t.Fatalf("third lifecycle identity = %q, want first pipe-node lifecycle", thirdTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(thirdTargets[0].NodeKey, thirdTargets[0].LifecycleIdentity, thirdTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	if stale := tracker.nodeCompactionMemory[secondNodeKey].LastCompactionLifecycleIdentity; stale != "" {
		t.Fatalf("stale second-node memory lifecycle = %q, want cleared after pipe-node remap back", stale)
	}
	delete(tracker.paneCaptureState, "%11")
	tracker.mu.Unlock()

	now = now.Add(compactionPingCooldown + time.Second)
	fourthTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		secondNodeKey: {PaneID: "%11", SessionName: "team|blue", SessionDir: sessionDir},
	})
	if len(fourthTargets) != 1 {
		t.Fatalf("fourth checkPaneCapture() returned %d targets, want 1 after second pipe-node remap and state recreation", len(fourthTargets))
	}
	if fourthTargets[0].NodeKey != secondNodeKey {
		t.Fatalf("fourth target node = %q, want %q", fourthTargets[0].NodeKey, secondNodeKey)
	}
	if fourthTargets[0].LifecycleIdentity != "team|blue:second|%11" {
		t.Fatalf("fourth lifecycle identity = %q, want second pipe-node lifecycle", fourthTargets[0].LifecycleIdentity)
	}
	tracker.MarkCompactionPingDelivered(fourthTargets[0].NodeKey, fourthTargets[0].LifecycleIdentity, fourthTargets[0].MarkerIdentity)

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	memory := tracker.nodeCompactionMemory[secondNodeKey]
	tracker.mu.Unlock()
	if state.LastCompactionPaneID != "%11" {
		t.Fatalf("state pane identity = %q, want %%11", state.LastCompactionPaneID)
	}
	if memory.LastCompactionPaneID != "%11" {
		t.Fatalf("memory pane identity = %q, want %%11", memory.LastCompactionPaneID)
	}

	now = now.Add(compactionPingCooldown + time.Second)
	repeatTargets := tracker.checkPaneCapture(cfg, map[string]discovery.NodeInfo{
		secondNodeKey: {PaneID: "%11", SessionName: "team|blue", SessionDir: sessionDir},
	})
	if len(repeatTargets) != 0 {
		t.Fatalf("repeat checkPaneCapture() returned %d targets, want 0 after pipe-node delivery", len(repeatTargets))
	}
}

func TestIdleTracker_ClearOtherNodeCompactionMemoryForPaneWithPipeNodeKeysKeepsCurrentAndOtherPanes(t *testing.T) {
	now := time.Date(2026, time.August, 9, 0, 25, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	tracker.nodeCompactionMemory["team|blue:first"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "team|blue:first|%11|pid:100",
		LastCompactionPaneID:            "%11",
	}
	tracker.nodeCompactionMemory["team|blue:second"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "team|blue:second|%11|pid:100",
		LastCompactionPaneID:            "%11",
	}
	tracker.nodeCompactionMemory["team|blue:other"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "team|blue:other|%12|pid:200",
		LastCompactionPaneID:            "%12",
	}

	tracker.clearOtherNodeCompactionMemoryForPane("team|blue:second", "%11")

	if _, ok := tracker.nodeCompactionMemory["team|blue:first"]; ok {
		t.Fatal("stale first pipe-node memory still exists, want cleared")
	}
	if _, ok := tracker.nodeCompactionMemory["team|blue:second"]; !ok {
		t.Fatal("current second pipe-node memory was cleared, want retained")
	}
	if _, ok := tracker.nodeCompactionMemory["team|blue:other"]; !ok {
		t.Fatal("other-pane pipe-node memory was cleared, want retained")
	}
}

func TestIdleTracker_NodeCompactionMemoryForDropsZeroValuePaneIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 9, 0, 30, 0, 0, time.UTC)
	tracker := newIdleTrackerWithClock(func() time.Time { return now })
	tracker.nodeCompactionMemory["team|blue:worker"] = PaneCaptureState{
		LastCompactionPingAt:            now,
		LastCompactionLifecycleIdentity: "team|blue:worker|%11|pid:100",
		LastCompactionTrigger:           "codex:conversation-compaction",
	}

	if _, ok := tracker.nodeCompactionMemoryFor("team|blue:worker", "%11", now); ok {
		t.Fatal("zero-value pane identity memory was reusable, want dropped")
	}
	if _, ok := tracker.nodeCompactionMemory["team|blue:worker"]; ok {
		t.Fatal("zero-value pane identity memory still exists, want deleted")
	}
}

func TestCheckPaneCapture_CompactionTriggerDoesNotRepeatSameCaptureAfterMarkerClears(t *testing.T) {
	scriptDir := t.TempDir()
	capturePath := filepath.Join(scriptDir, "capture.txt")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = 'list-panes' ] && [ \"$2\" = '-a' ] && [ \"$3\" = '-F' ] && [ \"$4\" = '#{pane_id}\t#{pane_current_command}' ]; then\n" +
		"  printf '%s\\n' '%11\tclaude'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'capture-pane' ] && [ \"$2\" = '-p' ] && [ \"$3\" = '-t' ] && [ \"$4\" = '%11' ]; then\n" +
		"  cat \"$TMUX_A2A_TEST_CAPTURE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_A2A_TEST_CAPTURE", capturePath)

	tracker := NewIdleTracker()
	cfg := &config.Config{
		ActivityWindowSeconds: 120,
		NodeStaleSeconds:      600,
	}
	sessionDir := filepath.Join(t.TempDir(), "review")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("CreateSessionDirs: %v", err)
	}
	nodes := map[string]discovery.NodeInfo{
		"review:worker": {
			PaneID:      "%11",
			SessionName: "review",
			SessionDir:  sessionDir,
		},
	}

	marker := "✻ Conversation compacted (ctrl+o for history)"
	if err := os.WriteFile(capturePath, []byte(marker), 0o644); err != nil {
		t.Fatalf("WriteFile(capture marker): %v", err)
	}
	firstTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(firstTargets) != 1 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 1 for an already-visible initial marker", len(firstTargets))
	}

	if err := os.WriteFile(capturePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(capture ready): %v", err)
	}
	clearedTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(clearedTargets) != 0 {
		t.Fatalf("clearing checkPaneCapture() returned %d targets, want 0", len(clearedTargets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	state.LastCompactionPingAt = time.Now().Add(-compactionPingCooldown - time.Second)
	tracker.paneCaptureState["%11"] = state
	tracker.mu.Unlock()

	if err := os.WriteFile(capturePath, []byte(marker), 0o644); err != nil {
		t.Fatalf("WriteFile(capture marker again): %v", err)
	}
	repeatedTargets := tracker.checkPaneCapture(cfg, nodes)
	if len(repeatedTargets) != 0 {
		t.Fatalf("repeated checkPaneCapture() returned %d targets, want 0 for the same compaction capture", len(repeatedTargets))
	}
}
