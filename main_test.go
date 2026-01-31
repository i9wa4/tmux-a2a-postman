package main

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestSessionLock_Acquire(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "postman.lock")

	// Normal acquire
	lock1, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}

	// Double-acquire should fail
	_, err = NewSessionLock(lockPath)
	if err == nil {
		t.Fatal("expected error on double acquire, got nil")
	}

	_ = lock1.Release()
}

func TestSessionLock_Release(t *testing.T) {
	tmpDir := t.TempDir()
	lockPath := filepath.Join(tmpDir, "postman.lock")

	lock1, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("first lock acquire failed: %v", err)
	}

	if err := lock1.Release(); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// Re-acquire after release should succeed
	lock2, err := NewSessionLock(lockPath)
	if err != nil {
		t.Fatalf("re-acquire after release failed: %v", err)
	}
	_ = lock2.Release()
}

func TestResolveBaseDir(t *testing.T) {
	// Test default
	origVal := os.Getenv("POSTMAN_HOME")
	defer func() {
		if origVal != "" {
			_ = os.Setenv("POSTMAN_HOME", origVal)
		} else {
			_ = os.Unsetenv("POSTMAN_HOME")
		}
	}()

	_ = os.Unsetenv("POSTMAN_HOME")
	if got := resolveBaseDir(); got != ".postman" {
		t.Errorf("default: got %q, want %q", got, ".postman")
	}

	// Test POSTMAN_HOME priority
	_ = os.Setenv("POSTMAN_HOME", "/tmp/custom-postman")
	if got := resolveBaseDir(); got != "/tmp/custom-postman" {
		t.Errorf("POSTMAN_HOME: got %q, want %q", got, "/tmp/custom-postman")
	}
}

func TestCreateSessionDirs(t *testing.T) {
	tmpDir := t.TempDir()
	sessionDir := filepath.Join(tmpDir, "test-session")

	if err := createSessionDirs(sessionDir); err != nil {
		t.Fatalf("createSessionDirs failed: %v", err)
	}

	expectedDirs := []string{"inbox", "post", "draft", "read", "dead-letter"}
	for _, d := range expectedDirs {
		path := filepath.Join(sessionDir, d)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("directory %q not created: %v", d, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q is not a directory", d)
		}
	}
}

func TestParseMessageFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		wantTS   string
		wantFrom string
		wantTo   string
	}{
		{
			name:     "normal",
			filename: "20260201-022121-from-orchestrator-to-worker.md",
			wantTS:   "20260201-022121",
			wantFrom: "orchestrator",
			wantTo:   "worker",
		},
		{
			name:     "short timestamp",
			filename: "12345-from-a-to-b.md",
			wantTS:   "12345",
			wantFrom: "a",
			wantTo:   "b",
		},
		{
			name:     "hyphenated names",
			filename: "20260201-022121-from-node-alpha-to-node-beta.md",
			wantTS:   "20260201-022121",
			wantFrom: "node-alpha",
			wantTo:   "node-beta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseMessageFilename(tt.filename)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if info.Timestamp != tt.wantTS {
				t.Errorf("Timestamp: got %q, want %q", info.Timestamp, tt.wantTS)
			}
			if info.From != tt.wantFrom {
				t.Errorf("From: got %q, want %q", info.From, tt.wantFrom)
			}
			if info.To != tt.wantTo {
				t.Errorf("To: got %q, want %q", info.To, tt.wantTo)
			}
		})
	}
}

func TestParseMessageFilename_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{"no extension", "20260201-from-a-to-b"},
		{"wrong extension", "20260201-from-a-to-b.txt"},
		{"missing from marker", "20260201-to-b.md"},
		{"missing to marker", "20260201-from-a.md"},
		{"empty from", "20260201-from--to-b.md"},
		{"empty to", "20260201-from-a-to-.md"},
		{"empty timestamp", "-from-a-to-b.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseMessageFilename(tt.filename)
			if err == nil {
				t.Errorf("expected error for %q, got nil", tt.filename)
			}
		})
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
