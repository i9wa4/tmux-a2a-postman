package envelope

import (
	"strings"
	"testing"

	"github.com/i9wa4/tmux-a2a-postman/internal/config"
	"github.com/i9wa4/tmux-a2a-postman/internal/discovery"
)

func TestBuildEnvelope_BasicExpansion(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	result := BuildEnvelope(cfg, "PING {node} in {context_id}", "worker", "postman", "test-ctx", "/session/post/file.md", []string{"worker"}, adjacency, nodes, "", livenessMap)

	if result != "PING worker in test-ctx" {
		t.Errorf("BuildEnvelope() = %q, want %q", result, "PING worker in test-ctx")
	}
}

func TestBuildEnvelope_NoVariables(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	result := BuildEnvelope(cfg, "PING message", "worker", "postman", "ctx", "/session/post/file.md", nil, adjacency, nodes, "", livenessMap)

	if result != "PING message" {
		t.Errorf("BuildEnvelope() = %q, want %q", result, "PING message")
	}
}

func TestBuildEnvelope_MissingVariable(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	result := BuildEnvelope(cfg, "PING {node} in {missing}", "worker", "postman", "ctx", "/session/post/file.md", nil, adjacency, nodes, "", livenessMap)

	if !strings.Contains(result, "PING worker") {
		t.Errorf("BuildEnvelope() = %q, want to contain 'PING worker'", result)
	}
	if !strings.Contains(result, "{missing}") {
		t.Errorf("BuildEnvelope() = %q, want to contain literal '{missing}'", result)
	}
}

func TestBuildEnvelope_SentinelObfuscation(t *testing.T) {
	nodeTemplate := "# WORKER\n<!-- end of message -->\nSome content"
	cfg := &config.Config{
		TmuxTimeout: 5.0,
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: nodeTemplate},
		},
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	result := BuildEnvelope(cfg, "<!-- message start -->\n{template}\n<!-- end of message -->\n", "worker", "postman", "ctx", "/session/post/file.md", nil, adjacency, nodes, "", livenessMap)

	if strings.Contains(result, "# WORKER\n<!-- end of message -->") {
		t.Errorf("user template sentinel was not obfuscated; result: %q", result)
	}
	if !strings.Contains(result, "<!-- end of msg -->") {
		t.Errorf("expected obfuscated sentinel in result; got: %q", result)
	}
	if !strings.HasSuffix(strings.TrimRight(result, "\n"), "<!-- end of message -->") {
		t.Errorf("protocol sentinel was altered or missing; result: %q", result)
	}
}

func TestBuildRoleContentDemotesTemplateHeadings(t *testing.T) {
	cfg := &config.Config{
		CommonTemplate: "# Common",
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: "## Worker\n\n```sh\n# keep code literal\n```"},
		},
	}

	got := BuildRoleContent(cfg, "worker")

	for _, want := range []string{
		"### Common",
		"#### Worker",
		"```sh\n# keep code literal\n```",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildRoleContent() missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"\n# Common",
		"\n## Worker",
		"```sh\n### keep code literal",
	} {
		if strings.Contains("\n"+got, unwanted) {
			t.Fatalf("BuildRoleContent() contains unwanted %q:\n%s", unwanted, got)
		}
	}
}

func TestBuildRoleContentWithAppendixDemotesExtraHeadings(t *testing.T) {
	cfg := &config.Config{
		CommonTemplate: "# Common",
		Nodes: map[string]config.NodeConfig{
			"worker": {Template: "## Worker"},
		},
	}

	got := BuildRoleContentWithAppendix(cfg, "worker", "### Available Skills\n\n- `bash`: Bash rules.")

	for _, want := range []string{
		"### Common",
		"#### Worker",
		"#### Available Skills",
		"- `bash`: Bash rules.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildRoleContentWithAppendix() missing %q:\n%s", want, got)
		}
	}
}

func TestBuildEnvelope_TalksToLine(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{
		"worker": {"orchestrator", "observer"},
	}
	nodes := map[string]discovery.NodeInfo{
		"test:orchestrator": {PaneID: "%2", SessionName: "test"},
		"test:observer":     {PaneID: "%3", SessionName: "test"},
	}
	livenessMap := map[string]bool{
		"test:orchestrator": true,
	}

	result := BuildEnvelope(cfg, "msg: {talks_to_line}", "worker", "postman", "ctx", "/session/post/file.md", nil, adjacency, nodes, "test", livenessMap)

	if !strings.Contains(result, "orchestrator") {
		t.Errorf("result = %q, want to contain 'orchestrator'", result)
	}
	if strings.Contains(result, "observer") {
		t.Errorf("result = %q, should not contain 'observer' (not liveness-confirmed)", result)
	}
}

func TestBuildEnvelope_InboxPath(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout: 5.0,
	}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	result := BuildEnvelope(cfg, "inbox: {inbox_path}", "worker", "postman", "ctx", "/my/session/post/file.md", nil, adjacency, nodes, "", livenessMap)

	if strings.Contains(result, "{inbox_path}") {
		t.Errorf("inbox_path was not expanded: %q", result)
	}
	if !strings.Contains(result, "/my/session/inbox/worker") {
		t.Errorf("result = %q, want to contain '/my/session/inbox/worker'", result)
	}
}

