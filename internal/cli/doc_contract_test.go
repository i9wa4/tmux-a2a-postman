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
	assertContainsNormalized(t, commandsDoc, "The public surface is intentionally small: `start`, `stop`, `send`, `pop`, `get-health`, `get-health-oneline`, `version`, `help`, and `--version`.")
	assertContainsNormalized(t, commandsDoc, "Use an explicit subcommand. Bare `tmux-a2a-postman` prints usage instead of starting the daemon.")
	assertContainsNormalized(t, commandsDoc, "| `get-health` | Print canonical session health JSON |")
	assertContainsNormalized(t, commandsDoc, "| `get-health-oneline` | Print compact all-session health |")
	assertContainsNormalized(t, commandsDoc, "| `version` | Print the build version JSON |")
	assertContainsNormalized(t, commandsDoc, "| `help [topic]` | Print built-in help |")
	assertContainsNormalized(t, commandsDoc, `"compact": "🔷"`)
	assertContainsNormalized(t, commandsDoc, `{"sent":"20240101-120000-xxxx-from-worker.md","status":"processed","context_id":"...","session":"...","from":"worker","to":"critic","submit_path":"daemon-submit"}`)
	assertContainsNormalized(t, commandsDoc, `{"status":"message","id":"filename.md","from":"...","to":"...","timestamp":"...","body":"...","content":"...","unread_before":1,"remaining":0}`)
	assertContainsNormalized(t, commandsDoc, "`pop` reads the next unread inbox message for the current pane title and archives the message after reading.")
	assertContainsNormalized(t, commandsDoc, "tmux-a2a-postman send --help")
	for _, hidden := range []string{
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
		"`read_context_mode`",
		"`status`",
		"`--params`",
		"`--session`",
		"`--peek`",
		"`--file`",
		"`--no-tui`",
		"`--log-file`",
	} {
		if strings.Contains(commandsDoc, hidden) {
			t.Fatalf("docs/commands.md exposes hidden public surface %s", hidden)
		}
	}

	popSource := readRepoFile(t, "internal/cli/pop.go")
	if strings.Contains(popSource, "findInboxFileByName") {
		t.Fatal("pop source still contains removed file-specific inbox lookup")
	}
}

func TestReducedSurfaceDocContract_DaemonModelAndNotificationGuide(t *testing.T) {
	daemonModelDoc := readRepoFile(t, "docs/design/daemon-session-model.md")
	assertContainsNormalized(t, daemonModelDoc, "The default operator workflow assumes one daemon process per Unix user.")
	assertContainsNormalized(t, daemonModelDoc, "concurrent starts cannot race into two daemons")
	assertContainsNormalized(t, daemonModelDoc, "A different Unix user's daemon is still treated as alive for cleanup safety, but it is not treated as the current user's owner.")
	assertContainsNormalized(t, daemonModelDoc, "Cross-context ownership follows the live enabled-session marker, not leftover session directories.")

	notificationDoc := readRepoFile(t, "docs/design/notification.md")
	assertContainsNormalized(t, notificationDoc, "The daemon delivers mail to the recipient inbox, sends a pane hint to that recipient when delivery succeeds, and emits startup auto-PING messages.")
	assertContainsNormalized(t, notificationDoc, "`ui_node` is not a general escalation channel.")
	assertContainsNormalized(t, notificationDoc, "The remaining notification-related public settings are")
}

