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
	commandsDoc := readRepoFile(t, "docs/commands.md")
	assertContainsNormalized(t, commandsDoc, "The public surface is intentionally small: `start`, `stop`, `send`, `pop`, `status`, and `--version`.")
	assertContainsNormalized(t, commandsDoc, "Use an explicit subcommand. Bare `tmux-a2a-postman` prints usage instead of starting the daemon.")
	assertContainsNormalized(t, commandsDoc, "| `status` | Show the current runtime status |")
	assertContainsNormalized(t, commandsDoc, `"compact": "🟣"`)
	assertContainsNormalized(t, commandsDoc, `{"sent":"20240101-120000-xxxx-from-worker.md","status":"processed"}`)
	assertContainsNormalized(t, commandsDoc, `{"id":"filename.md","from":"...","to":"...","body":"...","timestamp":"..."}`)
	assertContainsNormalized(t, commandsDoc, "It archives the message after reading unless `--peek` or `--file` is used.")
	for _, hidden := range []string{
		"`get-health`",
		"`get-health-oneline`",
		"`read`",
		"`todo`",
		"`timeline`",
		"`replay`",
		"`schema`",
		"`bind`",
		"`supervisor-drain`",
		"`get-context-id`",
		"`--context-id`",
		"`--from`",
		"`--bindings`",
		"`read_context_mode`",
	} {
		if strings.Contains(commandsDoc, hidden) {
			t.Fatalf("docs/commands.md exposes hidden public surface %s", hidden)
		}
	}

	popSource := readRepoFile(t, "internal/cli/pop.go")
	assertContainsNormalized(t, popSource, "print a specific inbox message by filename from the current session inbox (non-destructive)")
}

func TestReducedSurfaceDocContract_DaemonModelAndAlertGuide(t *testing.T) {
	daemonModelDoc := readRepoFile(t, "docs/design/daemon-session-model.md")
	assertContainsNormalized(t, daemonModelDoc, "The default operator workflow assumes one daemon process per Unix user.")
	assertContainsNormalized(t, daemonModelDoc, "concurrent starts cannot race into two daemons")
	assertContainsNormalized(t, daemonModelDoc, "A different Unix user's daemon is still treated as alive for cleanup safety, but it is not treated as the current user's owner.")
	assertContainsNormalized(t, daemonModelDoc, "Cross-context ownership follows the live enabled-session marker, not leftover session directories.")

	alertGuide := readRepoFile(t, "docs/guides/alert-config.md")
	assertContainsNormalized(t, alertGuide, "Use the daemon log as the reliable startup signal; the reduced default TUI does not expose a separate event-log pane.")
	assertContainsNormalized(t, alertGuide, "Operator Triage Map")
	assertContainsNormalized(t, alertGuide, "Canonical visible state, not a daemon alert by itself")
	assertContainsNormalized(t, alertGuide, "Coordination signal separate from `ui_node` inbox alerts")
	assertContainsNormalized(t, alertGuide, "`message_footer` controls reply guidance only for stored messages written by `send`.")
	assertContainsNormalized(t, alertGuide, "Daemon alerts and heartbeat mail use `daemon_message_template`, and dead-letter notifications embed their own re-send instructions.")
	assertContainsNormalized(t, alertGuide, "`pop` should print the delivered message body as stored, not invent a second hard-coded reply hint.")
	assertContainsNormalized(t, alertGuide, "reminder_interval_messages")
	assertContainsNormalized(t, alertGuide, "inbox_unread_threshold")
	assertContainsNormalized(t, alertGuide, "node_spinning_seconds")
	assertContainsNormalized(t, alertGuide, "alert_cooldown_seconds")
	assertContainsNormalized(t, alertGuide, "alert_delivery_window_seconds")
	assertContainsNormalized(t, alertGuide, "pane_notify_cooldown_seconds")
}

func TestReducedSurfaceDocContract_NotificationDesignStartsFromUnifiedModel(t *testing.T) {
	notificationDoc := readRepoFile(t, "docs/design/notification.md")
	assertContainsNormalized(t, notificationDoc, "get-health, get-health-oneline, and the default TUI are three views over the same canonical contract.")
	assertContainsNormalized(t, notificationDoc, "This document starts from that unified operator model, then maps the notification surfaces, delivery paths, and guard policy in the current tree.")
	assertContainsNormalized(t, notificationDoc, "## 2. Notification Surfaces in the Unified Model")

	if strings.Contains(notificationDoc, "There are eight distinct notification mechanisms") {
		t.Fatal("notification design doc still opens with the old mechanism-first framing")
	}
	if strings.Contains(notificationDoc, "This document explains all eight mechanisms") {
		t.Fatal("notification design doc still teaches the old mechanism-first summary")
	}
	if strings.Contains(notificationDoc, "## 2. Notification Mechanisms") {
		t.Fatal("notification design doc still uses the old mechanism-first section heading")
	}
}

