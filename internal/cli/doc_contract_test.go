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

func assertContainsAllNormalized(t *testing.T, got string, wants ...string) {
	t.Helper()

	for _, want := range wants {
		assertContainsNormalized(t, got, want)
	}
}

func TestReducedSurfaceDocContract_PopFileScopeAndCanonicalNames(t *testing.T) {
	commandsHelp := readRepoFile(t, "internal/cli/helptext/commands.txt")
	sendHelp := readRepoFile(t, "internal/cli/helptext/send-heredoc.txt")
	popHelp := readRepoFile(t, "internal/cli/helptext/pop.txt")
	statusHelp := readRepoFile(t, "internal/cli/helptext/get-status.txt")
	onelineHelp := readRepoFile(t, "internal/cli/helptext/get-status-oneline.txt")
	assertContainsNormalized(t, commandsHelp, "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.")
	assertContainsNormalized(t, commandsHelp, "get-status Print canonical session health JSON for agents and scripts.")
	assertContainsNormalized(t, commandsHelp, "get-status-oneline Print compact all-session health for quick agent coordination.")
	assertContainsNormalized(t, commandsHelp, "version Print the build version JSON.")
	assertContainsNormalized(t, commandsHelp, "help [topic] Show help overview or detailed topic page.")
	assertContainsNormalized(t, onelineHelp, "[0]🔷🟡:🟢 [1]🔴")
	assertContainsNormalized(t, sendHelp, `{"sent":"filename.md","status":"processed","context_id":"...","session":"...","from":"sender","to":"recipient","reply_policy":"none","submit_path":"daemon-submit"}`)
	assertContainsNormalized(t, popHelp, `{"status":"message","message_id":"filename.md","markdown_path":"/path/to/read/filename.md","frontmatter":{"params":{...}},"from":"...","to":"...","timestamp":"...","unread_before":1,"remaining":0}`)
	assertContainsNormalized(t, popHelp, "pop — read the next inbox message")
	assertContainsNormalized(t, sendHelp, "tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'")
	assertContainsNormalized(t, statusHelp, "Use nodes[*].visible_state for per-node state, queues for backlog counts, and compact for the compact display token.")
	helpSurface := commandsHelp + "\n" + sendHelp + "\n" + popHelp + "\n" + statusHelp + "\n" + onelineHelp
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
	assertContainsNormalized(t, notificationDoc, "The daemon delivers mail to the recipient inbox, sends a pane hint to that recipient when delivery succeeds, and emits auto-PING messages when the daemon starts or when a node appears.")
	assertContainsNormalized(t, notificationDoc, "If the same role reappears with a new pane ID, that replacement pane is treated as newly appeared.")
	assertContainsNormalized(t, notificationDoc, "`ui_node` is not a general escalation channel.")
	assertContainsNormalized(t, notificationDoc, "The remaining notification-related public settings are")
	assertContainsNormalized(t, notificationDoc, "Stored message Markdown is an envelope.")
	assertContainsNormalized(t, notificationDoc, "Sender Message")
	assertContainsNormalized(t, notificationDoc, "sender message section instead of becoming a top-level transport section")
}

