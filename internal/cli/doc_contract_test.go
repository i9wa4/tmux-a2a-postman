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
	assertContainsNormalized(t, daemonModelDoc, "Multiple daemons may run simultaneously, but startup is still serialized per tmux session name.")
	assertContainsNormalized(t, daemonModelDoc, "two contexts cannot start daemons against the same tmux session at the same time")

	alertGuide := readRepoFile(t, "docs/guides/alert-config.md")
	assertContainsNormalized(t, alertGuide, "Use the daemon log as the reliable startup signal; the reduced default TUI does not expose a separate event-log pane.")
}

func TestReducedSurfaceDocContract_SendSkillUsesCanonicalCommandNames(t *testing.T) {
	sendSkill := readRepoFile(t, "skills/send-message/SKILL.md")
	assertContainsNormalized(t, sendSkill, "tmux-a2a-postman send --to <node> --body \"message text\"")
	assertContainsNormalized(t, sendSkill, "schema send")
}
