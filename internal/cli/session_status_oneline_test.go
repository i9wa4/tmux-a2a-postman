package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunGetSessionStatusOneline_JSONOutput_NoLiveContext(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	var stdout bytes.Buffer
	if err := RunGetSessionStatusOneline(&stdout, []string{"--json"}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline: %v", err)
	}

	if stdout.String() != "{\"status\":\"\"}\n" {
		t.Fatalf("stdout = %q, want empty-status JSON", stdout.String())
	}
}

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

func TestApplyWaitingOverlay(t *testing.T) {
	tests := []struct {
		name                 string
		waitingFiles         map[string]string
		initialPaneActivity  map[string]string
		sessionTitleToPaneID map[string]string
		sessionSubdir        string
		wantPaneActivity     map[string]string
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
			name: "session_prefixed_recipient_uses_explicit_session",
			waitingFiles: map[string]string{
				"20260101-000000-s0000-from-orchestrator-to-review-session:worker.md": "---\nstate: composing\n---",
			},
			initialPaneActivity:  map[string]string{"%20": "active"},
			sessionTitleToPaneID: map[string]string{"review-session:worker": "%20"},
			sessionSubdir:        "source-session",
			wantPaneActivity:     map[string]string{"%20": "composing"},
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

func TestApplyPendingOverlay(t *testing.T) {
	tmpDir := t.TempDir()
	ctxDir := filepath.Join(tmpDir, "session-test")
	inboxDir := filepath.Join(ctxDir, "mysession", "inbox", "worker")
	if err := os.MkdirAll(inboxDir, 0o755); err != nil {
		t.Fatalf("creating inbox dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(inboxDir, "20260101-000000-s0000-from-orchestrator-to-worker.md"), []byte("body"), 0o644); err != nil {
		t.Fatalf("writing inbox file: %v", err)
	}

	pairs := [][2]string{{ctxDir, "mysession"}}
	sessionTitleToPaneID := map[string]string{"mysession:worker": "%10"}
	paneActivity := map[string]string{"%10": "active"}

	applyPendingOverlay(pairs, sessionTitleToPaneID, paneActivity)

	if got := paneActivity["%10"]; got != "pending" {
		t.Fatalf("paneActivity[%%10] = %q, want pending", got)
	}
}
