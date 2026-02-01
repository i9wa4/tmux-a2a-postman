package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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
