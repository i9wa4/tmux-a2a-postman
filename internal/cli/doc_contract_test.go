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

func TestFileFirstDocContract_ActiveHelpMatchesAdapters(t *testing.T) {
	commands := readRepoFile(t, "internal/cli/helptext/commands.txt")
	configuration := readRepoFile(t, "internal/cli/helptext/config.txt")
	send := readRepoFile(t, "internal/cli/helptext/send-heredoc.txt")
	pop := readRepoFile(t, "internal/cli/helptext/pop.txt")
	status := readRepoFile(t, "internal/cli/helptext/get-status.txt")
	execute := readRepoFile(t, "internal/cli/helptext/execute-bash.txt")
	assertContainsAllNormalized(t, commands, "send-heredoc", "pop", "get-status", "execute-bash", "--postman-md")
	assertContainsAllNormalized(t, configuration, "file-first workspace topology", "postman.md", "--postman-md", "strict Mermaid subset")
	assertContainsAllNormalized(t, send, "quoted delimiter", "--to", "--reply-to", "--fills-input-request-id", "--capture-related-panes", "notice.status=unavailable")
	assertContainsAllNormalized(t, pop, "markdown_absolute_path", "pop_receipt_absolute_path", "Read the complete archived Markdown body through EOF")
	assertContainsAllNormalized(t, status, "saved-file state", "does not register", "unknown is never reported as zero obligations")
	assertContainsAllNormalized(t, execute, "--command", "file-mail approval", "execution.notice", "emitted separately on stderr")
	for _, content := range []string{commands, send, pop, status, execute} {
		for _, obsolete := range []string{"daemon-submit", "--context-id", "--debug", "--tasks", "--verdict", "--evidence-", "--label", "--mode", "--reviewer", "--record-decision"} {
			if strings.Contains(content, obsolete) {
				t.Fatalf("active help retains unsupported flag or path %q", obsolete)
			}
		}
	}
}

func TestFileFirstDocContract_NotificationGuide(t *testing.T) {
	notificationDoc := readRepoFile(t, "docs/design/notification.md")
	assertContainsAllNormalized(t, notificationDoc,
		"Commit before cue",
		"receiver-inbox and sender-sent copies",
		"A failed or ambiguous notice never rolls back mail",
		"recipient reads the complete archived Markdown",
		"Generic tmux pane-input notification is therefore disabled.",
		"Only a verified same-message transition can be `sent`.",
		"The four core commands require no daemon",
	)
}

func TestFileFirstDocContract_NodeStateUsesSavedMail(t *testing.T) {
	stateDoc := readRepoFile(t, "docs/design/node-state-machine.md")
	assertContainsAllNormalized(t, stateDoc,
		"Saved mail and exact reply records are authoritative.",
		"A reply must name and satisfy that request",
		"exclusively claims one eligible inbox message",
		"reconstructs these facts from saved files",
		"`unknown`, never a claim that the agent is working",
	)
}

func TestFileFirstDocContract_ReadmeAndThreeSkills(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	commands := readRepoFile(t, "internal/cli/helptext/commands.txt")
	sendSkill := readRepoFile(t, "skills/postman-send-message/SKILL.md")
	usageSkill := readRepoFile(t, "skills/postman-usage/SKILL.md")
	auditorSkill := readRepoFile(t, "skills/postman-config-auditor/SKILL.md")
	assertContainsAllNormalized(t, readme,
		"file-first command path", "do not require a running Postman daemon",
		"send-heredoc", "pop", "get-status", "execute-bash",
		"markdown_absolute_path", "notice.status=unavailable",
		"gh skill install", "--agent codex", "--agent claude-code")
	assertContainsAllNormalized(t, commands, "send-heredoc", "pop", "get-status", "execute-bash")
	assertContainsAllNormalized(t, sendSkill, "quoted heredoc stdin", "post-operation self-state", "blind resend")
	assertContainsAllNormalized(t, usageSkill, "tmux-a2a-postman pop", "Read the complete archived Markdown through EOF")
	assertContainsAllNormalized(t, auditorSkill, "postman.md", "Mermaid edges")
	for path, content := range map[string]string{
		"README.md":            readme,
		"commands.txt":         commands,
		"postman-send-message": sendSkill,
		"postman-usage":        usageSkill,
	} {
		for _, obsolete := range []string{"tmux-a2a-postman start", "daemon-submit", "get-status-oneline", "inspect-daemon-submit", "--verdict", "--evidence-", "--label", "--mode blocking"} {
			if strings.Contains(content, obsolete) {
				t.Fatalf("%s retains obsolete workflow %q", path, obsolete)
			}
		}
	}
}

