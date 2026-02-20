package idle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
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

func TestCheckIdleNodes_NoTimeout(t *testing.T) {
	tracker := NewIdleTracker()

	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		Nodes: map[string]config.NodeConfig{
			"worker": {
				IdleTimeoutSeconds:          5.0,
				IdleReminderMessage:         "Test reminder",
				IdleReminderCooldownSeconds: 10.0,
			},
		},
	}

	// Set recent activity (within threshold)
	tracker.UpdateSendActivity("test-session:worker")

	// Check idle nodes - should NOT send reminder
	tracker.checkIdleNodes(cfg, nil, sessionDir)

	// Verify no reminder sent
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	entries, err := os.ReadDir(inboxDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading inbox failed: %v", err)
	}

	if len(entries) > 0 {
		t.Errorf("expected no reminder, but found %d files", len(entries))
	}
}

func TestCheckIdleNodes_WithTimeout(t *testing.T) {
	tracker := NewIdleTracker()

	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		Nodes: map[string]config.NodeConfig{
			"worker": {
				IdleTimeoutSeconds:          1.0, // 1 second threshold
				IdleReminderMessage:         "Test reminder message",
				IdleReminderCooldownSeconds: 10.0,
			},
		},
	}

	// Set old activity (exceeds threshold)
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastSent: time.Now().Add(-2 * time.Second),
	}
	tracker.mu.Unlock()

	// Check idle nodes - should send reminder
	tracker.checkIdleNodes(cfg, nil, sessionDir)

	// Verify reminder sent
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("reading inbox failed: %v", err)
	}

	if len(entries) != 1 {
		t.Errorf("expected 1 reminder file, got %d", len(entries))
	}

	// Verify file content
	if len(entries) > 0 {
		content, err := os.ReadFile(filepath.Join(inboxDir, entries[0].Name()))
		if err != nil {
			t.Fatalf("reading reminder file failed: %v", err)
		}

		contentStr := string(content)
		if !containsString(contentStr, "Test reminder message") {
			t.Errorf("reminder content missing message, got: %s", contentStr)
		}
	}
}

func TestCheckIdleNodes_WithCooldown(t *testing.T) {
	tracker := NewIdleTracker()

	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		Nodes: map[string]config.NodeConfig{
			"worker": {
				IdleTimeoutSeconds:          1.0,
				IdleReminderMessage:         "Test reminder",
				IdleReminderCooldownSeconds: 5.0, // 5 second cooldown
			},
		},
	}

	// Set old activity and recent reminder sent
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastSent: time.Now().Add(-2 * time.Second),
	}
	tracker.lastReminderSent["test-session:worker"] = time.Now().Add(-1 * time.Second) // Within cooldown
	tracker.mu.Unlock()

	// Check idle nodes - should NOT send reminder (cooldown active)
	tracker.checkIdleNodes(cfg, nil, sessionDir)

	// Verify no new reminder sent
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	entries, err := os.ReadDir(inboxDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading inbox failed: %v", err)
	}

	if len(entries) > 0 {
		t.Errorf("expected no reminder during cooldown, but found %d files", len(entries))
	}
}

func TestCheckIdleNodes_ActivityReset(t *testing.T) {
	tracker := NewIdleTracker()

	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		Nodes: map[string]config.NodeConfig{
			"worker": {
				IdleTimeoutSeconds:          1.0,
				IdleReminderMessage:         "Test reminder",
				IdleReminderCooldownSeconds: 10.0,
			},
		},
	}

	// Set old activity
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastSent: time.Now().Add(-2 * time.Second),
	}
	tracker.mu.Unlock()

	// Update activity (reset timer)
	tracker.UpdateSendActivity("test-session:worker")

	// Check idle nodes - should NOT send reminder (activity reset)
	tracker.checkIdleNodes(cfg, nil, sessionDir)

	// Verify no reminder sent
	inboxDir := filepath.Join(sessionDir, "inbox", "worker")
	entries, err := os.ReadDir(inboxDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading inbox failed: %v", err)
	}

	if len(entries) > 0 {
		t.Errorf("expected no reminder after activity reset, but found %d files", len(entries))
	}
}

