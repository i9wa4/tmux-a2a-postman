package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/status"
)

func TestRunGetSessionStatusOneline_NoLiveContext(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("POSTMAN_HOME", tmpDir)

	var stdout bytes.Buffer
	if err := RunGetSessionStatusOneline(&stdout, nil); err != nil {
		t.Fatalf("RunGetSessionStatusOneline: %v", err)
	}

	if stdout.String() != "" {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestRunGetSessionStatusOnelineWithContextWritesToConfiguredStdout(t *testing.T) {
	var stdout bytes.Buffer
	ctx := commandContext{
		stdout: &stdout,
		stderr: io.Discard,
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{}, nil
		},
		resolveContextID: func(contextID string) (string, error) {
			return contextID, nil
		},
		discoverAllSessions: func() ([]string, error) {
			return []string{"alpha", "beta"}, nil
		},
		collectSessionStatus: func(_, _, sessionName string, _ *config.Config) (status.SessionStatus, error) {
			return status.SessionStatus{
				SchemaVersion: status.SchemaVersion,
				SessionName:   sessionName,
				Compact:       sessionName[:1],
				Nodes:         []status.NodeStatus{},
				Windows:       []status.SessionWindow{},
			}, nil
		},
	}

	if err := runGetSessionStatusOnelineWithContext(ctx, []string{"--context-id", "ctx-oneline"}); err != nil {
		t.Fatalf("runGetSessionStatusOnelineWithContext: %v", err)
	}
	if got, want := stdout.String(), "[0]a [1]b\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
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
		{"pending", "🔷"},
		{"waiting", "🟡"},
		{"idle", "🟢"},
		{"stale", "🔴"},
		{"initial", "⚫"},
		{"", "⚫"},
	}
	for _, c := range cases {
		got := compactStatusMark(c.status)
		if got != c.want {
			t.Errorf("compactStatusMark(%q) = %q; want %q", c.status, got, c.want)
		}
	}
}

func TestCompactSessionStatusMark(t *testing.T) {
	if got := compactSessionStatusMark("unavailable"); got != "⚫" {
		t.Fatalf("compactSessionStatusMark(%q) = %q, want %q", "unavailable", got, "⚫")
	}
	if got := compactSessionStatusMark("initial"); got != "⚫" {
		t.Fatalf("compactSessionStatusMark(%q) = %q, want %q", "initial", got, "⚫")
	}
	if got := compactSessionStatusMark("pending"); got != "🔷" {
		t.Fatalf("compactSessionStatusMark(%q) = %q, want %q", "pending", got, "🔷")
	}
}