func TestBuildEnvelope_SentTimestamp(t *testing.T) {
	cfg := &config.Config{TmuxTimeout: 5.0}
	adjacency := map[string][]string{}
	nodes := map[string]discovery.NodeInfo{}
	livenessMap := map[string]bool{}

	tests := []struct {
		name     string
		filename string
		wantTS   string
	}{
		{
			name:     "valid YYYYMMDD-HHMMSS prefix",
			filename: "/session/post/20060102-150405-msg-from-worker.md",
			wantTS:   "20060102-150405",
		},
		{
			name:     "malformed filename no timestamp prefix",
			filename: "/session/post/foo-bar.md",
			wantTS:   "",
		},
		{
			name:     "correct length but non-digit chars",
			filename: "/session/post/ABCDEFGH-123456-rest.md",
			wantTS:   "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := BuildEnvelope(cfg, "{sent_timestamp}", "worker", "postman", "ctx", tc.filename, nil, adjacency, nodes, "", livenessMap)
			if result != tc.wantTS {
				t.Errorf("BuildEnvelope sent_timestamp = %q, want %q", result, tc.wantTS)
			}
		})
	}
}

func TestBuildEnvelope_DoesNotInjectContextIDForBareReplySendCommands(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout:  5.0,
		ReplyCommand: "send-heredoc --to orchestrator",
	}

	result := BuildEnvelope(cfg, "{reply_command}", "worker", "postman", "ctx-456", "/session/post/file.md", nil, map[string][]string{}, map[string]discovery.NodeInfo{}, "", map[string]bool{})

	if result != "send-heredoc --to orchestrator" {
		t.Fatalf("reply_command = %q, want canonical bare send unchanged", result)
	}
}

func TestRenderReplyCommand_PreservesMultilineFormatting(t *testing.T) {
	replyCommand := "tmux-a2a-postman send-heredoc --to <recipient> <<'POSTMAN_BODY'\n<your message>\nPOSTMAN_BODY"

	got := RenderReplyCommand(replyCommand, "ctx-789", "worker")

	if got != replyCommand {
		t.Fatalf("RenderReplyCommand() = %q, want %q", got, replyCommand)
	}
	if strings.Contains(got, "--to worker") {
		t.Fatalf("RenderReplyCommand() unexpectedly expanded recipient placeholder: %q", got)
	}
}

func TestBuildDaemonEnvelope_DoesNotExpandRecipientPlaceholder(t *testing.T) {
	cfg := &config.Config{
		TmuxTimeout:  5.0,
		ReplyCommand: "tmux-a2a-postman send-heredoc --to <recipient>",
	}

	result := BuildDaemonEnvelope(
		cfg,
		"Outer: {reply_command}",
		"messenger",
		"daemon",
		"ctx-daemon",
		"/session/post/file.md",
		nil,
		map[string][]string{},
		map[string]discovery.NodeInfo{},
		"review",
		nil,
	)

	if !strings.Contains(result, "--to <recipient>") {
		t.Fatalf("daemon envelope lost recipient placeholder: %q", result)
	}
	if strings.Contains(result, "--to messenger") {
		t.Fatalf("daemon envelope self-targeted ui node: %q", result)
	}
}

func TestRenderReplyCommand_ExpandsConfiguredPlaceholders(t *testing.T) {
	got := RenderReplyCommand("custom-reply --context {context_id} --node {node}", "ctx-wrapper", "worker")
	want := "custom-reply --context ctx-wrapper --node worker"
	if got != want {
		t.Fatalf("RenderReplyCommand() = %q, want %q", got, want)
	}
}

func TestMarkdownSectionContentDemotesATXHeadingsOutsideFences(t *testing.T) {
	input := "# Top\n\n## Child\n\n```sh\n# keep shell comment\n```\n\n###### Deep\n"

	got := MarkdownSectionContent(input)

	for _, want := range []string{
		"### Top",
		"#### Child",
		"```sh\n# keep shell comment\n```",
		"###### Deep",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("MarkdownSectionContent() missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"\n# Top",
		"\n## Child",
		"```sh\n### keep shell comment",
	} {
		if strings.Contains("\n"+got, unwanted) {
			t.Fatalf("MarkdownSectionContent() contains unwanted %q:\n%s", unwanted, got)
		}
	}
}

func TestMarkdownSectionContentPreservesFenceLikeContentLines(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantCode   string
		wantBody   string
		unwanted   string
		wantDemote string
	}{
		{
			name: "backtick content line with trailing text",
			input: strings.Join([]string{
				"```text",
				"```literal fence text",
				"# still code",
				"```",
				"# outside",
				"",
			}, "\n"),
			wantCode:   "```literal fence text\n# still code",
			wantBody:   "```\n### outside",
			unwanted:   "```literal fence text\n### still code",
			wantDemote: "### outside",
		},
		{
			name: "tilde content line with trailing text",
			input: strings.Join([]string{
				"~~~text",
				"~~~literal fence text",
				"# still code",
				"~~~",
				"## outside",
				"",
			}, "\n"),
			wantCode:   "~~~literal fence text\n# still code",
			wantBody:   "~~~\n#### outside",
			unwanted:   "~~~literal fence text\n### still code",
			wantDemote: "#### outside",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MarkdownSectionContent(tc.input)
			for _, want := range []string{tc.wantCode, tc.wantBody, tc.wantDemote} {
				if !strings.Contains(got, want) {
					t.Fatalf("MarkdownSectionContent() missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, tc.unwanted) {
				t.Fatalf("MarkdownSectionContent() contains unwanted %q:\n%s", tc.unwanted, got)
			}
		})
	}
}
