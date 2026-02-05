package notification

import (
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestBuildNotification(t *testing.T) {
	cfg := &config.Config{
		NotificationTemplate: "Message from {from_node} to {node}",
		TmuxTimeout:          5.0,
		ReplyCommand:         "postman create-draft --to <recipient>",
	}

	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}

	// Issue #33: nodes map now uses session-prefixed keys
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {
			PaneID:      "%1",
			SessionName: "test",
		},
		"test:orchestrator": {
			PaneID:      "%2",
			SessionName: "test",
		},
	}

	// sourceSessionName is "test"
	notification := BuildNotification(cfg, adjacency, nodes, "test-ctx", "worker", "orchestrator", "test", "/path/to/session/post/20260204-120000-from-orchestrator-to-worker.md")

	if !strings.Contains(notification, "Message from orchestrator to worker") {
		t.Errorf("notification = %q, want to contain 'Message from orchestrator to worker'", notification)
	}
}

func TestExtractTimestamp(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"20260204-120000-from-orchestrator-to-worker.md", "20260204-120000"},
		{"/path/to/20260204-120000-from-orchestrator-to-worker.md", "20260204-120000"},
		{"invalid.md", ""},
	}

	for _, tt := range tests {
		got := extractTimestamp(tt.filename)
		if got != tt.want {
			t.Errorf("extractTimestamp(%q) = %q, want %q", tt.filename, got, tt.want)
		}
	}
}

func TestSanitizeForTmux(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"hello $USER", "hello \\$USER"},
		{"hello `whoami`", "hello \\`whoami\\`"},
		{"hello \"world\"", "hello \\\"world\\\""},
		{"hello\\world", "hello\\\\world"},
	}

	for _, tt := range tests {
		got := sanitizeForTmux(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeForTmux(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSendToPane_InvalidPane(t *testing.T) {
	// Test that SendToPane gracefully handles invalid pane
	err := SendToPane("invalid-pane", "test message", 100*time.Millisecond, 1*time.Second)
	if err == nil {
		t.Error("SendToPane() with invalid pane should return error")
	}
}
