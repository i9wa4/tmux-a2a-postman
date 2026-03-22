package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
