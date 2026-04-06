package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/status"
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

func TestCompactStatusMark(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"active", "🟢"},
		{"ready", "🟢"},
		{"user_input", "🟣"},
		{"pending", "🔷"},
		{"composing", "🔵"},
		{"idle", "🟢"},
		{"spinning", "🟡"},
		{"stale", "🔴"},
		{"stalled", "🔴"},
		{"stuck", "🔴"},
		{"", "🔴"},
	}
	for _, c := range cases {
		got := compactStatusMark(c.status)
		if got != c.want {
			t.Errorf("compactStatusMark(%q) = %q; want %q", c.status, got, c.want)
		}
	}
}

func TestCompactSessionStatusMark(t *testing.T) {
	if got := compactSessionStatusMark("unavailable"); got != "🔴" {
		t.Fatalf("compactSessionStatusMark(%q) = %q, want %q", "unavailable", got, "🔴")
	}
	if got := compactSessionStatusMark("pending"); got != "🔷" {
		t.Fatalf("compactSessionStatusMark(%q) = %q, want %q", "pending", got, "🔷")
	}
}

func TestWaitingFileVisibleState(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "user_input_wins_without_reply_expectation",
			content: "---\nstate: user_input\nexpects_reply: false\n---",
			want:    "user_input",
		},
		{
			name:    "composing_requires_reply_expectation",
			content: "---\nstate: composing\nexpects_reply: true\n---",
			want:    "composing",
		},
		{
			name:    "composing_without_reply_expectation_is_ignored",
			content: "---\nstate: composing\nexpects_reply: false\n---",
			want:    "",
		},
		{
			name:    "stuck_normalizes_to_stalled",
			content: "---\nstate: stuck\nexpects_reply: true\n---",
			want:    "stalled",
		},
		{
			name:    "missing_frontmatter_is_ignored",
			content: "state: spinning",
			want:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := waitingFileVisibleState(tc.content); got != tc.want {
				t.Fatalf("waitingFileVisibleState(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

func TestFormatSessionHealthOneline(t *testing.T) {
	health := status.SessionHealth{
		Compact: "🔷🔵",
	}

	if got, want := formatSessionHealthOneline(health), "🔷🔵"; got != want {
		t.Fatalf("formatSessionHealthOneline(...) = %q, want %q", got, want)
	}
}

func TestFormatAllSessionHealthOneline(t *testing.T) {
	healths := status.AllSessionHealth{
		ContextID: "20260406-ctx",
		Sessions: []status.SessionHealth{
			{
				Compact: "🔴",
			},
			{
				Compact: "🔷🔵:🟢",
			},
		},
	}

	got := formatAllSessionHealthOneline(healths)
	if got != "[0]🔴 [1]🔷🔵:🟢" {
		t.Fatalf("formatAllSessionHealthOneline(...) = %q, want %q", got, "[0]🔴 [1]🔷🔵:🟢")
	}
}

func TestRunGetSessionStatusOneline_JSONOutput_UsesCanonicalWindowOrder(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260404-ctx"
	mainSessionDir := filepath.Join(tmpDir, contextID, "main")
	reviewSessionDir := filepath.Join(tmpDir, contextID, "review")

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger -- critic -- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(mainSessionDir, "inbox", "messenger"),
		filepath.Join(mainSessionDir, "inbox", "critic"),
		filepath.Join(mainSessionDir, "inbox", "worker"),
		filepath.Join(mainSessionDir, "waiting"),
		filepath.Join(reviewSessionDir, "inbox", "messenger"),
		filepath.Join(reviewSessionDir, "inbox", "critic"),
		filepath.Join(reviewSessionDir, "inbox", "worker"),
		filepath.Join(reviewSessionDir, "waiting"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%12": {"status":"idle","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%13": {"status":"active","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%21": {"status":"active","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%22": {"status":"idle","lastChangeAt":"2026-04-04T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(mainSessionDir, "inbox", "worker", "20260404-000000-s0000-from-boss-to-worker.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(main worker inbox): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(mainSessionDir, "waiting", "20260404-000001-s0000-from-orchestrator-to-critic.md"),
		[]byte("---\nstate: composing\nexpects_reply: true\n---\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(main critic waiting): %v", err)
	}

	if err := os.WriteFile(
		filepath.Join(reviewSessionDir, "inbox", "critic", "20260404-000002-s0000-from-boss-to-critic.md"),
		[]byte("body"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(review critic inbox): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-sessions -F #{session_name}\")\n" +
		"    printf '%s\\n' 'review' 'main'\n" +
		"    ;;\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\tmain\tworker' '%12\t" + contextID + "\tmain\tcritic' '%13\t" + contextID + "\tmain\tmessenger' '%21\t" + contextID + "\treview\tcritic' '%22\t" + contextID + "\treview\tworker'\n" +
		"    ;;\n" +
		"  \"list-windows -t main\")\n" +
		"    printf '%s\\n' '0' '1'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:0\")\n" +
		"    printf '%s\\n' '0\t0\t%11\tworker\tclaude' '0\t1\t%12\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:1\")\n" +
		"    printf '%s\\n' '1\t0\t%13\tmessenger\tclaude'\n" +
		"    ;;\n" +
		"  \"list-windows -t review\")\n" +
		"    printf '%s\\n' '0'\n" +
		"    ;;\n" +
		"  \"list-panes -t review:0\")\n" +
		"    printf '%s\\n' '0\t0\t%22\tworker\tclaude' '0\t1\t%21\tcritic\tclaude'\n" +
		"    ;;\n" +
		"  *)\n" +
		"    exit 1\n" +
		"    ;;\n" +
		"esac\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("WriteFile(fake tmux): %v", err)
	}
	t.Setenv("PATH", scriptDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var stdout bytes.Buffer
	if err := RunGetSessionStatusOneline(&stdout, []string{
		"--config", configPath,
		"--context-id", contextID,
		"--json",
	}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.Status != "[0]🔷🟢 [1]🔷🔵:🟢" {
		t.Fatalf("status = %q, want %q", payload.Status, "[0]🔷🟢 [1]🔷🔵:🟢")
	}
}

func TestRunGetSessionStatusOneline_JSONOutput_SkipsSessionsWithoutCanonicalPanes(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260406-ctx"
	ghostSessionDir := filepath.Join(tmpDir, contextID, "ghost")
	mainSessionDir := filepath.Join(tmpDir, contextID, "main")

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger -- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(ghostSessionDir, "inbox", "messenger"),
		filepath.Join(ghostSessionDir, "inbox", "worker"),
		filepath.Join(ghostSessionDir, "waiting"),
		filepath.Join(mainSessionDir, "inbox", "messenger"),
		filepath.Join(mainSessionDir, "inbox", "worker"),
		filepath.Join(mainSessionDir, "waiting"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"active","lastChangeAt":"2026-04-06T00:00:00Z"},
  "%12": {"status":"idle","lastChangeAt":"2026-04-06T00:00:00Z"}
}`),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(pane-activity.json): %v", err)
	}

	scriptDir := t.TempDir()
	scriptPath := filepath.Join(scriptDir, "tmux")
	script := "#!/bin/sh\n" +
		"case \"$1 $2 $3\" in\n" +
		"  \"list-sessions -F #{session_name}\")\n" +
		"    printf '%s\\n' 'ghost' 'main'\n" +
		"    ;;\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%11\t" + contextID + "\tmain\tworker' '%12\t" + contextID + "\tmain\tmessenger'\n" +
		"    ;;\n" +
		"  \"list-windows -t ghost\")\n" +
		"    printf ''\n" +
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

	var stdout bytes.Buffer
	if err := RunGetSessionStatusOneline(&stdout, []string{
		"--config", configPath,
		"--context-id", contextID,
		"--json",
	}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline: %v", err)
	}

	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(%q): %v", stdout.String(), err)
	}
	if payload.Status != "[0]🟢🟢" {
		t.Fatalf("status = %q, want %q", payload.Status, "[0]🟢🟢")
	}
}
