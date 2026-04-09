package reminder

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
	"github.com/i9wa4/tmux-a2a-postman/internal/notification"
	"github.com/i9wa4/tmux-a2a-postman/internal/template"
)

func TestNewReminderState(t *testing.T) {
	state := NewReminderState()
	if state == nil {
		t.Fatal("NewReminderState() returned nil")
	}
	if state.counters == nil {
		t.Error("counters map should be initialized")
	}
}

func TestReminderIncrement(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 5.0,
		ReminderMessage:  "REMINDER: You have {count} pending messages",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Increment counter below threshold
	for i := 1; i <= 3; i++ {
		state.Increment("worker", "", nodes, cfg)
		state.mu.Lock()
		count := state.counters["worker"]
		state.mu.Unlock()
		if count != i {
			t.Errorf("After %d increments, counter = %d, want %d", i, count, i)
		}
	}
}

func TestReminderThreshold(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 3.0, // Threshold = 3
		ReminderMessage:  "REMINDER: You have {count} pending messages",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Increment to threshold
	for i := 1; i <= 3; i++ {
		state.Increment("worker", "", nodes, cfg)
	}

	// After reaching threshold, counter should be reset to 0
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 0 {
		t.Errorf("After reaching threshold, counter = %d, want 0 (should be reset)", count)
	}

	// Increment again - should start from 0
	state.Increment("worker", "", nodes, cfg)
	state.mu.Lock()
	count = state.counters["worker"]
	state.mu.Unlock()

	if count != 1 {
		t.Errorf("After reset and increment, counter = %d, want 1", count)
	}
}

func TestReminderNodeSpecificConfig(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 2.0, // Global threshold (set to match node-specific)
		ReminderMessage:  "GLOBAL REMINDER",
		Nodes: map[string]config.NodeConfig{
			"worker": {
				ReminderInterval: 2.0, // Node-specific threshold
				ReminderMessage:  "WORKER REMINDER: {count} messages",
			},
		},
	}

	// Increment to node-specific threshold (2)
	state.Increment("worker", "", nodes, cfg)
	state.Increment("worker", "", nodes, cfg)

	// After reaching node-specific threshold, counter should be reset
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 0 {
		t.Errorf("After reaching node-specific threshold, counter = %d, want 0", count)
	}
}

func TestReminderThreadSafety(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 101.0, // High threshold to avoid reset during test (> 100)
		ReminderMessage:  "REMINDER",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Concurrent increments
	var wg sync.WaitGroup
	numGoroutines := 10
	incrementsPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < incrementsPerGoroutine; j++ {
				state.Increment("worker", "", nodes, cfg)
			}
		}()
	}

	wg.Wait()

	// Verify final count
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	expected := numGoroutines * incrementsPerGoroutine
	if count != expected {
		t.Errorf("After concurrent increments, counter = %d, want %d", count, expected)
	}
}

func TestReminderDisabled(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}

	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 0.0, // Disabled
		ReminderMessage:  "",
		Nodes:            map[string]config.NodeConfig{},
	}

	// Increment many times
	for i := 0; i < 10; i++ {
		state.Increment("worker", "", nodes, cfg)
	}

	// Counter should keep incrementing (no reset since reminder is disabled)
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 10 {
		t.Errorf("With reminder disabled, counter = %d, want 10", count)
	}
}

func TestReminderPhaseTwoLookup(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"test-session:worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}
	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 2.0,
		ReminderMessage:  "",
		Nodes:            map[string]config.NodeConfig{},
	}
	state.Increment("worker", "test-session", nodes, cfg)
	state.Increment("worker", "test-session", nodes, cfg)
	state.mu.Lock()
	count := state.counters["test-session:worker"]
	state.mu.Unlock()
	if count != 0 {
		t.Errorf("Phase 2 lookup: after threshold, counter = %d, want 0", count)
	}
}

func TestTemplateExpandTemplate(t *testing.T) {
	vars := map[string]string{
		"node":     "worker",
		"count":    "5",
		"template": "# WORKER ROLE",
	}
	msg := template.ExpandTemplate("{template} count:{count}", vars, 5*time.Second, false)
	if strings.Contains(msg, "{template}") {
		t.Errorf("expected {template} to be expanded, got: %s", msg)
	}
	if !strings.Contains(msg, "# WORKER ROLE") {
		t.Errorf("expected template content in output, got: %s", msg)
	}
}