func TestReducedSurfaceDocContract_NotificationDesignStartsFromUnifiedModel(t *testing.T) {
	notificationDoc := readRepoFile(t, "docs/design/notification.md")
	assertContainsNormalized(t, notificationDoc, "get-status, get-status-oneline, and the default TUI are three views over the same canonical contract.")
	assertContainsNormalized(t, notificationDoc, "Surface")
	assertContainsNormalized(t, notificationDoc, "Delivery")

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

func TestReducedSurfaceDocContract_ReadmeHelpAndSkillsSharePublicSurface(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	commandsHelp := readRepoFile(t, "internal/cli/helptext/commands.txt")
	sendSkill := readRepoFile(t, "skills/postman-send-message/SKILL.md")
	configAuditorSkill := readRepoFile(t, "skills/postman-config-auditor/SKILL.md")
	postmanMDReference := readRepoFile(t, "skills/postman-config-auditor/references/postman-md.md")

	assertContainsAllNormalized(t, readme,
		"postman daemon",
		"tmux pane",
		"Any AI coding agent",
		"filesystem-backed inboxes",
		"send",
		"pop",
		"get-status",
		"get-status-oneline",
		"Mermaid",
		"edges",
		"message footer",
		"pane notification",
		"gh skill install",
		"--agent codex",
		"--agent claude-code",
		"postman-send-message",
		"postman-config-auditor",
	)
	assertContainsAllNormalized(t, commandsHelp,
		"send",
		"pop",
		"get-status",
		"get-status-oneline",
		"version",
		"help [topic]",
	)
	assertContainsAllNormalized(t, sendSkill,
		"tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'",
		"Do not pass message text as a CLI argument, file-body shortcut, or generic pipe-oriented body.",
		"The sender is auto-detected from the current tmux pane title.",
		"tmux-a2a-postman help send-heredoc",
	)
	assertContainsAllNormalized(t, configAuditorSkill,
		"postman-config-auditor",
		"postman.md",
		"postman.toml",
		"get-status",
		"references/postman-md.md",
	)
	assertContainsAllNormalized(t, postmanMDReference,
		"Mermaid",
		"Only `---` is parsed as an edge operator.",
		"message_footer",
	)

	publicDocs := map[string]string{
		"README.md":                              readme,
		"internal/cli/helptext/commands.txt":     commandsHelp,
		"skills/postman-send-message/SKILL.md":   sendSkill,
		"skills/postman-config-auditor/SKILL.md": configAuditorSkill,
	}
	for path, content := range publicDocs {
		if strings.Contains(content, ".#skill-check") {
			t.Fatalf("%s exposes maintainer-only skill validation workflow", path)
		}
		if strings.Contains(content, "--pin") {
			t.Fatalf("%s exposes user-managed skill pinning workflow", path)
		}
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
			"status --json",
			"dropped_ball",
			"heartbeat mail",
		} {
			if strings.Contains(content, hidden) {
				t.Fatalf("%s exposes hidden public surface %q", path, hidden)
			}
		}
	}
}

func TestReducedSurfaceDocContract_MaintainerDocsCoverSkillReleaseFlow(t *testing.T) {
	contributing := readRepoFile(t, "CONTRIBUTING.md")
	assertContainsNormalized(t, contributing, "nix run '.#skill-check'")
	assertContainsNormalized(t, contributing, "Do not use `gh skill publish --tag` in the tag-push release workflow.")
	assertContainsNormalized(t, contributing, "The repository release flow uses the pushed `v*` tag plus GoReleaser")

	releasing := readRepoFile(t, "RELEASING.md")
	assertContainsNormalized(t, releasing, "Do not run `gh skill publish --tag` from the tag-push workflow.")
	assertContainsNormalized(t, releasing, "`gh skill publish --dry-run`; the published Git tag and GitHub Release are enough for `gh skill install` to resolve versions.")
}

func TestReducedSurfaceDocContract_RuntimeLifecycleRetentionDocs(t *testing.T) {
	configHelp := readRepoFile(t, "internal/cli/helptext/config.txt")
	assertContainsNormalized(t, configHelp, "retention_period_days            Inactive runtime cleanup window (default: 30; 0 = disabled)")

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
	assertContainsNormalized(t, designDoc, "marked in Mermaid with the `ui_node` class")

	configHelp := readRepoFile(t, "internal/cli/helptext/config.txt")
	assertContainsNormalized(t, configHelp, "postman.toml is optional.")
	assertContainsNormalized(t, configHelp, "A minimal postman.md can contain only Mermaid edges")
	assertContainsNormalized(t, configHelp, "Mermaid class <node> ui_node")
}
