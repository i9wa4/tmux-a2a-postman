package sessionidle

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func requireTmux(t *testing.T) {
	t.Helper()
	if err := exec.Command("tmux", "info").Run(); err != nil {
		t.Skip("tmux not available:", err)
	}
}

func TestHashContent(t *testing.T) {
	tests := []struct {
		name   string
		a      string
		b      string
		wantEq bool
	}{
		{"same content produces same hash", "hello", "hello", true},
		{"different content produces different hash", "hello", "world", false},
		{"empty content is consistent", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hashA := hashContent(tt.a)
			hashB := hashContent(tt.b)
			if tt.wantEq && hashA != hashB {
				t.Errorf("expected equal hashes for %q and %q, got %q and %q", tt.a, tt.b, hashA, hashB)
			}
			if !tt.wantEq && hashA == hashB {
				t.Errorf("expected different hashes for %q and %q, but both were %q", tt.a, tt.b, hashA)
			}
		})
	}
}

func TestNewSessionIdleState(t *testing.T) {
	s := NewSessionIdleState()
	if s == nil {
		t.Fatal("expected non-nil SessionIdleState")
	}
	if s.lastAlertMap == nil {
		t.Error("lastAlertMap not initialized")
	}
	if s.lastActivityMap == nil {
		t.Error("lastActivityMap not initialized")
	}
	if s.paneContentHash == nil {
		t.Error("paneContentHash not initialized")
	}
}

func TestCheckSessionIdle_DisabledThreshold(t *testing.T) {
	tests := []struct {
		name      string
		threshold float64
	}{
		{"zero threshold", 0},
		{"negative threshold", -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewSessionIdleState()
			nodes := map[string]discovery.NodeInfo{
				"worker": {PaneID: "%1", SessionName: "test"},
			}
			adjacency := map[string][]string{"worker": {"orchestrator"}}
			got, err := s.CheckSessionIdle(nodes, adjacency, tt.threshold, 10.0)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if got != nil {
				t.Errorf("expected nil idle sessions, got %v", got)
			}
		})
	}
}

func TestCheckSessionIdle_NoConnectedNodes(t *testing.T) {
	requireTmux(t)
	s := NewSessionIdleState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {PaneID: "%fake-test-pane-001", SessionName: "test-session"},
	}
	// Empty adjacency — worker is not connected, skipped
	adjacency := map[string][]string{}

	got, err := s.CheckSessionIdle(nodes, adjacency, 1.0, 10.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no idle sessions, got %v", got)
	}
}

func TestCheckSessionIdle_NodeIdle(t *testing.T) {
	requireTmux(t)
	s := NewSessionIdleState()
	// Pre-seed activity far in the past (exceeds 1s threshold)
	s.lastActivityMap["test-session"] = map[string]time.Time{
		"worker": time.Now().Add(-5 * time.Second),
	}

	// Use fake pane IDs that won't appear in real tmux output
	nodes := map[string]discovery.NodeInfo{
		"worker": {PaneID: "%fake-test-pane-idle-001", SessionName: "test-session"},
	}
	adjacency := map[string][]string{"worker": {"orchestrator"}}

	got, err := s.CheckSessionIdle(nodes, adjacency, 1.0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "test-session" {
		t.Errorf("expected [test-session] idle, got %v", got)
	}
}

func TestCheckSessionIdle_NodeActive(t *testing.T) {
	requireTmux(t)
	s := NewSessionIdleState()
	// Pre-seed activity very recently (within 10s threshold)
	s.lastActivityMap["test-session"] = map[string]time.Time{
		"worker": time.Now().Add(-500 * time.Millisecond),
	}

	nodes := map[string]discovery.NodeInfo{
		"worker": {PaneID: "%fake-test-pane-active-001", SessionName: "test-session"},
	}
	adjacency := map[string][]string{"worker": {"orchestrator"}}

	got, err := s.CheckSessionIdle(nodes, adjacency, 10.0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no idle sessions (node active), got %v", got)
	}
}

func TestCheckSessionIdle_CooldownActive(t *testing.T) {
	requireTmux(t)
	s := NewSessionIdleState()
	// Pre-seed old activity (exceeds threshold)
	s.lastActivityMap["test-session"] = map[string]time.Time{
		"worker": time.Now().Add(-5 * time.Second),
	}
	// Pre-seed recent alert (within cooldown)
	s.lastAlertMap["test-session"] = time.Now().Add(-2 * time.Second)

	nodes := map[string]discovery.NodeInfo{
		"worker": {PaneID: "%fake-test-pane-cooldown-001", SessionName: "test-session"},
	}
	adjacency := map[string][]string{"worker": {"orchestrator"}}

	got, err := s.CheckSessionIdle(nodes, adjacency, 1.0, 10.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no idle sessions (cooldown active), got %v", got)
	}
}

func TestSendWatchdogAlert_NoWatchdog(t *testing.T) {
	tmpDir := t.TempDir()
	nodes := map[string]discovery.NodeInfo{
		"worker": {PaneID: "%1", SessionName: "test-session", SessionDir: tmpDir},
	}
	adjacency := map[string][]string{"worker": {"orchestrator"}}
	cfg := config.DefaultConfig()

	err := SendWatchdogAlert("test-session", nodes, adjacency, tmpDir, "ctx-test", cfg)
	if err == nil {
		t.Error("expected error for missing watchdog node")
	}
}

func TestSendWatchdogAlert_CreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	nodes := map[string]discovery.NodeInfo{
		"worker":   {PaneID: "%1", SessionName: "test-session", SessionDir: tmpDir},
		"watchdog": {PaneID: "%2", SessionName: "test-session", SessionDir: tmpDir},
	}
	adjacency := map[string][]string{
		"watchdog": {"worker"},
	}
	cfg := config.DefaultConfig()

	err := SendWatchdogAlert("test-session", nodes, adjacency, tmpDir, "ctx-test", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	watchdogInbox := filepath.Join(tmpDir, "inbox", "watchdog")
	entries, err := os.ReadDir(watchdogInbox)
	if err != nil {
		t.Fatalf("reading watchdog inbox failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 alert file, got %d", len(entries))
	}
}
