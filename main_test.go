package main

import (
	"os"
	"os/exec"
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
	nodes := map[string]string{"worker": "%999"}

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
	nodes := map[string]string{"worker": "%999"}

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
