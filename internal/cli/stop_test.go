package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRunStop_NoActiveDaemonPrintsMessage(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	installFakeTmuxForCLI(t, tmpDir, "review-session", "worker")

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
	if err := RunStop(&stdout, nil); err != nil {
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

func TestRunStop_StopsDaemonOwningManagedSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	installFakeTmuxForStop(t, tmpDir, "managed-session", map[string]string{
		"managed-session": "ctx-owner:12345",
	})

	if err := os.MkdirAll(filepath.Join(tmpDir, "ctx-owner", "managed-session"), 0o755); err != nil {
		t.Fatalf("MkdirAll managed session: %v", err)
	}
	pidDir := filepath.Join(tmpDir, "ctx-owner", "daemon-session")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("MkdirAll daemon session: %v", err)
	}

	child := exec.Command("sleep", "60")
	if err := child.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- child.Wait()
	}()
	t.Cleanup(func() {
		if child.ProcessState == nil || !child.ProcessState.Exited() {
			_ = child.Process.Kill()
		}
		select {
		case <-waitCh:
		case <-time.After(2 * time.Second):
		}
	})

	if err := os.WriteFile(filepath.Join(pidDir, "postman.pid"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	var stdout bytes.Buffer
	if err := RunStop(&stdout, nil); err != nil {
		t.Fatalf("RunStop: %v", err)
	}

	var payload stopOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.Status != "stopped" {
		t.Fatalf("payload.Status = %q, want stopped; full payload: %s", payload.Status, stdout.String())
	}
	if payload.Session != "managed-session" {
		t.Fatalf("payload.Session = %q, want managed-session", payload.Session)
	}
	if payload.ContextID != "ctx-owner" {
		t.Fatalf("payload.ContextID = %q, want ctx-owner", payload.ContextID)
	}
	if payload.PID != child.Process.Pid {
		t.Fatalf("payload.PID = %d, want %d", payload.PID, child.Process.Pid)
	}
}

func installFakeTmuxForStop(t *testing.T, postmanHome, sessionName string, owners map[string]string) {
	t.Helper()
	t.Setenv("POSTMAN_HOME", postmanHome)
	t.Setenv("TMUX_PANE", "%99")
	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"#{session_name}\"*) printf '%s\\n' \"" + sessionName + "\" ;;\n" +
		"  \"show-options -gqv @a2a_session_on_managed-session\") printf '%s\\n' '" + owners["managed-session"] + "' ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake tmux: %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
