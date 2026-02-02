package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain_Version(t *testing.T) {
	cmd := exec.Command("go", "build", "-ldflags", "-X main.revision=test123", "-o", "postman_test_bin", ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	defer func() { _ = os.Remove("postman_test_bin") }()

	out, err := exec.Command("./postman_test_bin", "version").CombinedOutput()
	if err != nil {
		t.Fatalf("version subcommand failed: %v\n%s", err, out)
	}

	expected := "postman dev (rev: test123)\n"
	if string(out) != expected {
		t.Errorf("got %q, want %q", string(out), expected)
	}
}

func TestMain_NoArgs(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "postman_test_bin", ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	defer func() { _ = os.Remove("postman_test_bin") }()

	out, err := exec.Command("./postman_test_bin").CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit code for no args")
	}

	if len(out) == 0 {
		t.Error("expected usage message on stderr, got empty output")
	}
}
func TestGetNodeFromProcess(t *testing.T) {
	const testNode = "test-worker-node"

	// Build a clean env without any existing A2A_NODE, then add our test value.
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "A2A_NODE=") {
			env = append(env, e)
		}
	}
	env = append(env, "A2A_NODE="+testNode)

	// Spawn a child process with A2A_NODE set, then read it back via
	// the platform-specific getNodeFromProcessOS.
	cmd := exec.Command("sleep", "30")
	cmd.Env = env
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start child process: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	// Give the OS time to register the process.
	time.Sleep(100 * time.Millisecond)

	pid := strconv.Itoa(cmd.Process.Pid)
	got := getNodeFromProcessOS(pid)

	// NOTE: On macOS, recent versions restrict ps eww from showing
	// environment variables. Skip rather than fail on such platforms.
	if got == "" {
		t.Skipf("getNodeFromProcessOS returned empty; platform may restrict env reading (pid=%s)", pid)
	}
	if got != testNode {
		t.Errorf("getNodeFromProcessOS(%s): got %q, want %q", pid, got, testNode)
	}
}

func TestGetNodeFromProcess_NotSet(t *testing.T) {
	got := getNodeFromProcessOS("999999999")
	if got != "" {
		t.Errorf("expected empty string for non-existent PID, got %q", got)
	}
}

func TestBuildPingMessage(t *testing.T) {
	template := "PING {node} in context {context_id}"
	vars := map[string]string{
		"node":       "worker",
		"context_id": "session-123",
	}
	timeout := 1 * time.Second

	got := buildPingMessage(template, vars, timeout)
	want := "PING worker in context session-123"
	if got != want {
		t.Errorf("buildPingMessage() = %q, want %q", got, want)
	}
}

func TestReminder(t *testing.T) {
	state := NewReminderState()
	cfg := &Config{
		ReminderInterval: 3,
		ReminderMessage:  "Reminder: {node} has {count} messages",
		TmuxTimeout:      1.0,
		Nodes:            make(map[string]NodeConfig),
	}
	nodes := map[string]NodeInfo{"worker": {PaneID: "%999", SessionName: "test", SessionDir: "/tmp/test"}}

	// Increment 3 times to reach threshold
	for i := 0; i < 3; i++ {
		state.Increment("worker", nodes, cfg)
	}

	// Counter should be reset after sending reminder
	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 0 {
		t.Errorf("counter after reminder: got %d, want 0", count)
	}
}

func TestReminder_ThreadSafety(t *testing.T) {
	state := NewReminderState()
	cfg := &Config{
		ReminderInterval: 1000, // High threshold to avoid sending reminders
		TmuxTimeout:      1.0,
		Nodes:            make(map[string]NodeConfig),
	}
	nodes := map[string]NodeInfo{"worker": {PaneID: "%999", SessionName: "test", SessionDir: "/tmp/test"}}

	// Concurrent increments
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state.Increment("worker", nodes, cfg)
		}()
	}
	wg.Wait()

	state.mu.Lock()
	count := state.counters["worker"]
	state.mu.Unlock()

	if count != 100 {
		t.Errorf("concurrent increments: got %d, want 100", count)
	}
}

func TestObserverDigest_Integration(t *testing.T) {
	t.Run("loop prevention", func(t *testing.T) {
		cfg := &Config{
			DigestTemplate: "Digest: {sender}",
			TmuxTimeout:    1.0,
			Nodes: map[string]NodeConfig{
				"observer-1": {SubscribeDigest: true},
			},
		}
		nodes := map[string]NodeInfo{"observer-1": {PaneID: "%999", SessionName: "test", SessionDir: "/tmp/test"}}
		digestedFiles := make(map[string]bool)

		// Observer message should be skipped
		sendObserverDigest("test.md", "observer-1", nodes, cfg, digestedFiles)

		// File should NOT be digested (loop prevention)
		if digestedFiles["test.md"] {
			t.Error("observer message was digested (should be skipped)")
		}
	})

	t.Run("duplicate prevention", func(t *testing.T) {
		cfg := &Config{
			DigestTemplate: "Digest: {sender}",
			TmuxTimeout:    1.0,
			Nodes: map[string]NodeConfig{
				"observer-1": {SubscribeDigest: true},
			},
		}
		nodes := map[string]NodeInfo{"observer-1": {PaneID: "%999", SessionName: "test", SessionDir: "/tmp/test"}}
		digestedFiles := make(map[string]bool)

		// First call should mark as digested
		sendObserverDigest("test1.md", "worker", nodes, cfg, digestedFiles)
		if !digestedFiles["test1.md"] {
			t.Error("file not marked as digested")
		}

		// Second call with same file should be skipped
		initialLen := len(digestedFiles)
		sendObserverDigest("test1.md", "worker", nodes, cfg, digestedFiles)
		// digestedFiles should not grow (duplicate was skipped)
		if len(digestedFiles) != initialLen {
			t.Error("digestedFiles map grew (duplicate was not properly skipped)")
		}
	})
}

