package cli

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGetSessionHealth(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string
	}{
		{
			name:       "valid session name no error on flag parse",
			args:       []string{"--session", "my-session"},
			wantErrSub: "",
		},
		{
			name:       "path traversal rejected",
			args:       []string{"--session", "../bad"},
			wantErrSub: "invalid value",
		},
		{
			name:       "underscore session name accepted",
			args:       []string{"--session", "bad_name"},
			wantErrSub: "",
		},
		{
			name:       "dot session name rejected",
			args:       []string{"--session", "."},
			wantErrSub: "invalid value",
		},
		{
			name:       "double dot",
			args:       []string{"--session", ".."},
			wantErrSub: "invalid value",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("POSTMAN_HOME", tmpDir)
			err := RunGetSessionHealth(tc.args)
			if tc.wantErrSub == "" {
				if err != nil && (strings.Contains(err.Error(), "flag provided but not defined") ||
					strings.Contains(err.Error(), "invalid value")) {
					t.Errorf("unexpected validation error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
			}
			if !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Errorf("error = %q; want to contain %q", err.Error(), tc.wantErrSub)
			}
		})
	}
}

func TestRunGetSessionHealth_UsesTMUXSessionWhenSessionFlagMissing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	t.Setenv("TMUX_PANE", "%77")

	scriptDir := t.TempDir()
	scriptPath := scriptDir + string(os.PathSeparator) + "tmux"
	script := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"  *\"#{session_name}\"*) printf '%s\\n' \"tmux-session\" ;;\n" +
		"  *) exit 1 ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile fake tmux: %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	err := RunGetSessionHealth(nil)
	if err != nil && (strings.Contains(err.Error(), "flag provided but not defined") ||
		strings.Contains(err.Error(), "session name required")) {
		t.Fatalf("RunGetSessionHealth should use tmux session fallback, got: %v", err)
	}
}

func TestRunGetSessionHealth_IncludesVisibleStateAndTopology(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260404-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker -- critic\"]\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
		filepath.Join(sessionDir, "waiting"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
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

	if err := os.WriteFile(
		filepath.Join(sessionDir, "inbox", "worker", "20260404-000000-s0000-from-boss-to-worker.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(worker inbox): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(sessionDir, "waiting", "20260404-000001-s0000-from-orchestrator-to-critic.md"),
		[]byte("---\nstate: composing\nexpects_reply: true\n---\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(critic waiting): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + "\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	runErr := RunGetSessionHealth([]string{
		"--config", configPath,
		"--context-id", contextID,
		"--session", sessionName,
	})
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	if runErr != nil {
		t.Fatalf("RunGetSessionHealth: %v", runErr)
	}

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", string(out), err)
	}

	if got := payload["visible_state"]; got != "composing" {
		t.Fatalf("visible_state = %#v, want %q", got, "composing")
	}

	windows, ok := payload["windows"].([]any)
	if !ok || len(windows) != 1 {
		t.Fatalf("windows = %#v, want single window entry", payload["windows"])
	}

	nodes, ok := payload["nodes"].([]any)
	if !ok || len(nodes) != 2 {
		t.Fatalf("nodes = %#v, want 2 nodes", payload["nodes"])
	}

	nodeByName := make(map[string]map[string]any)
	for _, raw := range nodes {
		node, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("node entry = %#v, want object", raw)
		}
		name, _ := node["name"].(string)
		nodeByName[name] = node
	}

	if got := nodeByName["worker"]["visible_state"]; got != "pending" {
		t.Fatalf("worker visible_state = %#v, want %q", got, "pending")
	}
	if got := nodeByName["critic"]["visible_state"]; got != "composing" {
		t.Fatalf("critic visible_state = %#v, want %q", got, "composing")
	}
}

func TestRunGetSessionHealth_UsesConfigEdgeOrderForNodesAndTMUXOrderForWindows(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260406-ctx"
	sessionName := "review"
	sessionDir := filepath.Join(tmpDir, contextID, sessionName)

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"worker -- critic\"]\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(sessionDir, "inbox", "worker"),
		filepath.Join(sessionDir, "inbox", "critic"),
		filepath.Join(sessionDir, "waiting"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\t" + sessionName + "\tworker' '%12\t" + contextID + "\t" + sessionName + "\tcritic'\n" +
		"    ;;\n" +
		"  \"list-panes -t " + sessionName + "\")\n" +
		"    printf '%s\\n' '0\t0\t%12\tcritic\tclaude' '1\t0\t%11\tworker\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = oldStdout
	}()

	runErr := RunGetSessionHealth([]string{
		"--config", configPath,
		"--context-id", contextID,
		"--session", sessionName,
	})
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close: %v", err)
	}
	if runErr != nil {
		t.Fatalf("RunGetSessionHealth: %v", runErr)
	}

	out, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll(stdout): %v", err)
	}

	var payload struct {
		Nodes []struct {
			Name string `json:"name"`
		} `json:"nodes"`
		Windows []struct {
			Index string `json:"index"`
			Nodes []struct {
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", string(out), err)
	}

	if len(payload.Nodes) != 2 {
		t.Fatalf("nodes = %#v, want 2 nodes", payload.Nodes)
	}
	if payload.Nodes[0].Name != "worker" || payload.Nodes[1].Name != "critic" {
		t.Fatalf("nodes order = %#v, want worker then critic", payload.Nodes)
	}

	if len(payload.Windows) != 2 {
		t.Fatalf("windows = %#v, want 2 windows", payload.Windows)
	}
	if payload.Windows[0].Index != "0" || len(payload.Windows[0].Nodes) != 1 || payload.Windows[0].Nodes[0].Name != "critic" {
		t.Fatalf("first window = %#v, want window 0 with critic", payload.Windows[0])
	}
	if payload.Windows[1].Index != "1" || len(payload.Windows[1].Nodes) != 1 || payload.Windows[1].Nodes[0].Name != "worker" {
		t.Fatalf("second window = %#v, want window 1 with worker", payload.Windows[1])
	}
}