func TestReducedSurfaceDocContract_ReadmeAndSkillsCoverCanonicalSurface(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	assertContainsNormalized(t, readme, "Runtime status model")
	assertContainsNormalized(t, readme, "`status`, `status --json`, and the default TUI are three views over the same canonical contract")
	assertContainsNormalized(t, readme, "Quick reading guide")
	assertContainsNormalized(t, readme, "Canonical visible state for a node right now")
	assertContainsNormalized(t, readme, "[docs/commands.md](docs/commands.md)")
	assertContainsNormalized(t, readme, "The README teaches the beginner/operator loop.")
	assertContainsNormalized(t, readme, "Use explicit subcommands; bare `tmux-a2a-postman` prints usage and does not start the daemon.")
	assertContainsNormalized(t, readme, "For stored messages written by `send`, reply guidance comes from `message_footer` in `internal/config/postman.default.toml`.")
	assertContainsNormalized(t, readme, "`pop` prints the stored message as written and does not add a second hard-coded reply footer.")
	assertContainsNormalized(t, readme, "send: Sends messages to another node using tmux-a2a-postman send.")
	assertContainsNormalized(t, readme, "a2a-role-auditor: Audits node role templates to diagnose and fix node-to-node interaction breakdowns.")
	for _, hidden := range []string{
		"tmux-a2a-postman get-health",
		"tmux-a2a-postman get-health-oneline",
		"tmux-a2a-postman read",
		"tmux-a2a-postman todo",
		"tmux-a2a-postman timeline",
		"tmux-a2a-postman replay",
		"tmux-a2a-postman schema",
		"tmux-a2a-postman bind",
		"tmux-a2a-postman get-context-id",
		"`read_context_mode`",
		"`journal_health_cutover_enabled`",
		"`journal_compatibility_cutover_enabled`",
		"`[heartbeat].enabled`",
	} {
		if strings.Contains(readme, hidden) {
			t.Fatalf("README still exposes hidden public surface %q", hidden)
		}
	}

	sendSkill := readRepoFile(t, "skills/send-message/SKILL.md")
	assertContainsNormalized(t, sendSkill, "tmux-a2a-postman send --to <node> --body \"message text\"")
	assertContainsNormalized(t, sendSkill, "The public scope includes: `to`, `body`, `idempotency-key`, `json`.")
	if strings.Contains(sendSkill, "schema") {
		t.Fatal("send skill still teaches schema discovery")
	}

	roleAuditorSkill := readRepoFile(t, "skills/a2a-role-auditor/SKILL.md")
	assertContainsNormalized(t, roleAuditorSkill, "unread backlog")
	assertContainsNormalized(t, roleAuditorSkill, "quiet node")
	assertContainsNormalized(t, roleAuditorSkill, "late reply")
	assertContainsNormalized(t, roleAuditorSkill, "status --json")
	assertContainsNormalized(t, roleAuditorSkill, "`message_footer` | appended to stored `send` mail | `{can_talk_to}`, `{reply_command}`")
	assertContainsNormalized(t, roleAuditorSkill, "`daemon_message_template` | daemon-originated mail | `{role_content}`, `{talks_to_line}`, `{reply_command}`")
	assertContainsNormalized(t, roleAuditorSkill, "Dead-letter re-send instructions (written by dead-letter notification code)")
	for _, hidden := range []string{"get-health", "dropped_ball", "heartbeat mail"} {
		if strings.Contains(roleAuditorSkill, hidden) {
			t.Fatalf("role auditor skill still exposes hidden term %q", hidden)
		}
	}
}

func TestReducedSurfaceDocContract_RuntimeLifecycleRetentionDocs(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	assertContainsNormalized(t, readme, "`retention_period_days` controls that startup cleanup window. The embedded default is `90`.")
	assertContainsNormalized(t, readme, "| `{baseDir}/lock/` | Active coordination state | Always preserved |")
	assertContainsNormalized(t, readme, "| `{baseDir}/{contextId}/supervisor-memory/` | Durable supervisor memory state | Always preserved |")

	commandsDoc := readRepoFile(t, "docs/commands.md")
	assertContainsNormalized(t, commandsDoc, "## 7. Runtime Directory Lifecycle")
	assertContainsNormalized(t, commandsDoc, "`retention_period_days` controls cleanup of inactive runtime state.")
	assertContainsNormalized(t, commandsDoc, "Unknown entries are preserved by default instead of being pruned by name.")
}
