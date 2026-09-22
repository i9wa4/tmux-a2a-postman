package cli

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
	"github.com/i9wa4/tmux-a2a-postman/internal/tmuxtest"
)

func TestMain(m *testing.M) {
	restoreDurableWrites := journal.SetDurableWritesForTesting(false)
	configHome, err := os.MkdirTemp("", "tmux-a2a-postman-cli-test-config-*")
	if err != nil {
		panic(err)
	}
	home, err := os.MkdirTemp("", "tmux-a2a-postman-cli-test-home-*")
	if err != nil {
		panic(err)
	}
	if err := os.Setenv("XDG_CONFIG_HOME", configHome); err != nil {
		panic(err)
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	restoreDurableWrites()
	_ = os.RemoveAll(configHome)
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func installFakeTmuxForCLI(t *testing.T, postmanHome, sessionName, paneTitle string) {
	t.Helper()
	t.Setenv("POSTMAN_HOME", postmanHome)
	t.Setenv("TMUX_PANE", "%99")
	tmuxtest.Install(t, tmuxtest.WithPane(tmuxtest.Pane{
		ID:          "%99",
		SessionName: sessionName,
		Title:       paneTitle,
	}))
}

func messageFixture(from, to, body string) string {
	return "---\nparams:\n  from: " + from + "\n  to: " + to + "\n  timestamp: 2026-03-28T10:15:00Z\n---\n\n" + body + "\n"
}

func captureCommandOutput(t *testing.T, fn func() error) (string, string, error) {
	t.Helper()
	origStdout := os.Stdout
	origStderr := os.Stderr
	origLogWriter := log.Writer()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe stdout: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe stderr: %v", err)
	}
	os.Stdout = stdoutW
	os.Stderr = stderrW
	log.SetOutput(stderrW)
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	runErr := fn()
	log.SetOutput(origLogWriter)

	if err := stdoutW.Close(); err != nil {
		t.Fatalf("Close stdout writer: %v", err)
	}
	if err := stderrW.Close(); err != nil {
		t.Fatalf("Close stderr writer: %v", err)
	}

	stdoutBytes, err := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("ReadAll stdout: %v", err)
	}
	stderrBytes, err := io.ReadAll(stderrR)
	if err != nil {
		t.Fatalf("ReadAll stderr: %v", err)
	}
	return string(stdoutBytes), string(stderrBytes), runErr
}

func TestCaptureCommandOutput_CapturesDefaultLogger(t *testing.T) {
	stdout, stderr, err := captureCommandOutput(t, func() error {
		log.Print("captured logger output")
		return nil
	})
	if err != nil {
		t.Fatalf("captureCommandOutput returned err: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "captured logger output") {
		t.Fatalf("stderr missing logger output: %q", stderr)
	}
}

func assertNoMarkdownFilesInTree(t *testing.T, root string) {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return
	}

	var found []string
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".md" {
			found = append(found, path)
		}
		return nil
	}); err != nil {
		t.Fatalf("Walk %s: %v", root, err)
	}
	if len(found) != 0 {
		t.Fatalf("expected no markdown files under %s, found %v", root, found)
	}
}

func writeMinimalNodeConfig(t *testing.T, dir string) string {
	t.Helper()

	configPath := filepath.Join(dir, "postman.toml")
	content := `[postman]

[messenger]
role = "messenger"

[worker]
role = "worker"
`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile config: %v", err)
	}
	return configPath
}

// awaitDaemonSubmitRequest polls for a daemon-submit request file and returns
// an error on timeout or read failure instead of calling t.Fatalf directly.
// Per the testing.T contract, FailNow/Fatalf must only be called from the
// goroutine running the test; callers that poll from a background goroutine
// (#758) must check the returned error and fail via t.Errorf on their own
// goroutine, then unblock any channel the test goroutine is waiting on.
func awaitDaemonSubmitRequest(t *testing.T, sessionDir string, timeout time.Duration) (string, projection.DaemonSubmitRequest, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	requestsDir := projection.DaemonSubmitRequestsDir(sessionDir)
	for {
		entries, err := os.ReadDir(requestsDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
					continue
				}
				requestPath := filepath.Join(requestsDir, entry.Name())
				request, readErr := projection.ReadDaemonSubmitRequest(requestPath)
				if readErr != nil {
					return "", projection.DaemonSubmitRequest{}, fmt.Errorf("ReadDaemonSubmitRequest(%s): %w", requestPath, readErr)
				}
				return requestPath, request, nil
			}
		}
		if time.Now().After(deadline) {
			return "", projection.DaemonSubmitRequest{}, fmt.Errorf("timed out waiting for daemon submit request in %s", requestsDir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// awaitMarkdownFile polls dir for a markdown file and returns an error on
// timeout instead of calling t.Fatalf directly. Per the testing.T contract,
// FailNow/Fatalf must only be called from the goroutine running the test;
// callers that poll from a background goroutine (#763) must check the
// returned error and fail via t.Errorf on their own goroutine, then unblock
// any channel the test goroutine is waiting on.
func awaitMarkdownFile(t *testing.T, dir string, timeout time.Duration) (string, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
					continue
				}
				return entry.Name(), nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out waiting for markdown file in %s", dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestAwaitMarkdownFile_ReturnsErrorInsteadOfHangingFromGoroutine pins #763:
// calling awaitMarkdownFile from a background goroutine and letting it time
// out must return an error the goroutine can report via t.Errorf, not call
// t.Fatalf itself and leave the test goroutine's receive blocked forever.
func TestAwaitMarkdownFile_ReturnsErrorInsteadOfHangingFromGoroutine(t *testing.T) {
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		_, err := awaitMarkdownFile(t, dir, 50*time.Millisecond)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("awaitMarkdownFile() error = nil, want timeout error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitMarkdownFile did not return within 2s; regressed to blocking forever (#763)")
	}
}

// TestAwaitDaemonSubmitRequest_ReturnsErrorInsteadOfHangingFromGoroutine pins
// #758: calling awaitDaemonSubmitRequest from a background goroutine and
// letting it time out must return an error the goroutine can report via
// t.Errorf, not call t.Fatalf itself and leave the test goroutine's receive
// blocked forever.
func TestAwaitDaemonSubmitRequest_ReturnsErrorInsteadOfHangingFromGoroutine(t *testing.T) {
	sessionDir := t.TempDir()
	if err := os.MkdirAll(projection.DaemonSubmitRequestsDir(sessionDir), 0o700); err != nil {
		t.Fatalf("MkdirAll requests dir: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := awaitDaemonSubmitRequest(t, sessionDir, 50*time.Millisecond)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("awaitDaemonSubmitRequest() error = nil, want timeout error")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitDaemonSubmitRequest did not return within 2s; regressed to blocking forever (#758)")
	}
}
