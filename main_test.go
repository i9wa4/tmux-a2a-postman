package main

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/cliutil"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

// --- Regression tests for deleted commands (M2/M7) ---

// TestRunRead_DeadLettersFlag verifies runRead(--dead-letters) does not error
// when the dead-letter dir is absent (empty inbox scenario).
func TestRunRead_DeadLettersFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	// Provide a minimal tmux session name via env so GetTmuxSessionName returns something.
	t.Setenv("TMUX", "/tmp/tmux-test,1,0")
	err := runRead([]string{"--dead-letters"})
	// runRead may fail because we are not inside tmux (session name empty).
	// The test verifies only that no "flag provided but not defined" error occurs.
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

// TestRunRead_ArchivedFlag verifies runRead(--archived) exits gracefully with empty output.
func TestRunRead_ArchivedFlag(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runRead([]string{"--archived"})
	// May fail due to missing tmux env, but must not panic or return flag errors.
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

// TestRunPop_ContextIDFlagAccepted mirrors the deleted TestRunNext_ContextIDFlagAccepted.
// Verifies that --context-id is a recognized flag for runPop.
func TestRunPop_ContextIDFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runPop([]string{"--context-id", "test-ctx-123"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--context-id not defined in runPop: %v", err)
	}
}

func TestRunPop_RequeuedMessagePreservesOriginalPayload(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForPop(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-requeued"
	messageFile := "20260328-101503-from-orchestrator-to-worker.md"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	readDir := filepath.Join(tmpDir, contextID, "test-session", "read")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}

	content := messageFixture("orchestrator", "worker", "Requeued original payload")
	inboxPath := filepath.Join(inboxDir, messageFile)
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("runPop: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "Requeued original payload") {
		t.Fatalf("stdout %q does not contain original payload", stdout)
	}
	readPath := filepath.Join(readDir, messageFile)
	archived, err := os.ReadFile(readPath)
	if err != nil {
		t.Fatalf("ReadFile read: %v", err)
	}
	if string(archived) != content {
		t.Fatalf("archived content changed:\n got %q\nwant %q", archived, content)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("inbox file still present or wrong error: %v", err)
	}
}

func TestRunPop_RestoredArchivedMessagePreservesCanonicalReadTracking(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForPop(t, tmpDir, "test-session", "worker")
	contextID := "ctx-pop-restored"
	messageFile := "20260328-101504-from-orchestrator-to-worker.md"
	inboxDir := filepath.Join(tmpDir, contextID, "test-session", "inbox", "worker")
	readDir := filepath.Join(tmpDir, contextID, "test-session", "read")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll inbox: %v", err)
	}
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll read: %v", err)
	}

	content := messageFixture("orchestrator", "worker", "Archived original payload")
	readPath := filepath.Join(readDir, messageFile)
	inboxPath := filepath.Join(inboxDir, messageFile)
	if err := os.WriteFile(readPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile read: %v", err)
	}
	canonicalReadTime := time.Date(2026, time.March, 28, 10, 15, 4, 0, time.UTC)
	if err := os.Chtimes(readPath, canonicalReadTime, canonicalReadTime); err != nil {
		t.Fatalf("Chtimes read: %v", err)
	}
	if err := os.WriteFile(inboxPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile inbox: %v", err)
	}
	restoredCopyTime := canonicalReadTime.Add(2 * time.Minute)
	if err := os.Chtimes(inboxPath, restoredCopyTime, restoredCopyTime); err != nil {
		t.Fatalf("Chtimes inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runPop([]string{"--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("runPop: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, "Archived original payload") {
		t.Fatalf("stdout %q does not contain archived payload", stdout)
	}
	if _, err := os.Stat(inboxPath); !os.IsNotExist(err) {
		t.Fatalf("restored inbox copy still present or wrong error: %v", err)
	}
	readInfo, err := os.Stat(readPath)
	if err != nil {
		t.Fatalf("Stat read: %v", err)
	}
	if !readInfo.ModTime().Equal(canonicalReadTime) {
		t.Fatalf("canonical read modtime changed: got %s want %s", readInfo.ModTime().UTC().Format(time.RFC3339), canonicalReadTime.Format(time.RFC3339))
	}
	archived, err := os.ReadFile(readPath)
	if err != nil {
		t.Fatalf("ReadFile read: %v", err)
	}
	if string(archived) != content {
		t.Fatalf("read content changed:\n got %q\nwant %q", archived, content)
	}
}

func TestRunPop_FileReadsAcrossContextsNonDestructively(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForPop(t, tmpDir, "test-session", "worker")

	currentContextID := "ctx-pop-current"
	otherContextID := "ctx-pop-other"
	filename := "20260330-101505-from-orchestrator-to-worker.md"
	content := messageFixture("orchestrator", "worker", "Cross-context payload")

	currentInboxDir := filepath.Join(tmpDir, currentContextID, "test-session", "inbox", "worker")
	otherInboxDir := filepath.Join(tmpDir, otherContextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(currentInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll current inbox: %v", err)
	}
	if err := os.MkdirAll(otherInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll other inbox: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmpDir, currentContextID, "test-session", "postman.pid"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatalf("WriteFile postman.pid: %v", err)
	}

	targetPath := filepath.Join(otherInboxDir, filename)
	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile target inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runPop([]string{"--file", filename})
	})
	if err != nil {
		t.Fatalf("runPop --file: %v\nstderr=%s", err, stderr)
	}
	if stdout != content {
		t.Fatalf("stdout changed payload:\n got %q\nwant %q", stdout, content)
	}

	remaining, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target inbox: %v", err)
	}
	if string(remaining) != content {
		t.Fatalf("target inbox content changed:\n got %q\nwant %q", remaining, content)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, otherContextID, "test-session", "read", filename)); !os.IsNotExist(err) {
		t.Fatalf("unexpected archived copy or wrong error: %v", err)
	}
}

