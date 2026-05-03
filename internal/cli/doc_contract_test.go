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
	commandsHelp := readRepoFile(t, "internal/cli/helptext/commands.txt")
	sendHelp := readRepoFile(t, "internal/cli/helptext/send.txt")
	popHelp := readRepoFile(t, "internal/cli/helptext/pop.txt")
	healthHelp := readRepoFile(t, "internal/cli/helptext/get-health.txt")
	onelineHelp := readRepoFile(t, "internal/cli/helptext/get-health-oneline.txt")
	assertContainsNormalized(t, commandsHelp, "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.")
	assertContainsNormalized(t, commandsHelp, "get-health Print canonical session health JSON for agents and scripts.")
	assertContainsNormalized(t, commandsHelp, "get-health-oneline Print compact all-session health for quick agent coordination.")
	assertContainsNormalized(t, commandsHelp, "version Print the build version JSON.")
	assertContainsNormalized(t, commandsHelp, "help [topic] Show help overview or detailed topic page.")
	assertContainsNormalized(t, onelineHelp, "[0]🔷🔵:🟢 [1]🔴")
	assertContainsNormalized(t, sendHelp, `{"sent":"filename.md","status":"processed","context_id":"...","session":"...","from":"sender","to":"recipient","submit_path":"daemon-submit"}`)
	assertContainsNormalized(t, popHelp, `{"status":"message","id":"filename.md","from":"...","to":"...","timestamp":"...","body":"...","content":"...","unread_before":1,"remaining":0}`)
	assertContainsNormalized(t, popHelp, "pop — read the next inbox message")
	assertContainsNormalized(t, sendHelp, "tmux-a2a-postman send --help")
	assertContainsNormalized(t, healthHelp, "Use nodes[*].visible_state for per-node state, queues for backlog counts, and compact for the compact display token.")
	helpSurface := commandsHelp + "\n" + sendHelp + "\n" + popHelp + "\n" + healthHelp + "\n" + onelineHelp
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
		if strings.Contains(helpSurface, hidden) {
			t.Fatalf("helptext exposes hidden public surface %s", hidden)
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
	assertContainsNormalized(t, readme, "tmux-a2a-postman help commands")
	assertContainsNormalized(t, readme, "The exact CLI reference is built into the binary")
	assertContainsNormalized(t, readme, "Use explicit subcommands; bare `tmux-a2a-postman` prints usage and does not start the daemon.")
	assertContainsNormalized(t, readme, "For stored messages written by `send`, reply guidance comes from `message_footer` in `internal/config/postman.default.toml`.")
	assertContainsNormalized(t, readme, "`pop` returns JSON that includes the stored message content as written and does not add a second hard-coded reply footer.")
	assertContainsNormalized(t, readme, "`postman-send-message`: minimal entry point for sending the first postman message.")
	assertContainsNormalized(t, readme, "`postman-config-auditor`: audits `postman.md`, `postman.toml`, `nodes/*`, topology, and node templates.")
	assertContainsNormalized(t, readme, "This repo does not currently define a skill deployment workflow")
	assertContainsNormalized(t, readme, "[skills/postman-config-auditor/references/postman-md.md](skills/postman-config-auditor/references/postman-md.md)")
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

	sendSkill := readRepoFile(t, "skills/postman-send-message/SKILL.md")
	assertContainsNormalized(t, sendSkill, "tmux-a2a-postman send --to <node> --body \"message text\"")
	assertContainsNormalized(t, sendSkill, "Use `tmux-a2a-postman help send` for details.")
	if strings.Contains(sendSkill, "schema") {
		t.Fatal("send skill still teaches schema discovery")
	}
	if strings.Contains(sendSkill, "--params") {
		t.Fatal("send skill still teaches removed params flag")
	}

	configAuditorSkill := readRepoFile(t, "skills/postman-config-auditor/SKILL.md")
	assertContainsNormalized(t, configAuditorSkill, "postman-config-auditor")
	assertContainsNormalized(t, configAuditorSkill, "unread backlog")
	assertContainsNormalized(t, configAuditorSkill, "quiet node")
	assertContainsNormalized(t, configAuditorSkill, "late reply")
	assertContainsNormalized(t, configAuditorSkill, "get-health")
	assertContainsNormalized(t, configAuditorSkill, "Use `references/postman-md.md` as the detailed syntax contract.")
	assertContainsNormalized(t, configAuditorSkill, "Project-local `postman.md` appends `message_footer` to the effective base footer.")
	assertContainsNormalized(t, configAuditorSkill, "`message_footer`, `draft_template`, `daemon_message_template`, `notification_template`, or dead-letter notification text.")
	postmanMDReference := readRepoFile(t, "skills/postman-config-auditor/references/postman-md.md")
	assertContainsNormalized(t, postmanMDReference, "The main `postman.md` parser only recognizes h2 headings that contain a backtick-wrapped name.")
	assertContainsNormalized(t, postmanMDReference, "Only `---` is parsed as an edge operator.")
	assertContainsNormalized(t, postmanMDReference, "Project-local `postman.md` `message_footer` appends to the effective base footer.")
	for _, hidden := range []string{"status --json", "dropped_ball", "heartbeat mail"} {
		if strings.Contains(configAuditorSkill, hidden) {
			t.Fatalf("config auditor skill still exposes hidden term %q", hidden)
		}
	}
}

func TestReducedSurfaceDocContract_RuntimeLifecycleRetentionDocs(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	assertContainsNormalized(t, readme, "`retention_period_days` controls that startup cleanup window. The embedded default is `90`.")
	assertContainsNormalized(t, readme, "| `{baseDir}/lock/` | Active coordination state | Always preserved |")

	directoriesHelp := readRepoFile(t, "internal/cli/helptext/directories.txt")
	assertContainsNormalized(t, directoriesHelp, "Directories — session directory layout")
	assertContainsNormalized(t, directoriesHelp, "$XDG_STATE_HOME/tmux-a2a-postman")
	assertContainsNormalized(t, directoriesHelp, "dead-letter/ # unroutable messages land here")
}

func TestConfigSSOTDocContract(t *testing.T) {
	designDoc := readRepoFile(t, "docs/design/config-ssot.md")
	assertContainsNormalized(t, designDoc, "`internal/config/postman.default.toml` is the SSOT for user-configurable defaults.")
	assertContainsNormalized(t, designDoc, "`postman.toml` is optional.")
	assertContainsNormalized(t, designDoc, "A minimal `postman.md` may contain only a Mermaid `edges` section.")
	assertContainsNormalized(t, designDoc, "Nodes referenced by those edges are materialized with empty `NodeConfig` values.")

	configHelp := readRepoFile(t, "internal/cli/helptext/config.txt")
	assertContainsNormalized(t, configHelp, "postman.toml is optional.")
	assertContainsNormalized(t, configHelp, "A minimal postman.md can contain only Mermaid edges")
}
