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
	tracker := NewIdleTracker()
	nodeKey := "test-session:test-node"
	before := time.Now()

	// Test UpdateSendActivity
	tracker.UpdateSendActivity(nodeKey)

	tracker.mu.Lock()
	activity := tracker.nodeActivity[nodeKey]
	tracker.mu.Unlock()

	if activity.LastSent.IsZero() {
		t.Fatalf("send activity not recorded for %s", nodeKey)
	}

	if activity.LastSent.Before(before) {
		t.Errorf("send activity time %v is before test start %v", activity.LastSent, before)
	}

	// Test UpdateReceiveActivity
	before2 := time.Now()
	tracker.UpdateReceiveActivity(nodeKey)

	tracker.mu.Lock()
	activity2 := tracker.nodeActivity[nodeKey]
	tracker.mu.Unlock()

	if activity2.LastReceived.IsZero() {
		t.Fatalf("receive activity not recorded for %s", nodeKey)
	}

	if activity2.LastReceived.Before(before2) {
		t.Errorf("receive activity time %v is before test start %v", activity2.LastReceived, before2)
	}
}

// Issue #123: Test for ExportPaneActivityToFile — verifies new JSON schema (struct format)
func TestExportPaneActivityToFile(t *testing.T) {
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
	}
	now := time.Now()

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

	pane21, ok := exported["%21"]
	if !ok {
		t.Fatal("expected %%21 in exported data")
	}
	if pane21.Status != "idle" {
		t.Errorf("expected %%21 status 'idle', got %q", pane21.Status)
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
	if targets[0] != "review:worker" {
		t.Fatalf("checkPaneCapture() target = %q, want %q", targets[0], "review:worker")
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
