package cli

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/projection"
)

func installFakeTmuxForCLI(t *testing.T, postmanHome, sessionName, paneTitle string) {
	t.Helper()
	t.Setenv("POSTMAN_HOME", postmanHome)
	t.Setenv("TMUX_PANE", "%99")
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"#{session_name}\"*) printf '%s\\n' \"" + sessionName + "\" ;;\n" +
		"  *\"#{pane_title}\"*) printf '%s\\n' \"" + paneTitle + "\" ;;\n" +
		"  *\"#{pane_id}\"*) printf '%s\\n' \"%99\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake tmux: %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
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

func awaitCompatibilitySubmitRequest(t *testing.T, sessionDir string, timeout time.Duration) (string, projection.CompatibilitySubmitRequest) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	requestsDir := projection.CompatibilitySubmitRequestsDir(sessionDir)
	for {
		entries, err := os.ReadDir(requestsDir)
		if err == nil {
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
					continue
				}
				requestPath := filepath.Join(requestsDir, entry.Name())
				request, readErr := projection.ReadCompatibilitySubmitRequest(requestPath)
				if readErr != nil {
					t.Fatalf("ReadCompatibilitySubmitRequest(%s): %v", requestPath, readErr)
				}
				return requestPath, request
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for compatibility submit request in %s", requestsDir)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
