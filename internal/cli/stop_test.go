package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestRunStop_NoActiveDaemonPrintsMessage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("POSTMAN_HOME", tmpDir)

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("Chdir restore: %v", err)
		}
	})
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Chdir tmpDir: %v", err)
	}

	var stdout bytes.Buffer
	if err := RunStop(&stdout, []string{"--session", "review-session"}); err != nil {
		t.Fatalf("RunStop: %v", err)
	}

	var payload stopOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.Status != "not_running" {
		t.Fatalf("payload.Status = %q, want not_running", payload.Status)
	}
	if payload.Session != "review-session" {
		t.Fatalf("payload.Session = %q, want review-session", payload.Session)
	}
}