func TestRunPop_FileHonorsExplicitContextID(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForPop(t, tmpDir, "test-session", "worker")

	currentContextID := "ctx-pop-bound"
	otherContextID := "ctx-pop-leak"
	filename := "20260330-101506-from-orchestrator-to-worker.md"
	content := messageFixture("orchestrator", "worker", "Leaked payload")

	currentInboxDir := filepath.Join(tmpDir, currentContextID, "test-session", "inbox", "worker")
	otherInboxDir := filepath.Join(tmpDir, otherContextID, "test-session", "inbox", "worker")
	if err := os.MkdirAll(currentInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll current inbox: %v", err)
	}
	if err := os.MkdirAll(otherInboxDir, 0o700); err != nil {
		t.Fatalf("MkdirAll other inbox: %v", err)
	}

	targetPath := filepath.Join(otherInboxDir, filename)
	if err := os.WriteFile(targetPath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile target inbox: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runPop([]string{"--context-id", currentContextID, "--file", filename})
	})
	if err == nil {
		t.Fatal("runPop --context-id --file unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "not found in any inbox/ directory") {
		t.Fatalf("unexpected error: %v", err)
	}
	if stdout != "" {
		t.Fatalf("stdout leaked payload: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr unexpectedly wrote output: %q", stderr)
	}

	remaining, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatalf("ReadFile target inbox: %v", err)
	}
	if string(remaining) != content {
		t.Fatalf("target inbox content changed:\n got %q\nwant %q", remaining, content)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, otherContextID, "test-session", "read", filename)); !os.IsNotExist(err) {
		t.Fatalf("unexpected archived copy or wrong error: %v", err)
	}
}

// TestRunSendMessage_BasicFlagAccepted verifies basic flag parsing for runSendMessage
// does not panic and does not return a "flag provided but not defined" error.
func TestRunSendMessage_BasicFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendMessage([]string{"--to", "worker", "--body", "hello"})
	// Will fail at sender auto-detection (no tmux), but must not be a flag error.
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("unexpected flag-parse error: %v", err)
	}
}

