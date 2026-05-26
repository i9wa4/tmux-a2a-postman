package idle

import (
	"encoding/json"
	"os"
	"path/filepath"
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

func TestCheckPaneCapture_CompactionTriggerRecordsInitialMarkerWithoutPing(t *testing.T) {
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
	if len(targets) != 0 {
		t.Fatalf("checkPaneCapture() returned %d targets, want 0 for an already-visible initial marker", len(targets))
	}

	tracker.mu.Lock()
	state := tracker.paneCaptureState["%11"]
	tracker.mu.Unlock()
	if state.LastCompactionTrigger == "" {
		t.Fatal("checkPaneCapture() did not record the initial compaction trigger")
	}
	if !state.LastCompactionPingAt.IsZero() {
		t.Fatal("checkPaneCapture() recorded a ping timestamp for an initial marker")
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
	if state.LastCompactionPrefix != "• Context compacted" {
		t.Fatalf("checkPaneCapture() recorded compaction prefix %q, want marker-only prefix", state.LastCompactionPrefix)
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
	if state.LastCompactionPrefix != "• Context compacted" {
		t.Fatalf("checkPaneCapture() recorded third compaction prefix %q, want marker-only prefix", state.LastCompactionPrefix)
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
	if len(firstTargets) != 0 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 0 for an already-visible initial marker", len(firstTargets))
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
	if len(firstTargets) != 0 {
		t.Fatalf("first checkPaneCapture() returned %d targets, want 0 for an already-visible initial marker", len(firstTargets))
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
