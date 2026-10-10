package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/journal"
)

// TestRunSendHeredoc_DirectPathVerdictExemptionFollowsInterfaceNode pins #764
// slice A on the CLI local-fallback path: the verdict-debt exemption is the
// configured interface_node and nothing else. A sender literally named
// "messenger" is no longer exempt unless interface_node says so.
func TestRunSendHeredoc_DirectPathVerdictExemptionFollowsInterfaceNode(t *testing.T) {
	tests := []struct {
		name          string
		interfaceLine string
		wantExempt    bool
	}{
		{name: "unset interface_node exempts nobody", interfaceLine: "", wantExempt: false},
		{name: "empty interface_node exempts nobody", interfaceLine: `interface_node = ""`, wantExempt: false},
		{name: "interface_node messenger exempts messenger", interfaceLine: `interface_node = "messenger"`, wantExempt: true},
		{name: "other interface_node does not exempt messenger", interfaceLine: `interface_node = "worker"`, wantExempt: false},
		{name: "legacy ui_node does not exempt messenger", interfaceLine: `ui_node = "messenger"`, wantExempt: false},
		{name: "padded interface_node messenger exempts messenger (F-002)", interfaceLine: `interface_node = " messenger "`, wantExempt: true},
		{name: "whitespace-only interface_node exempts nobody (F-002)", interfaceLine: `interface_node = "   "`, wantExempt: false},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			contextID := fmt.Sprintf("ctx-iface-exempt-%d", i)
			configPath := filepath.Join(tmpDir, "postman.toml")
			configContent := fmt.Sprintf(`[postman]
base_dir = %q
edges = ["messenger --- worker"]
verdict_debt_cap = 0
verdict_grace_seconds = 3600
%s

[messenger]
role = "messenger"

[worker]
role = "worker"
`, tmpDir, tc.interfaceLine)
			if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			sessionDir := filepath.Join(tmpDir, contextID, "test-session")
			if err := config.CreateSessionDirs(sessionDir); err != nil {
				t.Fatalf("CreateSessionDirs: %v", err)
			}
			manager := journal.NewManager(contextID, os.Getpid())
			journal.InstallProcessManager(manager)
			t.Cleanup(journal.ClearProcessManager)
			if err := manager.Bootstrap(sessionDir, "test-session", time.Now().Add(-20*time.Second).UTC()); err != nil {
				t.Fatalf("Bootstrap: %v", err)
			}
			appendSendVerdictDebt(t, sessionDir, "test-session", "messenger", "worker", "ireq_iface_exempt", time.Now().Add(-10*time.Second).UTC())

			var stdout strings.Builder
			ctx := testSendCommandContext(tmpDir, strings.NewReader("new direct work"), &stdout)
			ctx.getTmuxPaneName = func() string { return "messenger" }
			ctx.getTmuxSessionName = func() string { return "test-session" }
			ctx.loadConfig = config.LoadConfig

			err := runSendHeredocWithContext(ctx, []string{
				"--config", configPath,
				"--context-id", contextID,
				"--to", "worker",
				"--reply-required",
			})

			postEntries, readErr := os.ReadDir(filepath.Join(sessionDir, "post"))
			if readErr != nil {
				t.Fatalf("read post dir: %v", readErr)
			}
			if tc.wantExempt {
				if err != nil {
					t.Fatalf("configured interface_node must be exempt from the debt cap, got: %v", err)
				}
				if len(postEntries) != 1 {
					t.Fatalf("exempt send should write exactly one post file, got %d", len(postEntries))
				}
				return
			}
			if err == nil {
				t.Fatal("sender without a matching interface_node must hit the verdict debt cap")
			}
			if !strings.Contains(err.Error(), "verdict debt 1 above verdict_debt_cap=0") {
				t.Fatalf("unexpected rejection: %v", err)
			}
			if len(postEntries) != 0 {
				t.Fatalf("post written despite rejection: %d files", len(postEntries))
			}
		})
	}
}
