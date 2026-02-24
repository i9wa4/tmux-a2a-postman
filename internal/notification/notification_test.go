package notification

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestResolveEnterCount(t *testing.T) {
	tests := []struct {
		name       string
		configured int
		runtime    string
		probeErr   bool
		want       int
	}{
		{"configured=0, codex", 0, "codex", false, 2},
		{"configured=0, bash", 0, "bash", false, 1},
		{"configured=0, probe error", 0, "", true, 1},
		{"configured=1, no probe", 1, "", false, 1},
		{"configured=2, codex", 2, "codex", false, 2},
		{"configured=2, bash", 2, "bash", false, 1},
		{"configured=2, probe error", 2, "", true, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			probeCalled := false
			probe := func() (string, error) {
				probeCalled = true
				if tt.probeErr {
					return "", fmt.Errorf("probe error")
				}
				return tt.runtime, nil
			}
			got := ResolveEnterCount(tt.configured, probe)
			if got != tt.want {
				t.Errorf("ResolveEnterCount(%d, probe) = %d, want %d", tt.configured, got, tt.want)
			}
			// configured=1 must not call probe
			if tt.configured == 1 && probeCalled {
				t.Error("probe should not be called when configured=1")
			}
		})
	}
}

func TestBuildNotification_MaterializedPath(t *testing.T) {
	matPath := "/fake/path/to/worker.md"

	cfg := &config.Config{
		// Use {template} at the end to simulate the vulnerable case
		NotificationTemplate: "header\n{template}",
		TmuxTimeout:          5.0,
		ReplyCommand:         "postman create-draft --to <recipient>",
		MaterializedPaths: map[string]string{
			"worker": matPath,
		},
	}

	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{
		"test:worker": {PaneID: "%1", SessionName: "test"},
	}
	pongActiveNodes := map[string]bool{}

	result := BuildNotification(cfg, adjacency, nodes, "test-ctx", "worker", "orchestrator", "test", "/path/file.md", pongActiveNodes)

	// Labeled path reference must appear (no @ prefix)
	if !strings.Contains(result, "Role template: "+matPath) {
		t.Errorf("expected labeled path in result, got: %q", result)
	}
	// Result must NOT contain @path (triggers autocomplete)
	if strings.Contains(result, "@"+matPath) {
		t.Errorf("result must not contain @path (triggers autocomplete): %q", result)
	}
}

func TestBuildNotification_SentinelObfuscation(t *testing.T) {
	// Node template (user-configured) contains the end-of-message sentinel.
	nodeTemplate := "# WORKER\n<!-- end of message -->\nSome content"

	cfg := &config.Config{
		// Protocol wrapper ends with the real sentinel.
		NotificationTemplate: "<!-- message start -->\n{template}\n<!-- end of message -->\n",
		TmuxTimeout:          5.0,
		ReplyCommand:         "postman create-draft --to <recipient>",
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: nodeTemplate},
		},
	}

	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	result := BuildNotification(cfg, adjacency, nodes, "ctx", "worker", "orchestrator", "test", "/path/file.md", pongActiveNodes)

	// User content sentinel must be obfuscated.
	if strings.Contains(result, "# WORKER\n<!-- end of message -->") {
		t.Errorf("user template sentinel was not obfuscated; result: %q", result)
	}
	if !strings.Contains(result, "<!-- end of msg -->") {
		t.Errorf("expected obfuscated sentinel in result; got: %q", result)
	}
	// Protocol wrapper sentinel must remain intact at the end.
	if !strings.HasSuffix(strings.TrimRight(result, "\n"), "<!-- end of message -->") {
		t.Errorf("protocol sentinel was altered or missing; result: %q", result)
	}
}

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
	err := SendToPane("invalid-pane", "test message", 100*time.Millisecond, 1*time.Second, 1)
	if err == nil {
		t.Error("SendToPane() with invalid pane should return error")
	}
}

func TestBuildNotification_WrapperPresent(t *testing.T) {
	// PingTemplate missing both protocol wrapper markers — warning should fire
	// when sender == "postman" (ping path).
	cfg := &config.Config{
		PingTemplate: "plain text without markers",
		TmuxTimeout:  5.0,
		ReplyCommand: "postman create-draft",
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	pongActiveNodes := map[string]bool{}

	// Redirect stderr to capture warning output.
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w

	BuildNotification(cfg, adjacency, nodes, "ctx", "worker", "postman", "test", "/path/file.md", pongActiveNodes)

	_ = w.Close()
	os.Stderr = origStderr

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)

	if !strings.Contains(buf.String(), "missing protocol wrapper markers") {
		t.Errorf("expected missing-markers warning, stderr: %q", buf.String())
	}
}