func installFakeTmuxForPop(t *testing.T, postmanHome, sessionName, paneTitle string) {
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
	defer func() {
		os.Stdout = origStdout
		os.Stderr = origStderr
	}()

	runErr := fn()

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
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
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

// TestRunSendMessage_FromFlagAccepted verifies --from is a recognized flag.
// --from requires --bindings, so the test expects an error about --bindings, not a flag error.
func TestRunSendMessage_FromFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendMessage([]string{"--to", "worker", "--body", "hello", "--from", "orchestrator"})
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--from not defined in runSendMessage: %v", err)
	}
	// Must error with --bindings requirement, not a flag parse error.
	if err == nil {
		t.Fatalf("expected error (--bindings required with --from), got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "bindings") {
		t.Errorf("expected error mentioning 'bindings', got: %v", err)
	}
}

// TestRunSendMessage_InvalidFromNodeName verifies that an invalid --from value
// (one that fails ValidateNodeName) returns an appropriate error.
func TestRunSendMessage_InvalidFromNodeName(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	// Write a minimal bindings file so --bindings check passes.
	bindingsFile := filepath.Join(tmpDir, "bindings.json")
	if err := os.WriteFile(bindingsFile, []byte(`[]`), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	err := runSendMessage([]string{
		"--to", "worker", "--body", "hello",
		"--from", "../escape", "--bindings", bindingsFile,
	})
	if err == nil {
		t.Fatal("expected error for invalid --from node name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") && !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("expected 'invalid node name' or 'invalid value', got: %v", err)
	}
}

func TestRunSendMessage_InvalidToNodeName(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForPop(t, tmpDir, "test-session", "messenger")

	err := runSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-to",
		"--to", "worker_alt",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected invalid --to node name error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-to", "test-session"))
}

func TestRunSendMessage_InvalidAutoDetectedPaneTitle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForPop(t, tmpDir, "test-session", "messenger_alt")

	err := runSendMessage([]string{
		"--config", configPath,
		"--context-id", "ctx-send-invalid-pane",
		"--to", "worker",
		"--body", "hello",
	})
	if err == nil {
		t.Fatal("expected invalid auto-detected pane title error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}

	assertNoMarkdownFilesInTree(t, filepath.Join(tmpDir, "ctx-send-invalid-pane", "test-session"))
}

func TestResolveInboxPath_InvalidAutoDetectedPaneTitle(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := writeMinimalNodeConfig(t, tmpDir)
	installFakeTmuxForPop(t, tmpDir, "test-session", "worker_alt")

	_, err := cliutil.ResolveInboxPath([]string{
		"--config", configPath,
		"--context-id", "ctx-resolve-invalid-pane",
	})
	if err == nil {
		t.Fatal("expected invalid auto-detected pane title error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Fatalf("expected invalid node name error, got: %v", err)
	}
}

func TestRunRead_ArchivedSessionPrefixedRecipient(t *testing.T) {
	tmpDir := t.TempDir()
	installFakeTmuxForPop(t, tmpDir, "review-session", "worker")
	contextID := "ctx-read-archived-prefixed"
	readDir := filepath.Join(tmpDir, contextID, "review-session", "read")
	if err := os.MkdirAll(readDir, 0o700); err != nil {
		t.Fatalf("MkdirAll readDir: %v", err)
	}

	filename := "20260328-123500-from-orchestrator-to-review-session:worker.md"
	content := messageFixture("orchestrator", "review-session:worker", "Archived cross-session payload")
	if err := os.WriteFile(filepath.Join(readDir, filename), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile archived message: %v", err)
	}

	stdout, stderr, err := captureCommandOutput(t, func() error {
		return runRead([]string{"--archived", "--context-id", contextID})
	})
	if err != nil {
		t.Fatalf("runRead --archived: %v\nstderr=%s", err, stderr)
	}
	if !strings.Contains(stdout, filename) {
		t.Fatalf("archived listing missing session-prefixed recipient file: stdout=%q", stdout)
	}
}

// TestRunSendMessage_IdempotencyKeyFlagAccepted verifies --idempotency-key is recognized.
func TestRunSendMessage_IdempotencyKeyFlagAccepted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)
	err := runSendMessage([]string{"--to", "worker", "--body", "hello", "--idempotency-key", "key-abc-123"})
	// Will fail at sender auto-detection or context-id resolution, but must not be a flag error.
	if err != nil && strings.Contains(err.Error(), "flag provided but not defined") {
		t.Errorf("--idempotency-key not defined in runSendMessage: %v", err)
	}
}

// --- Issue #351: parseParams / parseShorthand unit tests ---

// TestParseParams verifies parseParams behavior across JSON, shorthand, and edge-case inputs.
func TestParseParams(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantKey string // if non-empty, result must contain this key
		wantVal string // expected value for wantKey
		wantErr string // if non-empty, error must contain this substring
		wantNil bool   // true if result map should be nil (empty/no-op)
	}{
		{
			name:    "json integer preserved",
			input:   `{"n":1000000}`,
			wantKey: "n",
			wantVal: "1000000",
		},
		{
			name:    "json float preserved",
			input:   `{"n":3.14}`,
			wantKey: "n",
			wantVal: "3.14",
		},
		{
			name:    "json null returns error",
			input:   `{"to":null}`,
			wantErr: "must be a scalar value, not null",
		},
		{
			name:    "json array returns error",
			input:   `{"to":["a","b"]}`,
			wantErr: "must be scalar",
		},
		{
			name:    "shorthand happy path",
			input:   "to=worker",
			wantKey: "to",
			wantVal: "worker",
		},
		{
			name:    "shorthand no-equals returns error with prefix",
			input:   "invalid-no-equals-no-brace",
			wantErr: "--params: invalid shorthand pair",
		},
		{
			name:    "shorthand no-equals returns error with separator hint",
			input:   "invalid-no-equals-no-brace",
			wantErr: "missing = separator",
		},
		{
			name:    "empty string is no-op",
			input:   "",
			wantNil: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := cliutil.ParseParams(tc.input)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %q; want to contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if result != nil {
					t.Errorf("result = %v; want nil", result)
				}
				return
			}
			if tc.wantKey != "" {
				got, ok := result[tc.wantKey]
				if !ok {
					t.Errorf("result missing key %q; got %v", tc.wantKey, result)
				} else if got != tc.wantVal {
					t.Errorf("result[%q] = %q; want %q", tc.wantKey, got, tc.wantVal)
				}
			}
		})
	}
}

