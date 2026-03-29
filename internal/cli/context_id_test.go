package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRunGetContextID_JSONOutput(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	contextID := "ctx-context-id"
	sessionName := "review-session"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatalf("MkdirAll sessionDir: %v", err)
	}
	pidPath := filepath.Join(sessionDir, "postman.pid")
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	var stdout bytes.Buffer
	if err := RunGetContextID(&stdout, sessionName, "", true); err != nil {
		t.Fatalf("RunGetContextID: %v", err)
	}

	want := "{\"context_id\":\"ctx-context-id\"}\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}