func TestReducedSurfaceDocContract_PostmanSendSkillForbidsPostSendPolling(t *testing.T) {
	sendSkill := readRepoFile(t, "skills/postman-send-message/SKILL.md")
	evalTask := readRepoFile(t, "evals/postman-send-message/tasks/post-send-polling-forbidden.yaml")

	assertContainsAllNormalized(
		t, sendSkill,
		"Read its delivery outcome, notification outcome, and mandatory post-operation self-state separately.",
		"Notification failure does not undo a committed delivery.",
		"Wait passively for a genuine notification or exact reply.",
		"If it reports known own unread mail, run one POP and read that archived body.",
		"Do not use repeated empty POP, sleep/POP, or status loops as a wait mechanism.",
		"postman-usage",
		"session flow",
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
	usageSkill := readRepoFile(t, "skills/postman-usage/SKILL.md")
	usageFlow := readRepoFile(t, "skills/postman-usage/references/session-flow.md")

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
		t, usageSkill,
		"Filling an input request closes transport, not task acceptance.",
		"Task artifact:",
		"Original checklist: PASS",
		"Remaining blockers: none",
		"Use `BLOCKED` with `Original checklist: FAIL`",
		"verify checklist status, durable references, evidence, and blockers before relaying, approving, or closing work",
	)
	assertContainsAllNormalized(
		t, usageFlow,
		"Filling transport does not accept work or authorize an otherwise forbidden action.",
	)
}

func TestFileFirstCommandHelpDescribesExactReply(t *testing.T) {
	help := readRepoFile(t, "internal/cli/helptext/execute-bash.txt")
	assertContainsAllNormalized(t, help,
		"--command", "command_approver_node", "reply_to",
		"fills_input_request_id", "approved or rejected",
		"Only verified reviewer absence permits advisory execution.")
	assertNotContainsNormalized(t, help, "--record-decision")
	assertNotContainsNormalized(t, help, "command_hash")
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
		"Scope includes the file-first runtime contract in issue #937.",
		"daemon PING injection modes described by the legacy runtime are pre-cutover behavior",
	)
	assertContainsAllNormalized(
		t, readme,
		"Claude Code and Codex CLI have different runtime surfaces outside postman",
		"docs/agent-runtime-feature-differences.md",
	)
	assertContainsAllNormalized(t, configSSOT,
		"postman.md` plus explicit CLI overrides",
		"A normal `skill_path` selects agent-facing `SKILL.md` metadata",
	)
	assertContainsAllNormalized(t, productDirection,
		"durable, private workspace mail",
		"four foreground commands",
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
	usageSkill := readRepoFile(t, "skills/postman-usage/SKILL.md")

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
	assertContainsNormalized(t, readme, "## 1. Install and configure")
	assertNotContainsNormalized(t, readme, "## Concept")
	assertContainsNormalized(t, usageSkill, "## 1. Claim and read mail")
	assertNotContainsNormalized(t, usageSkill, "## Claim and read mail")
}

func TestReducedSurfaceDocContract_RuntimeLifecycleRetentionDocs(t *testing.T) {
	configHelp := readRepoFile(t, "internal/cli/helptext/config.txt")
	assertContainsNormalized(t, configHelp, "file-first workspace topology")
	assertContainsNormalized(t, configHelp, "The legacy postman.toml daemon configuration")

	directoriesHelp := readRepoFile(t, "internal/cli/helptext/directories.txt")
	assertContainsNormalized(t, directoriesHelp, "Directories — session directory layout")
	assertContainsNormalized(t, directoriesHelp, "$XDG_STATE_HOME/tmux-a2a-postman")
	assertContainsNormalized(t, directoriesHelp, "dead-letter/ # unroutable messages land here")
}

func TestConfigSSOTDocContract(t *testing.T) {
	designDoc := readRepoFile(t, "docs/design/config-ssot.md")
	assertContainsAllNormalized(t, designDoc,
		"postman.md` plus explicit CLI overrides",
		"Only adjacent roles may exchange ordinary mail.",
		"A normal `skill_path` selects agent-facing `SKILL.md` metadata",
		"The four commands do not merge legacy `postman.toml`",
	)
	configHelp := readRepoFile(t, "internal/cli/helptext/config.txt")
	assertContainsAllNormalized(t, configHelp,
		"Each command accepts exactly one absolute --config or --postman-md override.",
		"strict Mermaid subset",
		"`interface_node` marks an interface role",
		"legacy postman.toml daemon configuration",
	)
}

func TestArchivedBodyReadPublicDocsContract(t *testing.T) {
	readme := readRepoFile(t, "README.md")
	messagingHelp := readRepoFile(t, "internal/cli/helptext/messaging.txt")
	usageSkill := readRepoFile(t, "skills/postman-usage/SKILL.md")

	assertContainsAllNormalized(
		t, readme,
		"Read the complete archived Markdown through EOF before handling the message.",
	)
	assertContainsAllNormalized(
		t, messagingHelp,
		"After every successful `pop` with `status=message`, read the complete archived Markdown body before any handling, routing, reply, status decision, or no-action or no-op decision.",
		"`messageType: ping`, `replyPolicy: none`, and other JSON metadata do not allow skipping the archived body.",
		"Truncated command output from `cat`, `sed`, `rg`, shell logs, or other bounded stdout paths is not a complete read; use a complete file-read path or verified chunks through EOF.",
	)
	assertContainsAllNormalized(
		t, usageSkill,
		"Read the complete archived Markdown through EOF before any handling",
		"frontmatter, reply policy, and notification text never substitute for this.",
	)
	assertNotContainsNormalized(t, usageSkill, "complete archived Markdown body by running `tmux-a2a-postman inspect-message --id <message_id> --body`")
}
