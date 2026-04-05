package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func readRepoFile(t *testing.T, relativePath string) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, relativePath))
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", relativePath, err)
	}
	return string(data)
}

func normalizeSpace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func assertContainsNormalized(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(normalizeSpace(got), normalizeSpace(want)) {
		t.Fatalf("content missing normalized substring %q", want)
	}
}

func TestReducedSurfaceDocContract_PopFileScopeAndCanonicalNames(t *testing.T) {
	commandsDoc := readRepoFile(t, "docs/COMMANDS.md")
	assertContainsNormalized(t, commandsDoc, "The default operator surface is `send`, `pop`, `bind`, `get-health`, and `get-health-oneline`.")
	assertContainsNormalized(t, commandsDoc, "Older name | Current path")
	assertContainsNormalized(t, commandsDoc, "`get-session-status-oneline` | `get-health-oneline` | Pure one-line formatter over `get-health`")
	assertContainsNormalized(t, commandsDoc, "`--file` remains non-destructive; it searches across contexts only when `--context-id` is omitted, and an explicit `--context-id` binds lookup to that context without archiving.")

	popSource := readRepoFile(t, "internal/cli/pop.go")
	assertContainsNormalized(t, popSource, "print a specific inbox message by filename (non-destructive; searches across contexts only when --context-id is omitted; explicit --context-id binds lookup to that context)")
}

func TestReducedSurfaceDocContract_DaemonModelAndAlertGuide(t *testing.T) {
	daemonModelDoc := readRepoFile(t, "docs/design/daemon-session-model.md")
	assertContainsNormalized(t, daemonModelDoc, "The default operator workflow assumes one daemon per observed tmux session.")
	assertContainsNormalized(t, daemonModelDoc, "two contexts cannot start daemons against the same tmux session at the same time")
	assertContainsNormalized(t, daemonModelDoc, "Running additional daemons elsewhere is an advanced/internal topology detail, not part of the reduced default operator surface.")
	assertContainsNormalized(t, daemonModelDoc, "Cross-context ownership follows the live enabled-session marker, not leftover session directories.")

	alertGuide := readRepoFile(t, "docs/guides/alert-config.md")
	assertContainsNormalized(t, alertGuide, "Use the daemon log as the reliable startup signal; the reduced default TUI does not expose a separate event-log pane.")
	assertContainsNormalized(t, alertGuide, "reminder_interval_messages")
	assertContainsNormalized(t, alertGuide, "inbox_unread_threshold")
	assertContainsNormalized(t, alertGuide, "node_spinning_seconds")
	assertContainsNormalized(t, alertGuide, "alert_cooldown_seconds")
	assertContainsNormalized(t, alertGuide, "alert_delivery_window_seconds")
	assertContainsNormalized(t, alertGuide, "pane_notify_cooldown_seconds")
}

func TestReducedSurfaceDocContract_ReadmeAndSkillsCoverCanonicalSurface(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	assertContainsNormalized(t, readme, "Unified state + notification model")
	assertContainsNormalized(t, readme, "get-health, get-health-oneline, and the default TUI are three views over the same canonical contract")
	assertContainsNormalized(t, readme, "send: Sends messages to another node using tmux-a2a-postman send.")
	assertContainsNormalized(t, readme, "a2a-role-auditor: Audits node role templates to diagnose and fix node-to-node interaction breakdowns.")

	sendSkill := readRepoFile(t, "skills/send-message/SKILL.md")
	assertContainsNormalized(t, sendSkill, "tmux-a2a-postman send --to <node> --body \"message text\"")
	assertContainsNormalized(t, sendSkill, "schema send")
	assertContainsNormalized(t, sendSkill, "State and alert policy authority lives in README.md plus docs/guides/alert-config.md and docs/design/node-state-machine.md.")

	roleAuditorSkill := readRepoFile(t, "skills/a2a-role-auditor/SKILL.md")
	assertContainsNormalized(t, roleAuditorSkill, "unread backlog")
	assertContainsNormalized(t, roleAuditorSkill, "quiet node")
	assertContainsNormalized(t, roleAuditorSkill, "late reply")
	assertContainsNormalized(t, roleAuditorSkill, "node_spinning_seconds")
}