func TestCompactNodeStatusMarkUsesContextualNodeSeverityWhenVisibleReady(t *testing.T) {
	tests := []struct {
		name string
		node status.NodeStatus
		want string
	}{
		{
			name: "working pane is not definitive green",
			node: status.NodeStatus{
				VisibleState: "ready",
				Severity:     "working",
				NodeLocal: &status.NodeLocalStatus{
					State:         "working",
					Severity:      "working",
					EvidenceLevel: "inferred",
				},
			},
			want: "🔵",
		},
		{
			name: "blocked flow is not hidden behind ready pane",
			node: status.NodeStatus{
				VisibleState: "ready",
				Severity:     "blocked",
				Flow: &status.NodeFlowStatus{
					State:         "blocked",
					Severity:      "blocked",
					EvidenceLevel: "proven",
				},
			},
			want: "🔴",
		},
		{
			name: "pending obligation keeps pending mark",
			node: status.NodeStatus{
				VisibleState: "pending",
				Severity:     "working",
				NodeLocal: &status.NodeLocalStatus{
					State:         "working",
					Severity:      "working",
					EvidenceLevel: "inferred",
				},
			},
			want: "🔷",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactNodeStatusMark(tt.node); got != tt.want {
				t.Fatalf("compactNodeStatusMark(...) = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatSessionStatusOneline(t *testing.T) {
	health := status.SessionStatus{
		Compact: "🔷🟢",
	}

	if got, want := formatSessionStatusOneline(health), "🔷🟢"; got != want {
		t.Fatalf("formatSessionStatusOneline(...) = %q, want %q", got, want)
	}
}

func TestFormatAllSessionStatusOneline(t *testing.T) {
	healths := status.AllSessionStatus{
		ContextID: "20260406-ctx",
		Sessions: []status.SessionStatus{
			{
				Compact: "🔴",
			},
			{
				Compact: "🔷🟢:🟢",
			},
		},
	}

	got := formatAllSessionStatusOneline(healths)
	if got != "[0]🔴 [1]🔷🟢:🟢" {
		t.Fatalf("formatAllSessionStatusOneline(...) = %q, want %q", got, "[0]🔴 [1]🔷🟢:🟢")
	}
}

func TestFormatAllSessionStatusSeverityOneline(t *testing.T) {
	healths := status.AllSessionStatus{
		ContextID: "20260406-ctx",
		Sessions: []status.SessionStatus{
			{
				Compact:         "🔴",
				CompactSeverity: "delivery_failure:delivery:dead_letter_count=1",
			},
			{
				Compact:         "🔷🟢",
				CompactSeverity: "needs_action:node=worker:input_required=1",
			},
		},
	}

	got := formatAllSessionStatusSeverityOneline(healths)
	if got != "[0]delivery_failure:delivery:dead_letter_count=1 [1]needs_action:node=worker:input_required=1" {
		t.Fatalf("formatAllSessionStatusSeverityOneline(...) = %q, want severity status line", got)
	}
}

func TestRunGetSessionStatusOneline_UsesSessionIDOrder(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260404-ctx"
	mainSessionDir := filepath.Join(tmpDir, contextID, "main")
	reviewSessionDir := filepath.Join(tmpDir, contextID, "review")

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger --- critic --- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[critic]\ntemplate = \"critic\"\nrole = \"critic\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(mainSessionDir, "inbox", "messenger"),
		filepath.Join(mainSessionDir, "inbox", "critic"),
		filepath.Join(mainSessionDir, "inbox", "worker"),
		filepath.Join(reviewSessionDir, "inbox", "messenger"),
		filepath.Join(reviewSessionDir, "inbox", "critic"),
		filepath.Join(reviewSessionDir, "inbox", "worker"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q): %v", dir, err)
		}
	}

	if err := os.WriteFile(
		filepath.Join(tmpDir, contextID, "pane-activity.json"),
		[]byte(`{
  "%11": {"status":"idle","lastChangeAt":"2026-04-04T00:00:00Z"},
  "%12": {"status":"active","lastChangeAt":"2026-04-04T00:00:00Z"},
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
		"  \"list-sessions -F \"*)\n" +
		"    printf '%s\\n' 'review\t$210' 'main\t$173'\n" +
		"    ;;\n" +
		"  \"list-panes -a -F\")\n" +
		"    printf '%s\\n' '%12\t" + contextID + "\tmain\tworker' '%11\t" + contextID + "\tmain\tcritic' '%13\t" + contextID + "\tmain\tmessenger' '%21\t" + contextID + "\treview\tcritic' '%22\t" + contextID + "\treview\tworker'\n" +
		"    ;;\n" +
		"  \"list-windows -t main\")\n" +
		"    printf '%s\\n' '0' '1'\n" +
		"    ;;\n" +
		"  \"list-panes -t main:0\")\n" +
		"    printf '%s\\n' '0\t0\t%12\tworker\tclaude' '0\t1\t%11\tcritic\tclaude'\n" +
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

	legacy, _, ok, err := collectAllLiveSessionStatus(contextID, "", configPath)
	if err != nil {
		t.Fatalf("collectAllLiveSessionStatus: %v", err)
	}
	if !ok {
		t.Fatal("collectAllLiveSessionStatus reported no active context")
	}
	appendAllSessionStatusSnapshots(t, tmpDir, contextID, legacy.Sessions)

	var stdout bytes.Buffer
	if err := RunGetSessionStatusOneline(&stdout, []string{
		"--config", configPath,
		"--context-id", contextID,
	}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline: %v", err)
	}

	if stdout.String() != "[0]🔷🟢:🟢 [1]🟢🔷\n" {
		t.Fatalf("stdout = %q, want compact status line", stdout.String())
	}

	stdout.Reset()
	if err := RunGetSessionStatusOneline(&stdout, []string{
		"--config", configPath,
		"--context-id", contextID,
		"--severity",
	}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline(--severity): %v", err)
	}

	wantSeverity := "[0]needs_action?:node=worker:inbox_count=1 [1]needs_action?:node=critic:inbox_count=1\n"
	if stdout.String() != wantSeverity {
		t.Fatalf("stdout = %q, want severity status line %q", stdout.String(), wantSeverity)
	}
}

func TestRunGetSessionStatusOneline_PreservesSessionIDIndicesAcrossSessionsWithoutCanonicalPanes(t *testing.T) {
	tmpDir := t.TempDir()
	contextID := "20260406-ctx"
	ghostSessionDir := filepath.Join(tmpDir, contextID, "ghost")
	mainSessionDir := filepath.Join(tmpDir, contextID, "main")

	t.Setenv("POSTMAN_HOME", tmpDir)

	configPath := filepath.Join(tmpDir, "postman.toml")
	if err := os.WriteFile(
		configPath,
		[]byte("[postman]\nedges = [\"messenger --- worker\"]\n\n[messenger]\ntemplate = \"messenger\"\nrole = \"messenger\"\n\n[worker]\ntemplate = \"worker\"\nrole = \"worker\"\n"),
		0o644,
	); err != nil {
		t.Fatalf("WriteFile(postman.toml): %v", err)
	}

	for _, dir := range []string{
		filepath.Join(ghostSessionDir, "inbox", "messenger"),
		filepath.Join(ghostSessionDir, "inbox", "worker"),
		filepath.Join(mainSessionDir, "inbox", "messenger"),
		filepath.Join(mainSessionDir, "inbox", "worker"),
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
		"  \"list-sessions -F \"*)\n" +
		"    printf '%s\\n' 'ghost\t$210' 'main\t$173'\n" +
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

	legacy, _, ok, err := collectAllLiveSessionStatus(contextID, "", configPath)
	if err != nil {
		t.Fatalf("collectAllLiveSessionStatus: %v", err)
	}
	if !ok {
		t.Fatal("collectAllLiveSessionStatus reported no active context")
	}
	appendAllSessionStatusSnapshots(t, tmpDir, contextID, legacy.Sessions)

	var stdout bytes.Buffer
	if err := RunGetSessionStatusOneline(&stdout, []string{
		"--config", configPath,
		"--context-id", contextID,
	}); err != nil {
		t.Fatalf("RunGetSessionStatusOneline: %v", err)
	}

	if stdout.String() != "[0]🟢🟢 [1]⚫\n" {
		t.Fatalf("stdout = %q, want compact status line", stdout.String())
	}
}