func TestPING_Flow(t *testing.T) {
	sessionDir := t.TempDir()
	if err := createSessionDirs(sessionDir); err != nil {
		t.Fatalf("createSessionDirs failed: %v", err)
	}

	cfg := &Config{
		PingTemplate: "PING {node} in context {context_id}",
		TmuxTimeout:  1.0,
	}
	contextID := "test-context"

	t.Run("send PING to node", func(t *testing.T) {
		nodeInfo := NodeInfo{
			PaneID:      "%999",
			SessionName: "test",
			SessionDir:  sessionDir,
		}
		if err := sendPingToNode(nodeInfo, contextID, "worker", cfg.PingTemplate, cfg); err != nil {
			t.Fatalf("sendPingToNode failed: %v", err)
		}

		// Verify PING file created in post/
		postDir := filepath.Join(sessionDir, "post")
		entries, err := os.ReadDir(postDir)
		if err != nil {
			t.Fatalf("ReadDir failed: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 file in post/, got %d", len(entries))
		}

		// Verify filename format
		filename := entries[0].Name()
		if !strings.HasSuffix(filename, "-from-postman-to-worker.md") {
			t.Errorf("unexpected filename: %q", filename)
		}

		// Verify content
		content, err := os.ReadFile(filepath.Join(postDir, filename))
		if err != nil {
			t.Fatalf("ReadFile failed: %v", err)
		}
		expected := "PING worker in context test-context"
		if string(content) != expected {
			t.Errorf("content = %q, want %q", string(content), expected)
		}
	})
}

func TestMessageDelivery_EndToEnd(t *testing.T) {
	sessionDir := t.TempDir()
	if err := createSessionDirs(sessionDir); err != nil {
		t.Fatalf("createSessionDirs failed: %v", err)
	}

	// Create message in post/
	filename := "20260201-060000-from-orchestrator-to-worker.md"
	postPath := filepath.Join(sessionDir, "post", filename)
	if err := os.WriteFile(postPath, []byte("test message"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	nodes := map[string]NodeInfo{"worker": {PaneID: "%1", SessionName: "test", SessionDir: sessionDir}}
	adjacency := map[string][]string{
		"orchestrator": {"worker"},
		"worker":       {"orchestrator"},
	}

	// Deliver message
	if err := deliverMessage(sessionDir, filename, nodes, adjacency); err != nil {
		t.Fatalf("deliverMessage failed: %v", err)
	}

	// Verify message moved to inbox/worker/
	inboxPath := filepath.Join(sessionDir, "inbox", "worker", filename)
	if _, err := os.Stat(inboxPath); err != nil {
		t.Errorf("message not in inbox/worker/: %v", err)
	}

	// Verify removed from post/
	if _, err := os.Stat(postPath); !os.IsNotExist(err) {
		t.Error("message still in post/ after delivery")
	}

	// Simulate read by moving to read/
	readPath := filepath.Join(sessionDir, "read", filename)
	if err := os.Rename(inboxPath, readPath); err != nil {
		t.Fatalf("moving to read/ failed: %v", err)
	}

	// Verify in read/
	if _, err := os.Stat(readPath); err != nil {
		t.Errorf("message not in read/: %v", err)
	}
}

func TestSendPingToAll(t *testing.T) {
	sessionDir := t.TempDir()
	if err := createSessionDirs(sessionDir); err != nil {
		t.Fatalf("createSessionDirs failed: %v", err)
	}

	cfg := &Config{
		PingTemplate: "PING {node}",
		TmuxTimeout:  1.0,
	}
	contextID := "test-context"

	// sendPingToAll internally calls DiscoverNodes
	// Since we can't easily mock DiscoverNodes, we just verify it doesn't crash
	sendPingToAll(sessionDir, contextID, cfg)

	// Verify no panic occurred (basic smoke test)
	// In real environment, this would send PING to discovered nodes
}

func TestRunCreateDraft(t *testing.T) {
	sessionDir := t.TempDir()
	contextID := "test-context-123"

	// Create config with base_dir
	configPath := filepath.Join(t.TempDir(), "config.toml")
	configContent := "base_dir = \"" + sessionDir + "\"\n"
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Create draft directory
	draftDir := filepath.Join(sessionDir, contextID, "draft")
	if err := os.MkdirAll(draftDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}

	args := []string{
		"--to", "worker",
		"--from", "orchestrator",
		"--context-id", contextID,
		"--config", configPath,
	}

	if err := runCreateDraft(args); err != nil {
		t.Fatalf("runCreateDraft failed: %v", err)
	}

	// Verify draft file created
	entries, err := os.ReadDir(draftDir)
	if err != nil {
		t.Fatalf("ReadDir failed: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 draft file, got %d", len(entries))
	}

	// Verify filename format
	filename := entries[0].Name()
	if !strings.HasSuffix(filename, "-from-orchestrator-to-worker.md") {
		t.Errorf("unexpected filename: %q", filename)
	}

	// Verify content structure
	content, err := os.ReadFile(filepath.Join(draftDir, filename))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !strings.Contains(string(content), "method: message/send") {
		t.Error("draft content missing 'method: message/send'")
	}
	if !strings.Contains(string(content), "contextId: "+contextID) {
		t.Error("draft content missing contextId")
	}
}