func TestReminderDoesNotFallbackAcrossSessionsWithoutSessionName(t *testing.T) {
	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"test-session:worker": {
			PaneID:      "%100",
			SessionName: "test-session",
		},
	}
	cfg := &config.Config{
		TmuxTimeout:      5.0,
		ReminderInterval: 2.0,
		ReminderMessage:  "",
		Nodes:            map[string]config.NodeConfig{},
	}
	state.Increment("worker", "", nodes, cfg)
	state.Increment("worker", "", nodes, cfg)
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()
	if count != 2 {
		t.Errorf("cross-session fallback should not reset counter, got %d want 2", count)
	}
}

func TestReminderIncrement_BlocksShellExpansionForUntrustedReminderTemplate(t *testing.T) {
	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	projectDir := filepath.Join(root, "project")
	xdgConfigHome := filepath.Join(root, "xdg")
	xdgConfigDir := filepath.Join(xdgConfigHome, "tmux-a2a-postman")
	localConfigDir := filepath.Join(projectDir, ".tmux-a2a-postman")
	markerPath := filepath.Join(root, "shell-marker")

	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(homeDir): %v", err)
	}
	if err := os.MkdirAll(xdgConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(xdgConfigDir): %v", err)
	}
	if err := os.MkdirAll(localConfigDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(localConfigDir): %v", err)
	}

	xdgConfig := "[postman]\nallow_shell_templates = true\n\n[worker]\nrole = \"worker\"\ntemplate = \"worker\"\n"
	if err := os.WriteFile(filepath.Join(xdgConfigDir, "postman.toml"), []byte(xdgConfig), 0o600); err != nil {
		t.Fatalf("WriteFile(xdg postman.toml): %v", err)
	}

	localConfig := "[postman]\nreminder_interval_messages = 1\nreminder_message = \"REM $(printf x > " + markerPath + ")\"\n"
	if err := os.WriteFile(filepath.Join(localConfigDir, "postman.toml"), []byte(localConfig), 0o600); err != nil {
		t.Fatalf("WriteFile(local postman.toml): %v", err)
	}

	scriptDir := t.TempDir()
	logPath := filepath.Join(root, "tmux.log")
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"LOGFILE='" + logPath + "'\n" +
		"if [ \"$1\" = 'display-message' ]; then\n" +
		"  printf '%s\\n' 'bash'\n" +
		"  exit 0\n" +
		"fi\n" +
		"if [ \"$1\" = 'set-buffer' ] || [ \"$1\" = 'paste-buffer' ] || [ \"$1\" = 'send-keys' ]; then\n" +
		"  printf '%s\\n' \"$*\" >> \"$LOGFILE\"\n" +
		"  exit 0\n" +
		"fi\n" +
		"exit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}

	t.Chdir(projectDir)
	t.Setenv("HOME", homeDir)
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	cfg, err := config.LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig(\"\"): %v", err)
	}
	if cfg.AllowShellForReminderMessage("worker") {
		t.Fatal("AllowShellForReminderMessage(worker) = true, want false for project-local override")
	}

	notification.InitPaneCooldown(0)
	t.Cleanup(func() {
		notification.InitPaneCooldown(10 * time.Minute)
	})

	state := NewReminderState()
	nodes := map[string]discovery.NodeInfo{
		"worker": {
			PaneID:      "%100",
			SessionName: "test-session",
			SessionDir:  filepath.Join(root, "ctx-01", "test-session"),
		},
	}
	state.Increment("worker", "test-session", nodes, cfg)

	if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
		t.Fatalf("untrusted reminder template executed shell command: %v", err)
	}
}

func TestDefaultReminderMessageDoesNotExposeInboxPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	t.Setenv("HOME", filepath.Join(tmpDir, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmpDir, "xdg"))

	cfg, err := config.LoadConfig("")
	if err != nil {
		t.Fatalf("LoadConfig(\"\"): %v", err)
	}

	inboxPath := filepath.Join(tmpDir, "ctx-123", "internal", "inbox", "messenger")
	msg := template.ExpandTemplate(cfg.ReminderMessage, map[string]string{
		"count":      "3",
		"inbox_path": inboxPath,
	}, 5*time.Second, false)

	if strings.Contains(msg, inboxPath) {
		t.Fatalf("default reminder leaked inbox path: %q", msg)
	}
	if !strings.Contains(msg, "tmux-a2a-postman pop") {
		t.Fatalf("default reminder should stay actionable, got %q", msg)
	}
}
