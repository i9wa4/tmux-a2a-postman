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
	// Issue #84: Add pongActiveNodes parameter (all active for this test)
	pongActiveNodes := map[string]bool{
		"test:worker":       true,
		"test:orchestrator": true,
	}
	notification := BuildNotification(cfg, adjacency, nodes, "test-ctx", "worker", "orchestrator", "test", "/path/to/session/post/20260204-120000-from-orchestrator-to-worker.md", pongActiveNodes)

	if !strings.Contains(notification, "Message from orchestrator to worker") {
		t.Errorf("notification = %q, want to contain 'Message from orchestrator to worker'", notification)
	}
}

// TestBuildNotification_PongActiveFiltering tests Issue #84 - talks_to_line filtering
func TestBuildNotification_PongActiveFiltering(t *testing.T) {
	cfg := &config.Config{
		NotificationTemplate: "Message: {talks_to_line}",
		TmuxTimeout:          5.0,
		ReplyCommand:         "postman create-draft --to <recipient>",
	}

	adjacency := map[string][]string{
		"worker": {"orchestrator", "observer"},
	}

	nodes := map[string]discovery.NodeInfo{
		"test:worker": {
			PaneID:      "%1",
			SessionName: "test",
		},
		"test:orchestrator": {
			PaneID:      "%2",
			SessionName: "test",
		},
		"test:observer": {
			PaneID:      "%3",
			SessionName: "test",
		},
	}

	tests := []struct {
		name            string
		pongActiveNodes map[string]bool
		wantContains    string
		wantNotContains string
	}{
		{
			name: "All nodes PONG-active",
			pongActiveNodes: map[string]bool{
				"test:orchestrator": true,
				"test:observer":     true,
			},
			wantContains:    "orchestrator, observer",
			wantNotContains: "",
		},
		{
			name: "Only orchestrator PONG-active",
			pongActiveNodes: map[string]bool{
				"test:orchestrator": true,
			},
			wantContains:    "orchestrator",
			wantNotContains: "observer",
		},
		{
			name:            "No nodes PONG-active",
			pongActiveNodes: map[string]bool{},
			wantContains:    "",
			wantNotContains: "Can talk to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notification := BuildNotification(cfg, adjacency, nodes, "test-ctx", "worker", "orchestrator", "test", "/path/to/file.md", tt.pongActiveNodes)

			if tt.wantContains != "" && !strings.Contains(notification, tt.wantContains) {
				t.Errorf("notification = %q, want to contain %q", notification, tt.wantContains)
			}
			if tt.wantNotContains != "" && strings.Contains(notification, tt.wantNotContains) {
				t.Errorf("notification = %q, should not contain %q", notification, tt.wantNotContains)
			}
		})
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

// TestBuildNotification_FromInjection tests --from injection in reply_command
func TestBuildNotification_FromInjection(t *testing.T) {
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%1", SessionName: "test"},
	}
	adjacency := map[string][]string{}
	pongActiveNodes := map[string]bool{}

	tests := []struct {
		name        string
		replyCmd    string
		recipient   string
		wantContain string
		wantCount   int
	}{
		{
			name:        "inject --from before --to when absent",
			replyCmd:    "postman create-draft --to <recipient>",
			recipient:   "worker",
			wantContain: "--from worker --to",
			wantCount:   1,
		},
		{
			name:        "skip injection when --from already present",
			replyCmd:    "postman create-draft --from worker --to <recipient>",
			recipient:   "worker",
			wantContain: "--from worker",
			wantCount:   1,
		},
		{
			name:        "inject --from at end when no --to",
			replyCmd:    "postman create-draft",
			recipient:   "worker",
			wantContain: "--from worker",
			wantCount:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				NotificationTemplate: "{reply_command}",
				TmuxTimeout:          5.0,
				ReplyCommand:         tt.replyCmd,
			}
			notification := BuildNotification(cfg, adjacency, nodes, "ctx", tt.recipient, "sender", "test", "/path/file.md", pongActiveNodes)
			if !strings.Contains(notification, tt.wantContain) {
				t.Errorf("notification = %q, want to contain %q", notification, tt.wantContain)
			}
			// Verify --from appears exactly once
			count := strings.Count(notification, "--from")
			if count != tt.wantCount {
				t.Errorf("notification has %d --from occurrences, want %d; notification = %q", count, tt.wantCount, notification)
			}
		})
	}
}