func TestFilterToUINode(t *testing.T) {
	makeNodes := func(names ...string) map[string]discovery.NodeInfo {
		m := make(map[string]discovery.NodeInfo, len(names))
		for _, n := range names {
			m[n] = discovery.NodeInfo{SessionName: "s"}
		}
		return m
	}
	cases := []struct {
		name      string
		nodes     map[string]discovery.NodeInfo
		uiNode    string
		wantKeys  []string
		wantEmpty bool
	}{
		{
			name:     "uiNode empty returns all",
			nodes:    makeNodes("s:messenger", "s:worker", "s:critic"),
			uiNode:   "",
			wantKeys: []string{"s:messenger", "s:worker", "s:critic"},
		},
		{
			name:     "uiNode found returns only match",
			nodes:    makeNodes("s:messenger", "s:worker", "s:critic"),
			uiNode:   "messenger",
			wantKeys: []string{"s:messenger"},
		},
		{
			name:      "uiNode not found returns empty",
			nodes:     makeNodes("s:worker", "s:critic"),
			uiNode:    "messenger",
			wantEmpty: true,
		},
		{
			name:      "nil input map returns empty",
			nodes:     nil,
			uiNode:    "messenger",
			wantEmpty: true,
		},
		{
			name:     "no-colon node name matched by simple name",
			nodes:    makeNodes("messenger", "worker"),
			uiNode:   "messenger",
			wantKeys: []string{"messenger"},
		},
		{
			name:     "multi-session multi-match returns all matching entries",
			nodes:    makeNodes("s1:messenger", "s2:messenger", "s1:worker"),
			uiNode:   "messenger",
			wantKeys: []string{"s1:messenger", "s2:messenger"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cliutil.FilterToUINode(tc.nodes, tc.uiNode)
			if tc.wantEmpty {
				if len(got) != 0 {
					t.Errorf("want empty map, got %v", got)
				}
				return
			}
			if len(got) != len(tc.wantKeys) {
				t.Errorf("len = %d, want %d; got keys: %v", len(got), len(tc.wantKeys), got)
				return
			}
			for _, k := range tc.wantKeys {
				if _, ok := got[k]; !ok {
					t.Errorf("missing key %q in result %v", k, got)
				}
			}
		})
	}
}
