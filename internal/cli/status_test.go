package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func TestRunStatus_JSONOutput_NoLiveContext(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	var stdout bytes.Buffer
	if err := RunStatus(&stdout, []string{"--json"}); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}

	var payload status.AllSessionHealth
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.ContextID != "" {
		t.Fatalf("ContextID = %q, want empty", payload.ContextID)
	}
	if payload.Sessions == nil {
		t.Fatal("Sessions = nil, want empty slice")
	}
	if len(payload.Sessions) != 0 {
		t.Fatalf("Sessions length = %d, want 0", len(payload.Sessions))
	}
}

func TestRunStatus_HumanOutput_NoLiveContext(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	var stdout bytes.Buffer
	if err := RunStatus(&stdout, nil); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}

	if stdout.String() != "No active sessions.\n" {
		t.Fatalf("stdout = %q, want empty-session message", stdout.String())
	}
}

func TestRunStatus_ParamsCanSelectJSONButCannotRedirectSession(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	t.Run("json", func(t *testing.T) {
		var stdout bytes.Buffer
		if err := RunStatus(&stdout, []string{"--params", `{"json":true}`}); err != nil {
			t.Fatalf("RunStatus: %v", err)
		}
		if stdout.String() != "{\"schema_version\":1,\"context_id\":\"\",\"sessions\":[]}\n" {
			t.Fatalf("stdout = %q, want empty-session JSON", stdout.String())
		}
	})

	t.Run("session rejected", func(t *testing.T) {
		var stdout bytes.Buffer
		err := RunStatus(&stdout, []string{"--params", "session=other"})
		if err == nil {
			t.Fatal("RunStatus returned nil error for excluded --params session")
		}
		if !strings.Contains(err.Error(), `field "session" is not settable via --params`) {
			t.Fatalf("error = %q, want excluded session error", err.Error())
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})
}

func TestRunStatus_LiveRuntimeMatchesCanonicalAllSessionHealth(t *testing.T) {
	tmpDir, contextID, configPath := installStatusLiveFixture(t)
	t.Setenv("POSTMAN_HOME", tmpDir)

	var jsonStdout bytes.Buffer
	if err := RunStatus(&jsonStdout, []string{
		"--config", configPath,
		"--context-id", contextID,
		"--json",
	}); err != nil {
		t.Fatalf("RunStatus(--json): %v", err)
	}

	var payload status.AllSessionHealth
	if err := json.Unmarshal(jsonStdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", jsonStdout.String(), err)
	}
	if payload.ContextID != contextID {
		t.Fatalf("ContextID = %q, want %q", payload.ContextID, contextID)
	}
	if payload.SchemaVersion != status.SchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", payload.SchemaVersion, status.SchemaVersion)
	}
	if len(payload.Sessions) != 1 {
		t.Fatalf("Sessions length = %d, want 1: %#v", len(payload.Sessions), payload.Sessions)
	}
	if payload.Sessions[0].SessionName != "main" {
		t.Fatalf("SessionName = %q, want main", payload.Sessions[0].SessionName)
	}
	if payload.Sessions[0].Compact == "" {
		t.Fatalf("Compact = empty in payload %#v", payload.Sessions[0])
	}
	if payload.Sessions[0].Queues.InboxCount != 1 {
		t.Fatalf("InboxCount = %d, want 1", payload.Sessions[0].Queues.InboxCount)
	}
	if payload.Sessions[0].Queues.WaitingCount != 0 {
		t.Fatalf("WaitingCount = %d, want 0", payload.Sessions[0].Queues.WaitingCount)
	}
	if payload.Sessions[0].InputLocks == nil {
		t.Fatal("InputLocks = nil, want empty slice")
	}

	wantHuman := formatAllSessionHealthOneline(payload) + "\n"
	var humanStdout bytes.Buffer
	if err := RunStatus(&humanStdout, []string{
		"--config", configPath,
		"--context-id", contextID,
	}); err != nil {
		t.Fatalf("RunStatus: %v", err)
	}
	if humanStdout.String() != wantHuman {
		t.Fatalf("human status = %q, want %q from canonical payload", humanStdout.String(), wantHuman)
	}
}

func installStatusLiveFixture(t *testing.T) (string, string, string) {
	t.Helper()

	tmpDir := t.TempDir()
	contextID := "20260404-ctx"
	sessionDir := filepath.Join(tmpDir, contextID, "main")
	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger -- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "messenger"),
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "waiting"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(sessionDir, "inbox", "worker", "20260404-000000-s0000-from-boss-to-worker.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(worker inbox): %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%12": {"status":"idle","lastChangeAt":"2026-04-04T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-sessions -F \"*)\n" +
		"    printf '%s\\n' 'main\t$173'\n" +
		"    ;;\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\tmain\tworker' '%12\t" + contextID + "\tmain\tmessenger'\n" +
		"    ;;\n" +
		"  \"list-windows -t main\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:0\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tmessenger\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return tmpDir, contextID, configPath
}