func TestSendIdleReminder(t *testing.T) {
	tracker := NewIdleTracker()
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := config.DefaultConfig()
	nodeName := "test-worker"
	message := "Test idle reminder message"

	if err := tracker.sendIdleReminder(cfg, nodeName, message, sessionDir); err != nil {
		t.Fatalf("sendIdleReminder failed: %v", err)
	}

	// Verify file created in inbox
	inboxDir := filepath.Join(sessionDir, "inbox", nodeName)
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("reading inbox failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 reminder file, got %d", len(entries))
	}

	// Verify file content
	content, err := os.ReadFile(filepath.Join(inboxDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading reminder file failed: %v", err)
	}

	contentStr := string(content)
	if !containsString(contentStr, message) {
		t.Errorf("reminder content missing message, got: %s", contentStr)
	}

	if !containsString(contentStr, "from: postman") {
		t.Errorf("reminder missing 'from: postman', got: %s", contentStr)
	}

	if !containsString(contentStr, "## Idle Reminder") {
		t.Errorf("reminder missing '## Idle Reminder' header, got: %s", contentStr)
	}
}

// Helper function to check if string contains substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// Issue #56: Tests for dropped-ball detection
func TestCheckDroppedBalls_BasicDetection(t *testing.T) {
	tracker := NewIdleTracker()

	// Setup: Node holding ball beyond threshold
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastReceived:        time.Now().Add(-11 * time.Second),
		LastSent:            time.Now().Add(-15 * time.Second),
		PongReceived:        true,
		LastNotifiedDropped: time.Time{}, // Never notified
	}
	tracker.mu.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := tracker.CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 1 {
		t.Errorf("expected 1 dropped node, got %d", len(dropped))
	}

	if duration, exists := dropped["test-session:worker"]; !exists {
		t.Errorf("expected worker to be detected as dropped")
	} else if duration < 11*time.Second {
		t.Errorf("expected duration >= 11s, got %v", duration)
	}
}

func TestCheckDroppedBalls_ThresholdNotExceeded(t *testing.T) {
	tracker := NewIdleTracker()

	// Setup: Node holding ball but within threshold
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastReceived: time.Now().Add(-5 * time.Second),
		LastSent:     time.Now().Add(-7 * time.Second),
		PongReceived: true,
	}
	tracker.mu.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := tracker.CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (threshold not exceeded), got %d", len(dropped))
	}
}

func TestCheckDroppedBalls_CooldownActive(t *testing.T) {
	tracker := NewIdleTracker()

	// Setup: Node holding ball beyond threshold, but already notified recently
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastReceived:        time.Now().Add(-11 * time.Second),
		LastSent:            time.Now().Add(-15 * time.Second),
		PongReceived:        true,
		LastNotifiedDropped: time.Now().Add(-5 * time.Second), // Notified 5s ago
	}
	tracker.mu.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10, // 10s cooldown
		},
	}

	dropped := tracker.CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (cooldown active), got %d", len(dropped))
	}
}

func TestCheckDroppedBalls_NoPongReceived(t *testing.T) {
	tracker := NewIdleTracker()

	// Setup: Node holding ball but handshake incomplete (no PONG)
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastReceived: time.Now().Add(-11 * time.Second),
		LastSent:     time.Now().Add(-15 * time.Second),
		PongReceived: false, // No PONG yet
	}
	tracker.mu.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := tracker.CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (PONG not received), got %d", len(dropped))
	}
}

