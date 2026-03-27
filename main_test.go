package main

import (
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
			result, err := parseParams(tc.input)
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

// TestRunGetSessionHealth verifies flag parsing for runGetSessionHealth across key cases.
func TestRunGetSessionHealth(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string // non-empty: error must contain this substring
	}{
		{
			name:       "valid session name no error on flag parse",
			args:       []string{"--session", "my-session"},
			wantErrSub: "", // may fail at context-id resolution, but not at flag parse or validation
		},
		{
			name:       "path traversal rejected",
			args:       []string{"--session", "../bad"},
			wantErrSub: "invalid value",
		},
		{
			// underscore is not a path separator; "bad_name" is valid for tmux sessions
			name:       "underscore session name accepted",
			args:       []string{"--session", "bad_name"},
			wantErrSub: "", // may fail at context-id resolution, not at validation
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
			err := runGetSessionHealth(tc.args)
			if tc.wantErrSub == "" {
				// Valid case: must not fail with "flag provided but not defined" or "invalid value".
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
