package cli

import (
	"bytes"
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

	want := "postman: no daemon running\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}