func TestCheckDroppedBalls_DisabledNode(t *testing.T) {
	tracker := NewIdleTracker()

	// Setup: Node holding ball but dropped-ball detection disabled (threshold=0)
	tracker.mu.Lock()
	tracker.nodeActivity["test-session:worker"] = NodeActivity{
		LastReceived: time.Now().Add(-11 * time.Second),
		LastSent:     time.Now().Add(-15 * time.Second),
		PongReceived: true,
	}
	tracker.mu.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  0, // Disabled
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := tracker.CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (detection disabled), got %d", len(dropped))
	}
}

// Issue #122: Tests for GetPaneActivityStatus
func TestGetPaneActivityStatus_ChangeCountZeroAfterActive(t *testing.T) {
	// Bug case: ChangeCount==0 (reset after 2 consecutive changes) but LastChangeAt is recent.
	// Before fix: returned "stale". After fix: returns "active".
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
		NodeIdleSeconds:   900.0,
	}
	now := time.Now()
	tracker.mu.Lock()
	tracker.paneCaptureState["%10"] = PaneCaptureState{
		LastHash:      12345,
		LastChangeAt:  now.Add(-10 * time.Second), // Recent change
		ChangeCount:   0,                           // Reset after marking active
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
		NodeIdleSeconds:   900.0,
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
	// LastChangeAt between active and idle thresholds -> "idle".
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 60.0,
		NodeIdleSeconds:   600.0,
	}
	now := time.Now()
	tracker.mu.Lock()
	tracker.paneCaptureState["%12"] = PaneCaptureState{
		LastHash:      999,
		LastChangeAt:  now.Add(-120 * time.Second), // 2 min ago: beyond active (60s), within idle (600s)
		ChangeCount:   0,
		LastCaptureAt: now,
	}
	tracker.mu.Unlock()

	result := tracker.GetPaneActivityStatus(cfg)
	if result["%12"] != "idle" {
		t.Errorf("expected 'idle', got %q", result["%12"])
	}
}

func TestGetPaneActivityStatus_StalePane(t *testing.T) {
	// LastChangeAt older than idle threshold -> "stale".
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 60.0,
		NodeIdleSeconds:   600.0,
	}
	now := time.Now()
	tracker.mu.Lock()
	tracker.paneCaptureState["%13"] = PaneCaptureState{
		LastHash:      111,
		LastChangeAt:  now.Add(-700 * time.Second), // beyond idle threshold
		ChangeCount:   0,
		LastCaptureAt: now,
	}
	tracker.mu.Unlock()

	result := tracker.GetPaneActivityStatus(cfg)
	if result["%13"] != "stale" {
		t.Errorf("expected 'stale' for old LastChangeAt, got %q", result["%13"])
	}
}

func TestGetPaneActivityStatus_EmptyState(t *testing.T) {
	// No pane capture state -> empty result.
	tracker := NewIdleTracker()
	cfg := &config.Config{
		NodeActiveSeconds: 300.0,
		NodeIdleSeconds:   900.0,
	}
	result := tracker.GetPaneActivityStatus(cfg)
	if len(result) != 0 {
		t.Errorf("expected empty result for no pane state, got %v", result)
	}
}

func TestGetPongActiveNodes(t *testing.T) {
	tracker := NewIdleTracker()

	// Initially empty
	result := tracker.GetPongActiveNodes()
	if len(result) != 0 {
		t.Errorf("expected empty, got %v", result)
	}

	// Mark PONG received
	tracker.MarkPongReceived("session1:nodeA")
	tracker.MarkPongReceived("session1:nodeB")
	tracker.UpdateSendActivity("session1:nodeC") // No PONG

	result = tracker.GetPongActiveNodes()
	if len(result) != 2 {
		t.Errorf("expected 2, got %d", len(result))
	}
	if !result["session1:nodeA"] || !result["session1:nodeB"] {
		t.Errorf("expected nodeA and nodeB, got %v", result)
	}
	if result["session1:nodeC"] {
		t.Errorf("nodeC should not be active (no PONG)")
	}
}
