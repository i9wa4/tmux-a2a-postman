package app

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
	// Twenty terminal cases plus six existing-config regressions share this
	// deadline. Native tmux/file operations are slower on macOS; each driver's
	// individual wait remains bounded rather than exhausting this batch early.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "scripts/validation/notification-smoke.py", "--postman", f.bin, "--agent", "fixture", "--case", "all", "--terminal", "both", "--timeout", "10", "--output", filepath.Join(f.dir, "notification-report.json"))
	cmd.Dir = repositorySource(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terminal-driven notification matrix: %v\n%s", err, out)
	}
	t.Log("20 real-tmux cases passed: plain/bracketed input, rally, busy, concurrent, parent/child, approval/rejection, disabled notification, absorbed Enter and 8-request approval bursts")
	// Exercise a production-shaped, numbered postman.md rather than replacing
	// the user's established template sections with an edges-only topology.
	for _, scenario := range []string{"rally", "approval", "rejected"} {
		t.Run("existing-config/"+scenario, func(t *testing.T) {
			cmd := exec.CommandContext(ctx, python, "scripts/validation/notification-smoke.py", "--postman", f.bin,
				"--case", scenario, "--terminal", "both", "--timeout", "10", "--postman-md",
				"internal/config/testdata/postman-with-templates.md", "--output", filepath.Join(f.dir, scenario+"-existing-config.json"))
			cmd.Dir = repositorySource(t)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("existing configuration %s: %v\n%s", scenario, err, out)
			}
		})
	}
}
