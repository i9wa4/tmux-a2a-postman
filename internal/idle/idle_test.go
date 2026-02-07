package idle

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
)

func TestUpdateActivity(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	idleMutex.Unlock()

	nodeName := "test-node"
	before := time.Now()

	// Test UpdateSendActivity
	UpdateSendActivity(nodeName)

	idleMutex.Lock()
	activity := nodeActivity[nodeName]
	idleMutex.Unlock()

	if activity.LastSent.IsZero() {
		t.Fatalf("send activity not recorded for %s", nodeName)
	}

	if activity.LastSent.Before(before) {
		t.Errorf("send activity time %v is before test start %v", activity.LastSent, before)
	}

	// Test UpdateReceiveActivity
	before2 := time.Now()
	UpdateReceiveActivity(nodeName)

	idleMutex.Lock()
	activity2 := nodeActivity[nodeName]
	idleMutex.Unlock()

	if activity2.LastReceived.IsZero() {
		t.Fatalf("receive activity not recorded for %s", nodeName)
	}

	if activity2.LastReceived.Before(before2) {
		t.Errorf("receive activity time %v is before test start %v", activity2.LastReceived, before2)
	}
}

func TestCheckIdleNodes_NoTimeout(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	lastReminderSent = make(map[string]time.Time)
	idleMutex.Unlock()

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
	UpdateSendActivity("worker")

	// Check idle nodes - should NOT send reminder
	checkIdleNodes(cfg, nil, sessionDir)

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
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	lastReminderSent = make(map[string]time.Time)
	idleMutex.Unlock()

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
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastSent: time.Now().Add(-2 * time.Second),
	}
	idleMutex.Unlock()

	// Check idle nodes - should send reminder
	checkIdleNodes(cfg, nil, sessionDir)

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
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	lastReminderSent = make(map[string]time.Time)
	idleMutex.Unlock()

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
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastSent: time.Now().Add(-2 * time.Second),
	}
	lastReminderSent["worker"] = time.Now().Add(-1 * time.Second) // Within cooldown
	idleMutex.Unlock()

	// Check idle nodes - should NOT send reminder (cooldown active)
	checkIdleNodes(cfg, nil, sessionDir)

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
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	lastReminderSent = make(map[string]time.Time)
	idleMutex.Unlock()

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
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastSent: time.Now().Add(-2 * time.Second),
	}
	idleMutex.Unlock()

	// Update activity (reset timer)
	UpdateSendActivity("worker")

	// Check idle nodes - should NOT send reminder (activity reset)
	checkIdleNodes(cfg, nil, sessionDir)

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
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	nodeName := "test-worker"
	message := "Test idle reminder message"

	if err := sendIdleReminder(nodeName, message, sessionDir); err != nil {
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
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	idleMutex.Unlock()

	// Setup: Node holding ball beyond threshold
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastReceived:        time.Now().Add(-11 * time.Second),
		LastSent:            time.Now().Add(-15 * time.Second),
		PongReceived:        true,
		LastNotifiedDropped: time.Time{}, // Never notified
	}
	idleMutex.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 1 {
		t.Errorf("expected 1 dropped node, got %d", len(dropped))
	}

	if duration, exists := dropped["worker"]; !exists {
		t.Errorf("expected worker to be detected as dropped")
	} else if duration < 11*time.Second {
		t.Errorf("expected duration >= 11s, got %v", duration)
	}
}

func TestCheckDroppedBalls_ThresholdNotExceeded(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	idleMutex.Unlock()

	// Setup: Node holding ball but within threshold
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastReceived: time.Now().Add(-5 * time.Second),
		LastSent:     time.Now().Add(-7 * time.Second),
		PongReceived: true,
	}
	idleMutex.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (threshold not exceeded), got %d", len(dropped))
	}
}

func TestCheckDroppedBalls_CooldownActive(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	idleMutex.Unlock()

	// Setup: Node holding ball beyond threshold, but already notified recently
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastReceived:        time.Now().Add(-11 * time.Second),
		LastSent:            time.Now().Add(-15 * time.Second),
		PongReceived:        true,
		LastNotifiedDropped: time.Now().Add(-5 * time.Second), // Notified 5s ago
	}
	idleMutex.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10, // 10s cooldown
		},
	}

	dropped := CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (cooldown active), got %d", len(dropped))
	}
}

func TestCheckDroppedBalls_NoPongReceived(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	idleMutex.Unlock()

	// Setup: Node holding ball but handshake incomplete (no PONG)
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastReceived: time.Now().Add(-11 * time.Second),
		LastSent:     time.Now().Add(-15 * time.Second),
		PongReceived: false, // No PONG yet
	}
	idleMutex.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  10,
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (PONG not received), got %d", len(dropped))
	}
}

func TestCheckDroppedBalls_DisabledNode(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	nodeActivity = make(map[string]NodeActivity)
	idleMutex.Unlock()

	// Setup: Node holding ball but dropped-ball detection disabled (threshold=0)
	idleMutex.Lock()
	nodeActivity["worker"] = NodeActivity{
		LastReceived: time.Now().Add(-11 * time.Second),
		LastSent:     time.Now().Add(-15 * time.Second),
		PongReceived: true,
	}
	idleMutex.Unlock()

	nodeConfigs := map[string]config.NodeConfig{
		"worker": {
			DroppedBallTimeoutSeconds:  0, // Disabled
			DroppedBallCooldownSeconds: 10,
		},
	}

	dropped := CheckDroppedBalls(nodeConfigs)

	if len(dropped) != 0 {
		t.Errorf("expected no dropped nodes (detection disabled), got %d", len(dropped))
	}
}
