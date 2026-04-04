package notification

import (
	"fmt"
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

func TestBuildNotification_SentinelObfuscation(t *testing.T) {
	// Node template (user-configured) contains the end-of-message sentinel.
	nodeTemplate := "# WORKER\n<!-- end of message -->\nSome content"

	cfg := &config.Config{
		// Protocol wrapper ends with the real sentinel.
		NotificationTemplate: "<!-- message start -->\n{template}\n<!-- end of message -->\n",
		TmuxTimeout:          5.0,
		ReplyCommand:         "postman send --to <recipient>",
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: nodeTemplate},
		},
	}

	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	result := BuildNotification(cfg, adjacency, nodes, "ctx", "worker", "orchestrator", "test", "/path/file.md", livenessMap)

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
		ReplyCommand:         "postman send --to <recipient>",
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
	// Issue #84: Add livenessMap parameter (all active for this test)
	livenessMap := map[string]bool{
		"test:worker":       true,
		"test:orchestrator": true,
	}
	notification := BuildNotification(cfg, adjacency, nodes, "test-ctx", "worker", "orchestrator", "test", "/path/to/session/post/20260204-120000-from-orchestrator-to-worker.md", livenessMap)

	if !strings.Contains(notification, "Message from orchestrator to worker") {
		t.Errorf("notification = %q, want to contain 'Message from orchestrator to worker'", notification)
	}
}

func TestBuildNotification_ReplyCommandExpandsConcreteRecipient(t *testing.T) {
	cfg := &config.Config{
		NotificationTemplate: "Reply: {reply_command}",
		TmuxTimeout:          5.0,
		ReplyCommand:         "tmux-a2a-postman send-message --to <recipient> --body \"<your message>\"",
	}

	notification := BuildNotification(
		cfg,
		map[string][]string{},
		map[string]discovery.NodeInfo{},
		"ctx-notify",
		"worker",
		"orchestrator",
		"test",
		"/path/to/session/post/20260204-120000-from-orchestrator-to-worker.md",
		nil,
	)

	if strings.Contains(notification, "send-message") {
		t.Fatalf("notification still contains legacy send-message: %q", notification)
	}
	if strings.Contains(notification, "<recipient>") {
		t.Fatalf("notification still contains recipient placeholder: %q", notification)
	}
	if !strings.Contains(notification, "send --context-id ctx-notify --to worker") {
		t.Fatalf("notification missing concrete reply target: %q", notification)
	}
}

// TestBuildNotification_LivenessFiltering tests Issue #84 - talks_to_line filtering
func TestBuildNotification_LivenessFiltering(t *testing.T) {
	cfg := &config.Config{
		NotificationTemplate: "Message: {talks_to_line}",
		TmuxTimeout:          5.0,
		ReplyCommand:         "postman send --to <recipient>",
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
		livenessMap     map[string]bool
		wantContains    string
		wantNotContains string
	}{
		{
			name: "All nodes liveness-confirmed",
			livenessMap: map[string]bool{
				"test:orchestrator": true,
				"test:observer":     true,
			},
			wantContains:    "orchestrator, observer",
			wantNotContains: "",
		},
		{
			name: "Only orchestrator liveness-confirmed",
			livenessMap: map[string]bool{
				"test:orchestrator": true,
			},
			wantContains:    "orchestrator",
			wantNotContains: "observer",
		},
		{
			name:            "No nodes liveness-confirmed",
			livenessMap:     map[string]bool{},
			wantContains:    "",
			wantNotContains: "Can talk to",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notification := BuildNotification(cfg, adjacency, nodes, "test-ctx", "worker", "orchestrator", "test", "/path/to/file.md", tt.livenessMap)

			if tt.wantContains != "" && !strings.Contains(notification, tt.wantContains) {
				t.Errorf("notification = %q, want to contain %q", notification, tt.wantContains)
			}
			if tt.wantNotContains != "" && strings.Contains(notification, tt.wantNotContains) {
				t.Errorf("notification = %q, should not contain %q", notification, tt.wantNotContains)
			}
		})
	}
}

func TestStripVT(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string
	}{
		{"clean text", "hello world", false, "hello world"},
		{"NUL byte", "foo\x00bar", false, "foobar"},
		{"ESC+CSI sequence", "foo\x1b[2Jbar", false, "foobar"},
		{"DEL byte", "foo\x7fbar", false, "foobar"},
		{"C1 byte (U+009B)", "foo\xc2\x9bbar", false, "foobar"},
		{"OSC sequence BEL terminated", "foo\x1b]0;title\x07bar", false, "foobar"},
		{"OSC sequence ST terminated", "foo\x1b]0;title\x1b\\bar", false, "foobar"},
		{"DCS sequence ST terminated", "foo\x1bPdata\x1b\\bar", false, "foobar"},
		{"malformed UTF-8 overlong", "foo\xc0\x80bar", true, ""},
		{"lone continuation byte", "foo\x80bar", true, ""},
		{"valid multibyte UTF-8", "foo\xe6\x97\xa5bar", false, "foo\u65e5bar"},
		{"LF preserved", "foo\nbar", false, "foo\nbar"},
		{"CRLF normalized to LF", "foo\r\nbar", false, "foo\nbar"},
		{"C0 control stripped", "foo\x01bar", false, "foobar"},
		{"two-byte ESC sequence", "foo\x1b@bar", false, "foobar"},
		{"TAB stripped", "foo\tbar", false, "foobar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := StripVT(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("StripVT(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("StripVT(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSendToPane_InvalidPane(t *testing.T) {
	// Test that SendToPane gracefully handles invalid pane
	err := SendToPane("invalid-pane", "test message", 100*time.Millisecond, 1*time.Second, 1, true, 0, 0)
	if err == nil {
		t.Error("SendToPane() with invalid pane should return error")
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

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 2, true, 0, 0)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected calls: set-buffer, paste-buffer -t %99, send-keys -t %99 C-m, send-keys -t %99 C-m
	if len(lines) != 6 {
		t.Errorf("expected 6 lines (set-buffer+2 sentinel lines+paste-buffer+2 send-keys), got %d: %v", len(lines), lines)
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

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 3, true, 0, 0)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected calls: set-buffer, paste-buffer, send-keys x3
	if len(lines) != 7 {
		t.Errorf("expected 7 lines (set-buffer+2 sentinel lines+paste-buffer+3 send-keys), got %d: %v", len(lines), lines)
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

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 1, true, 0, 0)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected calls: set-buffer, paste-buffer, send-keys x1
	if len(lines) != 5 {
		t.Errorf("expected 5 lines (set-buffer+2 sentinel lines+paste-buffer+1 send-keys), got %d: %v", len(lines), lines)
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

	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, 0, true, 0, 0)
	if err != nil {
		t.Fatalf("SendToPane failed: %v", err)
	}

	argsData, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ReadFile argsFile failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(argsData)), "\n")

	// Expected: set-buffer, paste-buffer, send-keys x1 (0 treated same as 1)
	if len(lines) != 5 {
		t.Errorf("expected 5 lines (enterCount=0 sends one Enter), got %d: %v", len(lines), lines)
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
	err := SendToPane("%99", "hello", 1*time.Millisecond, 1*time.Second, -1, true, 0, 0)
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
