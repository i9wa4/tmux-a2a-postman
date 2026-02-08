package compaction

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
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

func TestSendCompactionNotification(t *testing.T) {
	tracker := NewCompactionTracker()
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		CompactionDetection: config.CompactionDetectionConfig{
			Enabled:      true,
			Pattern:      "auto-compact",
			DelaySeconds: 0,
			MessageTemplate: config.CompactionMessageTemplate{
				Type: "compaction-recovery",
				Body: "Compaction detected for node {node}. Observers: please send status update.",
			},
		},
	}

	observerName := "observer-test"
	affectedNode := "worker-node"

	if err := tracker.sendCompactionNotification(observerName, affectedNode, cfg, sessionDir); err != nil {
		t.Fatalf("sendCompactionNotification failed: %v", err)
	}

	// Verify notification file created
	inboxDir := filepath.Join(sessionDir, "inbox", observerName)
	entries, err := os.ReadDir(inboxDir)
	if err != nil {
		t.Fatalf("reading inbox failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 notification file, got %d", len(entries))
	}

	// Verify file content
	content, err := os.ReadFile(filepath.Join(inboxDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading notification file failed: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "Compaction detected for node worker-node") {
		t.Errorf("notification missing expected message, got: %s", contentStr)
	}

	if !strings.Contains(contentStr, "from: postman") {
		t.Errorf("notification missing 'from: postman', got: %s", contentStr)
	}

	if !strings.Contains(contentStr, "type: compaction-recovery") {
		t.Errorf("notification missing 'type: compaction-recovery', got: %s", contentStr)
	}
}

func TestNotifyObserversOfCompaction(t *testing.T) {
	tracker := NewCompactionTracker()
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		CompactionDetection: config.CompactionDetectionConfig{
			Enabled:      true,
			Pattern:      "auto-compact",
			DelaySeconds: 0,
			MessageTemplate: config.CompactionMessageTemplate{
				Type: "compaction-recovery",
				Body: "Compaction detected for node {node}.",
			},
		},
		Nodes: map[string]config.NodeConfig{
			"observer-1": {
				Observes: []string{"worker-node", "other-node"},
			},
			"observer-2": {
				Observes: []string{"other-node"}, // Does not observe worker-node
			},
			"observer-3": {
				Observes: []string{}, // No observes (will be skipped)
			},
		},
	}

	nodes := map[string]discovery.NodeInfo{
		"worker-node": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	affectedNode := "worker-node"

	tracker.notifyObserversOfCompaction(affectedNode, cfg, nodes, sessionDir)

	// Verify observer-1 received notification
	inbox1 := filepath.Join(sessionDir, "inbox", "observer-1")
	entries1, err := os.ReadDir(inbox1)
	if err != nil {
		t.Fatalf("reading observer-1 inbox failed: %v", err)
	}
	if len(entries1) != 1 {
		t.Errorf("observer-1: expected 1 notification, got %d", len(entries1))
	}

	// Verify observer-2 did NOT receive notification (does not observe worker-node)
	inbox2 := filepath.Join(sessionDir, "inbox", "observer-2")
	entries2, err := os.ReadDir(inbox2)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading observer-2 inbox failed: %v", err)
	}
	if len(entries2) != 0 {
		t.Errorf("observer-2: expected 0 notifications, got %d", len(entries2))
	}

	// Verify observer-3 did NOT receive notification (not subscribed)
	inbox3 := filepath.Join(sessionDir, "inbox", "observer-3")
	entries3, err := os.ReadDir(inbox3)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading observer-3 inbox failed: %v", err)
	}
	if len(entries3) != 0 {
		t.Errorf("observer-3: expected 0 notifications, got %d", len(entries3))
	}
}

func TestCompactionDelay(t *testing.T) {
	tracker := NewCompactionTracker()
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		CompactionDetection: config.CompactionDetectionConfig{
			Enabled:      true,
			Pattern:      "auto-compact",
			DelaySeconds: 1.0, // 1 second delay
			MessageTemplate: config.CompactionMessageTemplate{
				Type: "compaction-recovery",
				Body: "Compaction detected for node {node}.",
			},
		},
		Nodes: map[string]config.NodeConfig{
			"observer-test": {
				Observes: []string{"worker-node"},
			},
		},
	}

	nodes := map[string]discovery.NodeInfo{
		"worker-node": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	affectedNode := "worker-node"

	// Trigger notification (should have 1 second delay)
	start := time.Now()
	tracker.notifyObserversOfCompaction(affectedNode, cfg, nodes, sessionDir)

	// Check immediately - should have no notification yet
	inbox := filepath.Join(sessionDir, "inbox", "observer-test")
	entries, err := os.ReadDir(inbox)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading inbox failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 notifications immediately after trigger, got %d", len(entries))
	}

	// Wait for delay + buffer
	time.Sleep(1500 * time.Millisecond)

	// Check after delay - should have notification now
	entries, err = os.ReadDir(inbox)
	if err != nil {
		t.Fatalf("reading inbox after delay failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 notification after delay, got %d", len(entries))
	}

	elapsed := time.Since(start)
	if elapsed < 1*time.Second {
		t.Errorf("notification sent too early: elapsed=%v, expected>=1s", elapsed)
	}
}

func TestCompactionDetection_Disabled(t *testing.T) {
	tracker := NewCompactionTracker()
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")
	if err := config.CreateSessionDirs(sessionDir); err != nil {
		t.Fatalf("config.CreateSessionDirs failed: %v", err)
	}

	cfg := &config.Config{
		CompactionDetection: config.CompactionDetectionConfig{
			Enabled:      false, // Disabled
			Pattern:      "auto-compact",
			DelaySeconds: 0,
		},
		Nodes: map[string]config.NodeConfig{
			"observer-test": {
				Observes: []string{"worker-node"},
			},
		},
	}

	nodes := map[string]discovery.NodeInfo{
		"worker-node": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	// Start compaction check (should do nothing when disabled)
	ctx := context.Background()
	tracker.StartCompactionCheck(ctx, cfg, nodes, sessionDir)

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Verify no notifications sent
	inbox := filepath.Join(sessionDir, "inbox", "observer-test")
	entries, err := os.ReadDir(inbox)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("reading inbox failed: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 notifications when disabled, got %d", len(entries))
	}
}
