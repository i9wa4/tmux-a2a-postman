package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Real terminal input drives every recipient action. No Codex, model server,
// API key, recipient registration, or Bash/Readline integration is involved.
func TestNotificationDrivenTerminalAgents(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 unavailable")
	}
	f := newRebuiltCLIFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "scripts/validation/notification-smoke.py", "--postman", f.bin, "--agent", "fixture", "--case", "all", "--terminal", "both", "--timeout", "10", "--output", filepath.Join(f.dir, "notification-report.json"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terminal-driven notification matrix: %v\n%s", err, out)
	}
	t.Log("16 real-tmux cases passed: plain/bracketed input, rally, busy, concurrent, parent/child, approval/rejection and disabled notification")
	// Exercise a production-shaped, numbered postman.md rather than replacing
	// the user's established template sections with an edges-only topology.
	for _, scenario := range []string{"rally", "approval", "rejected"} {
		t.Run("existing-config/"+scenario, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, python, "scripts/validation/notification-smoke.py", "--postman", f.bin,
				"--case", scenario, "--terminal", "both", "--timeout", "10", "--postman-md",
				"internal/config/testdata/postman-with-templates.md", "--output", filepath.Join(f.dir, scenario+"-existing-config.json"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("existing configuration %s: %v\n%s", scenario, err, out)
			}
		})
	}
}
