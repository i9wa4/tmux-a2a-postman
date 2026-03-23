package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsShellCommand(t *testing.T) {
	shells := []string{"bash", "zsh", "sh", "fish", "dash", "ksh", "csh", "tcsh", "nu"}
	for _, s := range shells {
		if !isShellCommand(s) {
			t.Errorf("isShellCommand(%q) = false, want true", s)
		}
	}
	nonShells := []string{"claude", "python", "node", "ruby", ""}
	for _, s := range nonShells {
		if isShellCommand(s) {
			t.Errorf("isShellCommand(%q) = true, want false", s)
		}
	}
}

func TestStatusDot_NonTTY(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"active", "🟢"},
		{"ready", "🟢"},
		{"user_input", "🟣"},
		{"pending", "🔷"},
		{"composing", "🔵"},
		{"idle", "🟡"},
		{"spinning", "🟡"},
		{"stale", "🔴"},
		{"stalled", "🔴"},
		{"stuck", "🔴"},
		{"", "🔴"},
	}
	for _, c := range cases {
		got := statusDot(c.status, false)
		if got != c.want {
			t.Errorf("statusDot(%q, false) = %q; want %q", c.status, got, c.want)
		}
	}
}

func TestStatusDot_TTY(t *testing.T) {
	// In TTY mode, output contains ANSI codes. We verify it contains the dot
	// and does not equal the plain emoji.
	ttyCases := []string{"active", "user_input", "composing", "idle", "spinning", "stale"}
	for _, status := range ttyCases {
		got := statusDot(status, true)
		if got == "" {
			t.Errorf("statusDot(%q, true) returned empty string", status)
		}
		if !strings.Contains(got, "●") {
			t.Errorf("statusDot(%q, true) = %q; want string containing ●", status, got)
		}
	}
}

// TestApplyWaitingOverlay verifies the overlay priority logic for all key cases.
// Pattern: t.TempDir() + filesystem fixtures (mirrors e2e/e2e_test.go).
func TestApplyWaitingOverlay(t *testing.T) {
	tests := []struct {
		name                 string
		waitingFiles         map[string]string // filename -> content
		initialPaneActivity  map[string]string // paneID -> state
		sessionTitleToPaneID map[string]string // "session:title" -> paneID
		sessionSubdir        string
		wantPaneActivity     map[string]string // expected result
	}{
		{
			name: "composing_overrides_active",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-worker.md": "---\nstate: composing\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "active"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "composing"},
		},
		{
			name: "composing_overrides_idle",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-worker.md": "---\nstate: composing\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "idle"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "composing"},
		},
		{
			// Two waiting files for the same pane: spinning (rank 3) beats composing (rank 1).
			name: "spinning_overrides_composing_multiple_files",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-worker.md": "---\nstate: composing\n---",
				"20260101-000001-s0000-from-messenger-to-worker.md":    "---\nstate: spinning\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "active"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "spinning"},
		},
		{
			// stalled (rank 4) beats spinning (rank 3); "stuck" compat maps to "stalled".
			name: "stalled_overrides_spinning",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-worker.md": "---\nstate: spinning\n---",
				"20260101-000001-s0000-from-messenger-to-worker.md":    "---\nstate: stuck\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "idle"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "stalled"},
		},
		{
			// user_input (rank 0) must NOT override composing (rank 1).
			name: "user_input_does_not_override_composing",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-worker.md": "---\nstate: composing\n---",
				"20260101-000001-s0000-from-messenger-to-worker.md":    "---\nstate: user_input\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "active"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "composing"},
		},
		{
			name: "malformed_filename_skipped",
			waitingFiles: map[string]string{
				"not-a-valid-message.md": "---\nstate: composing\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "active"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "active"},
		},
		{
			name: "unknown_recipient_skipped",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-unknown-node.md": "---\nstate: composing\n---",
			},
			initialPaneActivity:  map[string]string{"%10": "active"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "active"},
		},
		{
			name:                 "no_waiting_files_unchanged",
			waitingFiles:         map[string]string{},
			initialPaneActivity:  map[string]string{"%10": "idle"},
			sessionTitleToPaneID: map[string]string{"mysession:worker": "%10"},
			sessionSubdir:        "mysession",
			wantPaneActivity:     map[string]string{"%10": "idle"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			ctxDir := filepath.Join(tmpDir, "session-test")
			waitingDir := filepath.Join(ctxDir, tc.sessionSubdir, "waiting")
			if err := os.MkdirAll(waitingDir, 0o755); err != nil {
				t.Fatalf("creating waiting dir: %v", err)
			}
			for name, content := range tc.waitingFiles {
				if err := os.WriteFile(filepath.Join(waitingDir, name), []byte(content), 0o644); err != nil {
					t.Fatalf("writing waiting file %s: %v", name, err)
				}
			}

			pairs := [][2]string{{ctxDir, tc.sessionSubdir}}
			paneActivity := make(map[string]string)
			for k, v := range tc.initialPaneActivity {
				paneActivity[k] = v
			}

			applyWaitingOverlay(pairs, tc.sessionTitleToPaneID, paneActivity)

			for paneID, wantState := range tc.wantPaneActivity {
				if got := paneActivity[paneID]; got != wantState {
					t.Errorf("paneActivity[%q] = %q, want %q", paneID, got, wantState)
				}
			}
		})
	}
}

