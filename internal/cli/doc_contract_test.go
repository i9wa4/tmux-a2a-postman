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

func assertNotContainsNormalized(t *testing.T, got, forbidden string) {
	t.Helper()

	if strings.Contains(normalizeSpace(got), normalizeSpace(forbidden)) {
		t.Fatalf("content contains forbidden normalized substring %q", forbidden)
	}
}

func TestReducedSurfaceDocContract_PopFileScopeAndCanonicalNames(t *testing.T) {
	commandsHelp := readRepoFile(t, "internal/cli/helptext/commands.txt")
	sendHelp := readRepoFile(t, "internal/cli/helptext/send-heredoc.txt")
	popHelp := readRepoFile(t, "internal/cli/helptext/pop.txt")
	statusHelp := readRepoFile(t, "internal/cli/helptext/get-status.txt")
	onelineHelp := readRepoFile(t, "internal/cli/helptext/get-status-oneline.txt")
	assertContainsNormalized(t, commandsHelp, "Use an explicit command. Bare `tmux-a2a-postman` prints usage; it does not start the daemon.")
	assertContainsNormalized(t, commandsHelp, "get-status Print canonical session status JSON for agents and scripts.")
	assertContainsNormalized(t, commandsHelp, "get-status-oneline Print compact all-session status for quick agent coordination.")
	assertContainsNormalized(t, commandsHelp, "version Print the build version JSON.")
	assertContainsNormalized(t, commandsHelp, "help [topic] Show help overview or detailed topic page.")
	assertContainsNormalized(t, onelineHelp, "[0]🔷🟡:🟢 [1]⚫")
	assertContainsNormalized(t, sendHelp, `{"sent":"filename.md","status":"processed","context_id":"...","session":"...","from":"sender","to":"recipient","reply_policy":"none","submit_path":"daemon-submit"}`)
	assertContainsNormalized(t, popHelp, `{"status":"message","message_id":"filename.md","markdown_path":"~/.local/state/tmux-a2a-postman/.../read/filename.md","markdown_absolute_path":"/absolute/path/to/read/filename.md","frontmatter":{"params":{...}},"from":"...","to":"...","timestamp":"...","unread_before":1,"remaining":0,"archived_body_read_required":true`)
	assertContainsNormalized(t, popHelp, "Use markdown_absolute_path when present for programmatic file/body reads.")
	assertContainsNormalized(t, popHelp, "pop claims and archives the message; it never embeds full body text inline.")
	assertContainsNormalized(t, popHelp, "After every successful pop with status=message, read the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.")
	assertContainsNormalized(t, popHelp, "messageType: ping, replyPolicy: none, and other metadata do not waive this requirement.")
	assertContainsNormalized(t, popHelp, "Truncated command output from cat, sed, rg, shell logs, or other bounded stdout paths is not a valid archived-body read.")
	assertContainsNormalized(t, popHelp, "pop — claim and archive the next inbox message")
	assertContainsNormalized(t, sendHelp, "tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'")
	assertContainsNormalized(t, statusHelp, "Use nodes[*].visible_state for per-node state, queues for backlog counts, and compact for the compact display token.")
	assertContainsNormalized(t, statusHelp, "--debug adds runtime_diagnostics as an optional object without changing schema_version.")
	assertContainsNormalized(t, statusHelp, "It requests a point-in-time daemon runtime snapshot with Go memory, GC, goroutine, and count-only daemon cardinality fields.")
	assertContainsNormalized(t, statusHelp, "This is not a persisted time series.")
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
	assertContainsNormalized(t, notificationDoc, "The narrowing is intentional startup-noise control: an explicit non-empty `ui_node` declares the human-facing bootstrap entry point")
	assertContainsNormalized(t, notificationDoc, "The remaining notification-related public settings are")
	assertContainsNormalized(t, notificationDoc, "Stored message Markdown is an envelope.")
	assertContainsNormalized(t, notificationDoc, "Sender Message")
	assertContainsNormalized(t, notificationDoc, "Sender body Markdown is inserted verbatim after that separator")
	assertContainsNormalized(t, notificationDoc, "Recipient role instructions are normalized when inserted into the generated envelope")
	assertContainsNormalized(t, notificationDoc, "their shallowest ATX heading becomes `###`")
	assertContainsNormalized(t, notificationDoc, "without producing `#####` headings immediately under the generated `##` envelope heading")
	assertContainsNormalized(t, notificationDoc, "The recipient reads the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.")
	assertContainsNormalized(t, notificationDoc, "This applies to daemon PING mail, `messageType: ping`, `replyPolicy: none`, and every other message type.")
	assertContainsNormalized(t, notificationDoc, "truncated output is not a complete body read.")
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
	operatorSkill := readRepoFile(t, "skills/postman-session-operator/SKILL.md")
	postmanMDReference := readRepoFile(t, "skills/postman-config-auditor/references/postman-md.md")

	assertContainsAllNormalized(
		t, readme,
		"postman daemon",
		"tmux pane",
		"Any AI coding agent",
		"filesystem-backed inboxes",
		"send",
		"pop",
		"get-status",
		"get-status-oneline",
		"inspect-message --id <message_id>",
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
	assertContainsAllNormalized(
		t, commandsHelp,
		"send",
		"pop",
		"get-status",
		"get-status-oneline",
		"inspect-message",
		"version",
		"help [topic]",
	)
	assertContainsAllNormalized(
		t, operatorSkill,
		"tmux-a2a-postman inspect-message --id <message_id>",
		"read-only historical lookup",
		"Use `--path` for the stored Markdown path and `--body` for sender-authored body text.",
	)
	assertContainsAllNormalized(
		t, sendSkill,
		"tmux-a2a-postman send-heredoc --to <node> <<'POSTMAN_BODY'",
		"Do not pass message text as a CLI argument, file-body shortcut, or generic pipe-oriented body.",
		"The sender is auto-detected from the current tmux pane title.",
		"tmux-a2a-postman help send-heredoc",
	)
	assertContainsAllNormalized(
		t, configAuditorSkill,
		"postman-config-auditor",
		"postman.md",
		"postman.toml",
		"get-status",
		"references/postman-md.md",
	)
	assertContainsAllNormalized(
		t, postmanMDReference,
		"Mermaid",
		"Only `---` is parsed as an edge operator.",
		"message_footer",
	)

	publicDocs := map[string]string{
		"README.md":                                readme,
		"internal/cli/helptext/commands.txt":       commandsHelp,
		"skills/postman-send-message/SKILL.md":     sendSkill,
		"skills/postman-session-operator/SKILL.md": operatorSkill,
		"skills/postman-config-auditor/SKILL.md":   configAuditorSkill,
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

func TestReducedSurfaceDocContract_PostmanSendSkillForbidsPostSendPolling(t *testing.T) {
	sendSkill := readRepoFile(t, "skills/postman-send-message/SKILL.md")
	evalTask := readRepoFile(t, "evals/postman-send-message/tasks/post-send-polling-forbidden.yaml")

	assertContainsAllNormalized(
		t, sendSkill,
		"After a successful send:",
		"Informational or terminal send | Stop.",
		"Reply-required send | Wait for daemon notification or exact reply.",
		"Timeout/watchdog boundary | One bounded status check/follow-up.",
		"Suspected delivery/routing trouble | Use `postman-session-operator`.",
		"`pop` must not be used as a wait or poll mechanism after a successful send.",
		"Forbidden post-send wait patterns: repeated `pop`, `sleep && pop`, and mixed `pop`/`get-status` loops.",
		"skills/postman-session-operator/references/session-flow.md",
		"`waiting` and `expected_wait` handling.",
	)
	assertContainsAllNormalized(
		t, evalTask,
		"tmux-a2a-postman pop",
		"sleep && tmux-a2a-postman pop",
		"tmux-a2a-postman get-status",
		"wait for daemon notification",
	)
}

func TestRequiredReplyCompletionGateDocContract(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	messagingHelp := readRepoFile(t, "internal/cli/helptext/messaging.txt")
	defaultConfig := readRepoFile(t, "internal/config/postman.default.toml")
	operatorSkill := readRepoFile(t, "skills/postman-session-operator/SKILL.md")
	operatorFlow := readRepoFile(t, "skills/postman-session-operator/references/session-flow.md")

	assertContainsAllNormalized(
		t, defaultConfig,
		"required_reply_completion_gate",
		"completion proof guidance for reply-required messages; empty otherwise",
	)
	assertContainsAllNormalized(
		t, messagingHelp,
		"Filling an input request closes transport, not task acceptance.",
		"Task artifact: <artifact-reference>",
		"Original checklist: PASS",
		"Evidence: <commands, issue/PR links, tests, or verification output>",
		"Remaining blockers: none",
		"Use BLOCKED with Original checklist: FAIL",
		"Receivers should verify checklist status, durable references, evidence, and blockers before relaying, approving, or closing work.",
	)
	assertContainsAllNormalized(
		t, readme,
		"Filling an input request closes transport, not task acceptance.",
		"Task artifact",
		"Original checklist: PASS",
		"Remaining blockers: none",
		"Receivers verify the checklist status, durable references, evidence, and blockers before relaying, approving, or closing work.",
	)
	assertContainsAllNormalized(
		t, operatorSkill,
		"Filling an input request closes transport, not task acceptance.",
		"Task artifact: <artifact-reference>",
		"Original checklist: PASS",
		"Remaining blockers: none",
		"Use `BLOCKED` with `Original checklist: FAIL`",
		"check send JSON `fill`, `required_input`, and `notice`",
		"verify checklist status, durable references, evidence, and blockers before relaying, approving, or closing work",
	)
	assertContainsAllNormalized(
		t, operatorFlow,
		"`fill`, `required_input`, and `notice` fields before treating the input request as closed.",
	)
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

func TestAgentRuntimeFeatureDifferencesDocContract(t *testing.T) {
	runtimeDifferences := readRepoFile(t, "docs/agent-runtime-feature-differences.md")
	readme := readRepoFile(t, "README.md")
	configSSOT := readRepoFile(t, "docs/design/config-ssot.md")
	productDirection := readRepoFile(t, "docs/design/product-direction.md")
	postmanMDReference := readRepoFile(t, "skills/postman-config-auditor/references/postman-md.md")

	assertContainsAllNormalized(
		t, runtimeDifferences,
		"Feature / behavior area",
		"Claude Code behavior",
		"Codex CLI behavior",
		"Parity status",
		"Source / reference",
		"Owner / update trigger",
		"Last reviewed date",
		"Temporary task artifacts may record discoveries",
		"repo-relative paths or stable URLs only",
		"Runtime behavior changes need a separate issue.",
	)
	assertContainsAllNormalized(
		t, readme,
		"Claude Code and Codex CLI have different runtime surfaces outside postman",
		"docs/agent-runtime-feature-differences.md",
	)
	assertContainsAllNormalized(
		t, configSSOT,
		"Agent Runtime Feature Differences",
		"Do not encode runtime-specific behavior in `postman.toml` defaults",
	)
	assertContainsAllNormalized(
		t, productDirection,
		"which behavior belongs to tmux-a2a-postman and which behavior belongs to Claude Code or Codex CLI",
		"Agent Runtime Feature Differences",
	)
	assertContainsAllNormalized(
		t, postmanMDReference,
		"Agent Runtime Feature Differences",
		"do not duplicate the long-term runtime comparison here",
		"Only `skill_path` mappings accept `inject`.",
		"`inject: ping` stores the generated catalog for every daemon PING",
		"`inject: compaction_ping` stores the generated catalog for compaction-triggered daemon PING",
		"a YAML list containing `ping` and `compaction_ping` routes the same selected catalog to each listed daemon PING target.",
		"Flow-style YAML lists such as `inject: [ping, compaction_ping]` are accepted",
		"Path order controls duplicate precedence and the rendered source-path display order.",
		"PING event timing",
	)

	publicDocs := map[string]string{
		"README.md": readme,
		"docs/agent-runtime-feature-differences.md":              runtimeDifferences,
		"docs/design/config-ssot.md":                             configSSOT,
		"docs/design/product-direction.md":                       productDirection,
		"skills/postman-config-auditor/references/postman-md.md": postmanMDReference,
	}
	for path, content := range publicDocs {
		for _, localPath := range []string{"/home/", "/nix/store/", "~/ghq/"} {
			if strings.Contains(content, localPath) {
				t.Fatalf("%s exposes machine-local path fragment %q", path, localPath)
			}
		}
	}
}

func TestMarkdownFormatterHeadingPolicyDocContract(t *testing.T) {
	flake := readRepoFile(t, "flake.nix")
	contributing := readRepoFile(t, "CONTRIBUTING.md")
	runtimeDifferences := readRepoFile(t, "docs/agent-runtime-feature-differences.md")
	readme := readRepoFile(t, "README.md")
	operatorSkill := readRepoFile(t, "skills/postman-session-operator/SKILL.md")

	assertContainsAllNormalized(
		t, flake,
		"markdown-formatter = {",
		"name = \"markdown-formatter (all tracked markdown)\";",
		"entry = \"${markdownFormatter} --write\";",
		"types = [ \"markdown\" ];",
	)
	assertNotContainsNormalized(t, flake, "markdown-formatter-docs")
	assertNotContainsNormalized(t, flake, "markdown-formatter-stable-headings")
	assertNotContainsNormalized(t, flake, "--no-heading-numbering")
	assertContainsAllNormalized(
		t, contributing,
		"`markdown-formatter` covers all tracked Markdown files with its default heading-numbering behavior enabled.",
		"The repository does not maintain separate root-doc or skill exceptions.",
		"Ignored or generated files such as `.pre-commit-config.yaml` are not repository Markdown policy surfaces.",
	)

	assertContainsNormalized(t, runtimeDifferences, "## 1. Status Vocabulary")
	assertContainsNormalized(t, runtimeDifferences, "## 4. Verification")
	assertContainsNormalized(t, readme, "## 1. Concept")
	assertNotContainsNormalized(t, readme, "## Concept")
	assertContainsNormalized(t, operatorSkill, "## 1. USE FOR")
	assertNotContainsNormalized(t, operatorSkill, "## USE FOR")
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

func TestArchivedBodyReadPublicDocsContract(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	messagingHelp := readRepoFile(t, "internal/cli/helptext/messaging.txt")
	pathDisplayDoc := readRepoFile(t, "docs/design/path-display-policy.md")
	operatorSkill := readRepoFile(t, "skills/postman-session-operator/SKILL.md")

	assertContainsAllNormalized(
		t, readme,
		"After every successful `pop` with `status=message`, read the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.",
	)
	assertContainsAllNormalized(
		t, messagingHelp,
		"After every successful `pop` with `status=message`, read the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.",
		"`messageType: ping`, `replyPolicy: none`, and other JSON metadata do not allow skipping the archived body.",
		"Truncated command output from `cat`, `sed`, `rg`, shell logs, or other bounded stdout paths is not a complete read; use a complete file-read path or verified chunks through EOF.",
	)
	assertContainsAllNormalized(
		t, pathDisplayDoc,
		"After every successful `pop` with `status=message`, consumers must read the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.",
		"`messageType: ping`, `replyPolicy: none`, and other metadata do not waive the body-read requirement.",
		"A `cat`, `sed`, `rg`, shell log, or tool transcript that omits later body content does not count as a complete archived-body read.",
	)
	assertContainsAllNormalized(
		t, operatorSkill,
		"After every successful `pop` with `status=message`, read the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.",
		"`messageType: ping`, `replyPolicy: none`, and other metadata do not allow skipping the body.",
		"truncated command output does not count as a complete read.",
	)
	assertNotContainsNormalized(t, operatorSkill, "complete archived Markdown body by running `tmux-a2a-postman inspect-message --id <message_id> --body`")
}
