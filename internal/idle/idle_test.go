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
	lastActivity = make(map[string]time.Time)
	idleMutex.Unlock()

	nodeName := "test-node"
	before := time.Now()

	UpdateActivity(nodeName)

	idleMutex.Lock()
	activityTime, exists := lastActivity[nodeName]
	idleMutex.Unlock()

	if !exists {
		t.Fatalf("activity not recorded for %s", nodeName)
	}

	if activityTime.Before(before) {
		t.Errorf("activity time %v is before test start %v", activityTime, before)
	}
}

func TestCheckIdleNodes_NoTimeout(t *testing.T) {
	// Reset state
	idleMutex.Lock()
	lastActivity = make(map[string]time.Time)
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
	UpdateActivity("worker")

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
	lastActivity = make(map[string]time.Time)
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
	lastActivity["worker"] = time.Now().Add(-2 * time.Second)
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
	lastActivity = make(map[string]time.Time)
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
	lastActivity["worker"] = time.Now().Add(-2 * time.Second)
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
	lastActivity = make(map[string]time.Time)
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
	lastActivity["worker"] = time.Now().Add(-2 * time.Second)
	idleMutex.Unlock()

	// Update activity (reset timer)
	UpdateActivity("worker")

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