func TestReducedSurfaceDocContract_NotificationDesignStartsFromUnifiedModel(t *testing.T) {
	notificationDoc := readRepoFile(t, "docs/design/notification.md")
	assertContainsNormalized(t, notificationDoc, "get-health, get-health-oneline, and the default TUI are three views over the same canonical contract.")
	assertContainsNormalized(t, notificationDoc, "## 1. Surfaces")
	assertContainsNormalized(t, notificationDoc, "## 2. Delivery Path")

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
	assertContainsNormalized(t, readme, "`get-health`, `get-health-oneline`, and the default TUI are views over the same canonical contract")
	assertContainsNormalized(t, readme, "Quick reading guide")
	assertContainsNormalized(t, readme, "Canonical visible state for a node right now")
	assertContainsNormalized(t, readme, "[docs/commands.md](docs/commands.md)")
	assertContainsNormalized(t, readme, "The README teaches the beginner/operator loop.")
	assertContainsNormalized(t, readme, "Use explicit subcommands; bare `tmux-a2a-postman` prints usage and does not start the daemon.")
	assertContainsNormalized(t, readme, "For stored messages written by `send`, reply guidance comes from `message_footer` in `internal/config/postman.default.toml`.")
	assertContainsNormalized(t, readme, "`pop` returns JSON that includes the stored message content as written and does not add a second hard-coded reply footer.")
	assertContainsNormalized(t, readme, "send: Sends messages to another node using tmux-a2a-postman send.")
	assertContainsNormalized(t, readme, "a2a-role-auditor: Audits node role templates to diagnose and fix node-to-node interaction breakdowns.")
	assertContainsNormalized(t, readme, "`postman.toml` is optional; without it, embedded defaults from `internal/config/postman.default.toml` are used.")
	for _, hidden := range []string{
		"tmux-a2a-postman read",
		"tmux-a2a-postman todo",
		"tmux-a2a-postman timeline",
		"tmux-a2a-postman replay",
		"tmux-a2a-postman schema",
		"tmux-a2a-postman bind",
		"tmux-a2a-postman get-context-id",
		"tmux-a2a-postman status",
		"`read_context_mode`",
		"`journal_health_cutover_enabled`",
		"`[heartbeat].enabled`",
		"waiting/",
		"--params",
		"--no-tui",
	} {
		if strings.Contains(readme, hidden) {
			t.Fatalf("README still exposes hidden public surface %q", hidden)
		}
	}

	sendSkill := readRepoFile(t, "skills/send-message/SKILL.md")
	assertContainsNormalized(t, sendSkill, "tmux-a2a-postman send --to <node> --body \"message text\"")
	assertContainsNormalized(t, sendSkill, "The public scope includes: `to`, `body`.")
	if strings.Contains(sendSkill, "schema") {
		t.Fatal("send skill still teaches schema discovery")
	}
	if strings.Contains(sendSkill, "--params") {
		t.Fatal("send skill still teaches removed params flag")
	}

	roleAuditorSkill := readRepoFile(t, "skills/a2a-role-auditor/SKILL.md")
	assertContainsNormalized(t, roleAuditorSkill, "unread backlog")
	assertContainsNormalized(t, roleAuditorSkill, "quiet node")
	assertContainsNormalized(t, roleAuditorSkill, "late reply")
	assertContainsNormalized(t, roleAuditorSkill, "get-health")
	assertContainsNormalized(t, roleAuditorSkill, "`message_footer` | appended to stored `send` mail | `{can_talk_to}`, `{reply_command}`")
	assertContainsNormalized(t, roleAuditorSkill, "`daemon_message_template` | daemon-originated mail | `{role_content}`, `{talks_to_line}`, `{reply_command}`")
	assertContainsNormalized(t, roleAuditorSkill, "Dead-letter recovery guidance (written by dead-letter notification code)")
	for _, hidden := range []string{"status --json", "dropped_ball", "heartbeat mail"} {
		if strings.Contains(roleAuditorSkill, hidden) {
			t.Fatalf("role auditor skill still exposes hidden term %q", hidden)
		}
	}
}

func TestReducedSurfaceDocContract_RuntimeLifecycleRetentionDocs(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	assertContainsNormalized(t, readme, "`retention_period_days` controls that startup cleanup window. The embedded default is `90`.")
	assertContainsNormalized(t, readme, "| `{baseDir}/lock/` | Active coordination state | Always preserved |")

	commandsDoc := readRepoFile(t, "docs/commands.md")
	assertContainsNormalized(t, commandsDoc, "## 7. Runtime Directory Lifecycle")
	assertContainsNormalized(t, commandsDoc, "`retention_period_days` controls cleanup of inactive runtime state.")
	assertContainsNormalized(t, commandsDoc, "Unknown entries are preserved by default instead of being pruned by name.")
}

func TestConfigSSOTDocContract(t *testing.T) {
	designDoc := readRepoFile(t, "docs/design/config-ssot.md")
	assertContainsNormalized(t, designDoc, "`internal/config/postman.default.toml` is the SSOT for user-configurable defaults.")
	assertContainsNormalized(t, designDoc, "`postman.toml` is optional.")
	assertContainsNormalized(t, designDoc, "`postman.md` may contain only a Mermaid `edges` section.")
	assertContainsNormalized(t, designDoc, "Nodes referenced by those edges are materialized with empty `NodeConfig` values.")

	commandsDoc := readRepoFile(t, "docs/commands.md")
	assertContainsNormalized(t, commandsDoc, "`postman.toml` is optional.")
	assertContainsNormalized(t, commandsDoc, "Embedded `postman.default.toml` is the SSOT for user-configurable defaults.")
}