func TestSendToPane_EnterCount2(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	fakeTmux := filepath.Join(tmpDir, "tmux")
	// Fake tmux: always succeeds, records each invocation on a separate line
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fakeTmux failed: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 2)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected calls: set-buffer, paste-buffer -t %99, send-keys -t %99 C-m, send-keys -t %99 C-m
	if len(lines) != 4 {
		t.Errorf("expected 4 tmux calls, got %d: %v", len(lines), lines)
	}

	// Count send-keys invocations (should be exactly 2)
	sendKeyCount := 0
	for _, line := range lines {
		if strings.Contains(line, "send-keys") {
			sendKeyCount++
		}
	}
	if sendKeyCount != 2 {
		t.Errorf("expected 2 send-keys calls (enterCount=2), got %d; lines: %v", sendKeyCount, lines)
	}
}

func TestSendToPane_EnterCount3(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	fakeTmux := filepath.Join(tmpDir, "tmux")
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fakeTmux failed: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 3)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected calls: set-buffer, paste-buffer, send-keys x3
	if len(lines) != 5 {
		t.Errorf("expected 5 tmux calls (set-buffer+paste-buffer+3 send-keys), got %d: %v", len(lines), lines)
	}

	sendKeyCount := 0
	for _, line := range lines {
		if strings.Contains(line, "send-keys") {
			sendKeyCount++
		}
	}
	if sendKeyCount != 3 {
		t.Errorf("expected 3 send-keys calls (enterCount=3), got %d; lines: %v", sendKeyCount, lines)
	}
}

func TestSendToPane_EnterCount1(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	fakeTmux := filepath.Join(tmpDir, "tmux")
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fakeTmux failed: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 1)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected calls: set-buffer, paste-buffer, send-keys x1
	if len(lines) != 3 {
		t.Errorf("expected 3 tmux calls (set-buffer+paste-buffer+1 send-keys), got %d: %v", len(lines), lines)
	}

	sendKeyCount := 0
	for _, line := range lines {
		if strings.Contains(line, "send-keys") {
			sendKeyCount++
		}
	}
	if sendKeyCount != 1 {
		t.Errorf("expected 1 send-keys call (enterCount=1), got %d; lines: %v", sendKeyCount, lines)
	}
}

func TestSendToPane_EnterCount0(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	fakeTmux := filepath.Join(tmpDir, "tmux")
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fakeTmux failed: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 0)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected: set-buffer, paste-buffer, send-keys x1 (0 treated same as 1)
	if len(lines) != 3 {
		t.Errorf("expected 3 tmux calls (enterCount=0 sends one Enter), got %d: %v", len(lines), lines)
	}

	sendKeyCount := 0
	for _, line := range lines {
		if strings.Contains(line, "send-keys") {
			sendKeyCount++
		}
	}
	if sendKeyCount != 1 {
		t.Errorf("expected 1 send-keys call (enterCount=0), got %d; lines: %v", sendKeyCount, lines)
	}
}

func TestSendToPane_EnterCountNegative(t *testing.T) {
	tmpDir := t.TempDir()
	argsFile := filepath.Join(tmpDir, "args.txt")
	fakeTmux := filepath.Join(tmpDir, "tmux")
	script := "#!/bin/sh\necho \"$@\" >> " + argsFile + "\n"
	if err := os.WriteFile(fakeTmux, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fakeTmux failed: %v", err)
	}
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	// Negative enterCount: loop does not execute, no panic; sends exactly 1 Enter
	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, -1)
	if err != nil {
		t.Fatalf("SendToPane with negative enterCount should not error: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	sendKeyCount := 0
	for _, line := range lines {
		if strings.Contains(line, "send-keys") {
			sendKeyCount++
		}
	}
	if sendKeyCount != 1 {
		t.Errorf("expected 1 send-keys call for negative enterCount, got %d; lines: %v", sendKeyCount, lines)
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