// captureStdoutStderr redirects os.Stdout and os.Stderr for the duration of fn,
// returning captured output as strings.
func captureStdoutStderr(fn func()) (stdout, stderr string) {
	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr
	fn()
	wOut.Close()
	wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	var bufOut, bufErr bytes.Buffer
	_, _ = bufOut.ReadFrom(rOut)
	_, _ = bufErr.ReadFrom(rErr)
	return bufOut.String(), bufErr.String()
}

// TestRunListDeadLetters_EmptyDir: empty dead-letter dir → exit 0, stderr message.
func TestRunListDeadLetters_EmptyDir(t *testing.T) {
	dlDir := t.TempDir()

	var err error
	out, errOut := captureStdoutStderr(func() {
		err = listDeadLettersFromDir(dlDir)
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Errorf("stdout = %q; want empty", out)
	}
	if !strings.Contains(errOut, "No dead-letter messages.") {
		t.Errorf("stderr = %q; want to contain 'No dead-letter messages.'", errOut)
	}
}

// TestRunListDeadLetters_OneMessage: one valid message → stdout has timestamp/from/to, no filename.
func TestRunListDeadLetters_OneMessage(t *testing.T) {
	dlDir := t.TempDir()
	filename := "20260322-100000-s0000-from-sender-node-to-recipient-node-dl-unknown.md"
	if err := os.WriteFile(filepath.Join(dlDir, filename), []byte("---\nmethod: message/send\n---\n"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	var err error
	out, _ := captureStdoutStderr(func() {
		err = listDeadLettersFromDir(dlDir)
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "from=sender-node") {
		t.Errorf("stdout = %q; want to contain 'from=sender-node'", out)
	}
	if !strings.Contains(out, "to=recipient-node") {
		t.Errorf("stdout = %q; want to contain 'to=recipient-node'", out)
	}
	if strings.Contains(out, filename) {
		t.Errorf("stdout = %q; must NOT contain filename", out)
	}
	if strings.Contains(out, dlDir) {
		t.Errorf("stdout = %q; must NOT contain directory path", out)
	}
}

// TestRunListDeadLetters_MalformedFrontmatter: unparseable filename → one line ending with [unreadable].
func TestRunListDeadLetters_MalformedFrontmatter(t *testing.T) {
	dlDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dlDir, "bad-filename.md"), []byte("not valid"), 0o644); err != nil {
		t.Fatalf("writing file: %v", err)
	}

	var err error
	out, _ := captureStdoutStderr(func() {
		err = listDeadLettersFromDir(dlDir)
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "[unreadable]") {
		t.Errorf("stdout = %q; want to contain '[unreadable]'", out)
	}
}

// TestResend_OldestPicksLexFirst: lex-first file is selected from two candidates.
func TestResend_OldestPicksLexFirst(t *testing.T) {
	dlDir := t.TempDir()
	postDir := t.TempDir()

	first := "20260101-000000-s0000-from-worker-to-orchestrator-dl-unknown.md"
	second := "20260201-000000-s0000-from-worker-to-orchestrator-dl-unknown.md"
	for _, name := range []string{first, second} {
		if err := os.WriteFile(filepath.Join(dlDir, name), []byte("body"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}

	path, ok, err := findOldestDeadLetterFile(dlDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok {
		t.Fatal("expected file to be found")
	}

	baseName := filepath.Base(path)
	dst := filepath.Join(postDir, baseName)
	if err := os.Rename(path, dst); err != nil {
		t.Fatalf("rename: %v", err)
	}

	if _, err := os.Stat(filepath.Join(postDir, first)); err != nil {
		t.Errorf("expected %s in post/; stat error: %v", first, err)
	}
	if _, err := os.Stat(filepath.Join(dlDir, second)); err != nil {
		t.Errorf("expected %s to remain in dead-letter/; stat error: %v", second, err)
	}
}

// TestResend_OldestEmptyDir: empty dead-letter dir → ok=false, no error.
func TestResend_OldestEmptyDir(t *testing.T) {
	dlDir := t.TempDir()

	path, ok, err := findOldestDeadLetterFile(dlDir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Errorf("expected ok=false, got path=%q", path)
	}
}

// TestResend_OldestAndFileMutuallyExclusive: --oldest + --file → error before tmux call.
func TestResend_OldestAndFileMutuallyExclusive(t *testing.T) {
	err := runResend([]string{"--oldest", "--file", "some.md"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("error = %q; want to contain 'mutually exclusive'", err.Error())
	}
}

// --- Issue #304: --from flag tests ---

// fromTestFixtures sets up a temporary base directory, config file, and
// bindings.toml for runCreateDraft --from tests.
// It unsets TMUX and TMUX_PANE so sender auto-detection is skipped.
// The returned configPath and bindingsPath are ready to pass as CLI flags.
// The returned baseDir/ctx1/mysession/draft/ tree will be created by
// runCreateDraft itself; this helper only creates the parent directories.
func fromTestFixtures(t *testing.T) (baseDir, configPath, bindingsPath string) {
	t.Helper()

	tmpDir := t.TempDir()
	baseDir = filepath.Join(tmpDir, "postman-home")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		t.Fatalf("creating base dir: %v", err)
	}

	// Write a minimal config TOML pointing to our temp base dir.
	// [worker] and [channel-a] sections satisfy the "no nodes defined" validation rule.
	configPath = filepath.Join(tmpDir, "config.toml")
	configContent := fmt.Sprintf("[postman]\nbase_dir = %q\n\n[worker]\n\n[channel-a]\n", baseDir)
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("writing config.toml: %v", err)
	}

	// Write bindings.toml with one active phony node.
	// channel-a → pane_node_name=worker (Row 4: active, session, title, node).
	bindingsPath = filepath.Join(tmpDir, "bindings.toml")
	bindingsContent := `[[binding]]
channel_id = "channel-a"
node_name = "channel-a"
context_id = "ctx1"
session_name = "mysession"
pane_title = "worker"
pane_node_name = "worker"
active = true
permitted_senders = ["channel-a"]
`
	if err := os.WriteFile(bindingsPath, []byte(bindingsContent), 0o600); err != nil {
		t.Fatalf("writing bindings.toml: %v", err)
	}

	// Unset tmux env vars so runCreateDraft skips tmux auto-detection.
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

	return baseDir, configPath, bindingsPath
}

// baseArgs returns the common CLI arguments used by all --from tests.
func fromBaseArgs(configPath, bindingsPath string) []string {
	return []string{
		"--to", "worker",
		"--from", "channel-a",
		"--bindings", bindingsPath,
		"--config", configPath,
		"--session", "mysession",
		"--context-id", "ctx1",
	}
}

// TestRunCreateDraft_FromMissingBindings: --from without --bindings → error.
func TestRunCreateDraft_FromMissingBindings(t *testing.T) {
	err := runCreateDraft([]string{"--to", "worker", "--from", "channel-a"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "--bindings is required") {
		t.Errorf("error = %q; want '--bindings is required'", err.Error())
	}
}

// TestRunCreateDraft_FromRegexInvalid: --from with bad node name → error before registry load.
func TestRunCreateDraft_FromRegexInvalid(t *testing.T) {
	_, configPath, bindingsPath := fromTestFixtures(t)
	err := runCreateDraft([]string{
		"--to", "worker",
		"--from", "bad node!",
		"--bindings", bindingsPath,
		"--config", configPath,
		"--session", "mysession",
		"--context-id", "ctx1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid node name") {
		t.Errorf("error = %q; want 'invalid node name'", err.Error())
	}
}

// TestRunCreateDraft_FromRegistryMiss: --from name not in registry → error.
func TestRunCreateDraft_FromRegistryMiss(t *testing.T) {
	_, configPath, bindingsPath := fromTestFixtures(t)
	err := runCreateDraft([]string{
		"--to", "worker",
		"--from", "channel-z",
		"--bindings", bindingsPath,
		"--config", configPath,
		"--session", "mysession",
		"--context-id", "ctx1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "no active binding found") {
		t.Errorf("error = %q; want 'no active binding found'", err.Error())
	}
}

// TestRunCreateDraft_FromUnassignedBinding: binding has pane_node_name="" (Row 3) → error.
func TestRunCreateDraft_FromUnassignedBinding(t *testing.T) {
	tmpDir := t.TempDir()
	baseDir := filepath.Join(tmpDir, "postman-home")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		t.Fatalf("creating base dir: %v", err)
	}
	configPath := filepath.Join(tmpDir, "config.toml")
	if err := os.WriteFile(configPath,
		[]byte(fmt.Sprintf("[postman]\nbase_dir = %q\n\n[worker]\n\n[channel-b]\n", baseDir)), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	// Row 3: active, session_name, pane_title, pane_node_name="" — title-matched only.
	bindingsPath := filepath.Join(tmpDir, "bindings.toml")
	bindingsContent := `[[binding]]
channel_id = "channel-b"
node_name = "channel-b"
context_id = "ctx1"
session_name = "mysession"
pane_title = "worker"
pane_node_name = ""
active = true
permitted_senders = ["channel-b"]
`
	if err := os.WriteFile(bindingsPath, []byte(bindingsContent), 0o600); err != nil {
		t.Fatalf("writing bindings: %v", err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")

	err := runCreateDraft([]string{
		"--to", "worker",
		"--from", "channel-b",
		"--bindings", bindingsPath,
		"--config", configPath,
		"--session", "mysession",
		"--context-id", "ctx1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "empty pane_node_name") {
		t.Errorf("error = %q; want 'empty pane_node_name'", err.Error())
	}
}

// TestRunCreateDraft_FromWrongRecipient: --to does not match pane_node_name → error.
func TestRunCreateDraft_FromWrongRecipient(t *testing.T) {
	_, configPath, bindingsPath := fromTestFixtures(t)
	err := runCreateDraft([]string{
		"--to", "some-other-pane",
		"--from", "channel-a",
		"--bindings", bindingsPath,
		"--config", configPath,
		"--session", "mysession",
		"--context-id", "ctx1",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "--to must be") {
		t.Errorf("error = %q; want '--to must be'", err.Error())
	}
}

// TestRunCreateDraft_FromHappyPath: valid --from creates a draft file with correct sender.
func TestRunCreateDraft_FromHappyPath(t *testing.T) {
	baseDir, configPath, bindingsPath := fromTestFixtures(t)
	args := fromBaseArgs(configPath, bindingsPath)
	if err := runCreateDraft(args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	draftDir := filepath.Join(baseDir, "ctx1", "mysession", "draft")
	entries, err := os.ReadDir(draftDir)
	if err != nil {
		t.Fatalf("reading draft dir %s: %v", draftDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 draft file, got %d", len(entries))
	}
	content, err := os.ReadFile(filepath.Join(draftDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}
	if !strings.Contains(string(content), "channel-a") {
		t.Errorf("draft frontmatter should contain sender 'channel-a', got: %q", string(content))
	}
}

// TestRunCreateDraft_IdempotencyKeyPresent: --idempotency-key injects field into frontmatter.
func TestRunCreateDraft_IdempotencyKeyPresent(t *testing.T) {
	baseDir, configPath, bindingsPath := fromTestFixtures(t)
	args := append(fromBaseArgs(configPath, bindingsPath), "--idempotency-key", "tok-abc123")
	if err := runCreateDraft(args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	draftDir := filepath.Join(baseDir, "ctx1", "mysession", "draft")
	entries, err := os.ReadDir(draftDir)
	if err != nil {
		t.Fatalf("reading draft dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no draft file created")
	}
	content, err := os.ReadFile(filepath.Join(draftDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}
	if !strings.Contains(string(content), "idempotency_key: tok-abc123") {
		t.Errorf("draft content = %q; want 'idempotency_key: tok-abc123'", string(content))
	}
}

// TestRunCreateDraft_IdempotencyKeyAbsent: without --idempotency-key, no field in draft.
func TestRunCreateDraft_IdempotencyKeyAbsent(t *testing.T) {
	baseDir, configPath, bindingsPath := fromTestFixtures(t)
	if err := runCreateDraft(fromBaseArgs(configPath, bindingsPath)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	draftDir := filepath.Join(baseDir, "ctx1", "mysession", "draft")
	entries, err := os.ReadDir(draftDir)
	if err != nil {
		t.Fatalf("reading draft dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no draft file created")
	}
	content, err := os.ReadFile(filepath.Join(draftDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("reading draft: %v", err)
	}
	if strings.Contains(string(content), "idempotency_key") {
		t.Errorf("draft should NOT contain idempotency_key, but got: %q", string(content))
	}
}

// TestRunCreateDraft_ContextIDInvalid: Gap 5 — invalid --context-id value → error.
func TestRunCreateDraft_ContextIDInvalid(t *testing.T) {
	_, configPath, bindingsPath := fromTestFixtures(t)
	err := runCreateDraft([]string{
		"--to", "worker",
		"--from", "channel-a",
		"--bindings", bindingsPath,
		"--config", configPath,
		"--session", "mysession",
		"--context-id", "../traversal",
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid value") {
		t.Errorf("error = %q; want 'invalid value'", err.Error())
	}
}
